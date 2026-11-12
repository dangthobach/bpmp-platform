use std::collections::BTreeMap;
use std::sync::RwLock;

use bpmp_authz_contracts::authorization::v1::AuthorizationPolicyBundle;
use bpmp_authz_contracts::{
    AuthorizationArtifactError, AuthorizationArtifactLimits, AuthorizationBundleCodec,
    AuthorizationKeyring, AuthorizationRevokeCodec,
};
use bpmp_authz_engine::{
    AuthorizationDecision, AuthorizationInput, ResourceAuthorizationDecision,
    ResourceAuthorizationInput, RevocationSnapshot, evaluate, evaluate_resource,
};
use thiserror::Error;

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum InstallOutcome {
    Installed,
    AlreadyCurrent,
}

struct TenantAuthorizationState {
    bundle: AuthorizationPolicyBundle,
    tenant_revoke_floor: u64,
    actor_revoke_floors: BTreeMap<String, u64>,
}

/// Verified in-memory policy state used by the embedded authorization path.
///
/// Signature verification happens before the write lock is acquired. Reads
/// hold a shared lock only for the deterministic in-memory evaluation.
pub struct VerifiedAuthorizationStore {
    keyring: AuthorizationKeyring,
    limits: AuthorizationArtifactLimits,
    tenants: RwLock<BTreeMap<String, TenantAuthorizationState>>,
}

impl VerifiedAuthorizationStore {
    /// Returns the active tenant/actor revocation floor without evaluating a
    /// resource policy. Command handlers can reject stale proofs before loading
    /// protected resource data.
    pub fn required_revoke_epoch(
        &self,
        tenant_id: &str,
        actor_id: &str,
    ) -> Result<u64, AuthorizationStoreError> {
        let tenants = self
            .tenants
            .read()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        let state = tenants
            .get(tenant_id)
            .ok_or_else(|| AuthorizationStoreError::MissingBundle(tenant_id.to_owned()))?;
        let actor_epoch = state
            .actor_revoke_floors
            .get(actor_id)
            .copied()
            .unwrap_or(0);
        Ok(state.tenant_revoke_floor.max(actor_epoch))
    }

    pub const fn new(keyring: AuthorizationKeyring, limits: AuthorizationArtifactLimits) -> Self {
        Self {
            keyring,
            limits,
            tenants: RwLock::new(BTreeMap::new()),
        }
    }

    /// Verifies and atomically installs a signed policy bundle.
    ///
    /// # Errors
    ///
    /// Rejects invalid signatures, sequence rollback, epoch rollback, or a
    /// conflicting artifact at the current sequence.
    pub fn install_signed_bundle(
        &self,
        bytes: &[u8],
    ) -> Result<InstallOutcome, AuthorizationStoreError> {
        let bundle = self.verify_signed_bundle(bytes)?;
        self.install_verified_bundle(bundle)
    }

    /// Verifies, durably persists, re-verifies the authoritative stored bundle,
    /// and only then makes it active for authorization decisions.
    pub fn install_signed_bundle_durably<F>(
        &self,
        bytes: &[u8],
        persist: F,
    ) -> Result<(InstallOutcome, AuthorizationPolicyBundle), AuthorizationStoreError>
    where
        F: FnOnce(&AuthorizationPolicyBundle, &[u8]) -> Result<Vec<u8>, String>,
    {
        let incoming = self.verify_signed_bundle(bytes)?;
        let persisted = persist(&incoming, bytes).map_err(AuthorizationStoreError::Persistence)?;
        let current = self.verify_signed_bundle(&persisted)?;
        if current.tenant_id != incoming.tenant_id {
            return Err(AuthorizationStoreError::PersistedBundleMismatch);
        }
        if current.bundle_sequence < incoming.bundle_sequence {
            return Err(AuthorizationStoreError::BundleSequenceRollback {
                current: current.bundle_sequence,
                incoming: incoming.bundle_sequence,
            });
        }
        if current.bundle_sequence == incoming.bundle_sequence
            && current.content_hash != incoming.content_hash
        {
            return Err(AuthorizationStoreError::BundleSequenceConflict {
                sequence: incoming.bundle_sequence,
            });
        }
        let outcome = self.install_verified_bundle(current)?;
        Ok((outcome, incoming))
    }

    /// Verifies a signed bundle without changing active evaluator state.
    pub fn verify_signed_bundle(
        &self,
        bytes: &[u8],
    ) -> Result<AuthorizationPolicyBundle, AuthorizationStoreError> {
        AuthorizationBundleCodec::open(bytes, &self.keyring, self.limits).map_err(Into::into)
    }

    pub fn has_bundle(&self, tenant_id: &str) -> Result<bool, AuthorizationStoreError> {
        let tenants = self
            .tenants
            .read()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        Ok(tenants.contains_key(tenant_id))
    }

    /// Installs a bundle after the caller has durably persisted its verified bytes.
    fn install_verified_bundle(
        &self,
        bundle: AuthorizationPolicyBundle,
    ) -> Result<InstallOutcome, AuthorizationStoreError> {
        let mut tenants = self
            .tenants
            .write()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        if let Some(current) = tenants.get(&bundle.tenant_id) {
            if bundle.bundle_sequence < current.bundle.bundle_sequence {
                return Err(AuthorizationStoreError::BundleSequenceRollback {
                    current: current.bundle.bundle_sequence,
                    incoming: bundle.bundle_sequence,
                });
            }
            if bundle.bundle_sequence == current.bundle.bundle_sequence {
                return if bundle.content_hash == current.bundle.content_hash {
                    Ok(InstallOutcome::AlreadyCurrent)
                } else {
                    Err(AuthorizationStoreError::BundleSequenceConflict {
                        sequence: bundle.bundle_sequence,
                    })
                };
            }
            if bundle.revoke_epoch < current.tenant_revoke_floor {
                return Err(AuthorizationStoreError::RevokeEpochRollback {
                    current: current.tenant_revoke_floor,
                    incoming: bundle.revoke_epoch,
                });
            }
        }

        let tenant_id = bundle.tenant_id.clone();
        let previous = tenants.remove(&tenant_id);
        let tenant_revoke_floor = previous.as_ref().map_or(bundle.revoke_epoch, |state| {
            state.tenant_revoke_floor.max(bundle.revoke_epoch)
        });
        let actor_revoke_floors =
            previous.map_or_else(BTreeMap::new, |state| state.actor_revoke_floors);
        tenants.insert(
            tenant_id,
            TenantAuthorizationState {
                bundle,
                tenant_revoke_floor,
                actor_revoke_floors,
            },
        );
        Ok(InstallOutcome::Installed)
    }

    /// Verifies and applies a tenant or actor revoke floor.
    ///
    /// An empty `actor_id` updates the tenant floor. Equal epochs are
    /// idempotent; lower epochs and updates for another bundle are rejected.
    ///
    /// # Errors
    ///
    /// Rejects invalid signatures, missing tenant bundles, sequence mismatch,
    /// and revoke-epoch rollback.
    pub fn apply_signed_revoke_update(
        &self,
        bytes: &[u8],
    ) -> Result<InstallOutcome, AuthorizationStoreError> {
        let update = AuthorizationRevokeCodec::open(bytes, &self.keyring, self.limits)?;
        let mut tenants = self
            .tenants
            .write()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        let state = tenants
            .get_mut(&update.tenant_id)
            .ok_or_else(|| AuthorizationStoreError::MissingBundle(update.tenant_id.clone()))?;
        if update.bundle_sequence != state.bundle.bundle_sequence {
            return Err(AuthorizationStoreError::RevokeBundleSequenceMismatch {
                current: state.bundle.bundle_sequence,
                incoming: update.bundle_sequence,
            });
        }
        let current = if update.actor_id.is_empty() {
            &mut state.tenant_revoke_floor
        } else {
            state
                .actor_revoke_floors
                .entry(update.actor_id)
                .or_insert(0)
        };
        if update.revoke_epoch < *current {
            return Err(AuthorizationStoreError::RevokeEpochRollback {
                current: *current,
                incoming: update.revoke_epoch,
            });
        }
        if update.revoke_epoch == *current {
            return Ok(InstallOutcome::AlreadyCurrent);
        }
        *current = update.revoke_epoch;
        Ok(InstallOutcome::Installed)
    }

    /// Evaluates against the currently verified tenant bundle.
    ///
    /// # Errors
    ///
    /// Missing bundle and poisoned state are returned as errors and must be
    /// mapped to DENY by the command boundary.
    pub fn evaluate(
        &self,
        input: &AuthorizationInput<'_>,
    ) -> Result<AuthorizationDecision, AuthorizationStoreError> {
        let tenants = self
            .tenants
            .read()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        let state = tenants
            .get(input.tenant_id)
            .ok_or_else(|| AuthorizationStoreError::MissingBundle(input.tenant_id.to_owned()))?;
        let actor_epoch = state
            .actor_revoke_floors
            .get(input.actor_id)
            .copied()
            .unwrap_or(0);
        Ok(evaluate(
            &state.bundle,
            input,
            RevocationSnapshot {
                tenant_epoch: state.tenant_revoke_floor,
                actor_epoch,
            },
        ))
    }

    /// Evaluates an owner resource/action against the current verified bundle.
    /// Resource branch ancestry and queue identity must be supplied by the
    /// authoritative owner service. The store applies its local revoke floors.
    pub fn evaluate_resource(
        &self,
        input: &ResourceAuthorizationInput<'_>,
    ) -> Result<ResourceAuthorizationDecision, AuthorizationStoreError> {
        let tenants = self
            .tenants
            .read()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        let state = tenants
            .get(input.tenant_id)
            .ok_or_else(|| AuthorizationStoreError::MissingBundle(input.tenant_id.to_owned()))?;
        let actor_epoch = state
            .actor_revoke_floors
            .get(input.actor_id)
            .copied()
            .unwrap_or(0);
        Ok(evaluate_resource(
            &state.bundle,
            input,
            RevocationSnapshot {
                tenant_epoch: state.tenant_revoke_floor,
                actor_epoch,
            },
        ))
    }

    /// Evaluates resource policy when the signed catalog declares the action.
    /// Returns `None` only when the action is not cataloged for the owner, so
    /// callers can preserve a separately versioned transition policy during
    /// phased migration. A cataloged action always uses resource grants.
    pub fn evaluate_resource_if_catalogued(
        &self,
        input: &ResourceAuthorizationInput<'_>,
    ) -> Result<Option<ResourceAuthorizationDecision>, AuthorizationStoreError> {
        let tenants = self
            .tenants
            .read()
            .map_err(|_| AuthorizationStoreError::LockPoisoned)?;
        let state = tenants
            .get(input.tenant_id)
            .ok_or_else(|| AuthorizationStoreError::MissingBundle(input.tenant_id.to_owned()))?;
        if !state.bundle.resource_catalog.iter().any(|definition| {
            definition.owner_id == input.owner_id
                && (definition.resource_type == input.resource_type
                    || definition.resource_type == "*")
                && (definition.action == input.action || definition.action == "*")
        }) {
            return Ok(None);
        }
        let actor_epoch = state
            .actor_revoke_floors
            .get(input.actor_id)
            .copied()
            .unwrap_or(0);
        Ok(Some(evaluate_resource(
            &state.bundle,
            input,
            RevocationSnapshot {
                tenant_epoch: state.tenant_revoke_floor,
                actor_epoch,
            },
        )))
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum AuthorizationStoreError {
    #[error(transparent)]
    Artifact(#[from] AuthorizationArtifactError),
    #[error("authorization state lock is poisoned")]
    LockPoisoned,
    #[error("authorization bundle persistence failed: {0}")]
    Persistence(String),
    #[error("persisted authorization bundle does not match the requested tenant")]
    PersistedBundleMismatch,
    #[error("verified authorization bundle is missing for tenant {0}")]
    MissingBundle(String),
    #[error("authorization bundle sequence rollback: current {current}, incoming {incoming}")]
    BundleSequenceRollback { current: u64, incoming: u64 },
    #[error("authorization bundle sequence {sequence} has conflicting content")]
    BundleSequenceConflict { sequence: u64 },
    #[error("revoke epoch rollback: current {current}, incoming {incoming}")]
    RevokeEpochRollback { current: u64, incoming: u64 },
    #[error("revoke update targets bundle sequence {incoming}, but current sequence is {current}")]
    RevokeBundleSequenceMismatch { current: u64, incoming: u64 },
}

#[cfg(test)]
mod tests {
    use bpmp_authz_contracts::authorization::v1::{
        AuthorizationPolicyBundle, AuthorizationPolicyEffect, AuthorizationPolicyGrant,
        AuthorizationRevokeEpochUpdate,
    };
    use bpmp_authz_contracts::{
        AUTHORIZATION_BUNDLE_SCHEMA_VERSION, AuthorizationArtifactLimits, AuthorizationBundleCodec,
        AuthorizationKeyring, AuthorizationRevokeCodec, Ed25519Signer,
    };
    use bpmp_authz_engine::{AuthorizationInput, DecisionType, DenyReason};

    use super::*;

    fn signer_and_store() -> (Ed25519Signer, VerifiedAuthorizationStore) {
        let signer = Ed25519Signer::from_bytes(&[9; 32]);
        let mut keyring = AuthorizationKeyring::new();
        keyring
            .insert("key-1", &signer.verifying_key_bytes())
            .unwrap();
        (signer, VerifiedAuthorizationStore::new(keyring, limits()))
    }

    fn limits() -> AuthorizationArtifactLimits {
        AuthorizationArtifactLimits::new(1024 * 1024, 1_000).unwrap()
    }

    fn bundle(sequence: u64, revoke_epoch: u64) -> AuthorizationPolicyBundle {
        AuthorizationPolicyBundle {
            schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            bundle_sequence: sequence,
            policy_version: format!("policy-v{sequence}"),
            revoke_epoch,
            valid_from_epoch_ms: 100,
            expires_at_epoch_ms: 1_000,
            grants: vec![AuthorizationPolicyGrant {
                grant_id: "allow-start".into(),
                actor_ids: vec!["actor-1".into()],
                roles: Vec::new(),
                required_capabilities: vec!["workflow.start".into()],
                workflow_type: "order".into(),
                workflow_version: "1".into(),
                active_node_id: "start".into(),
                action: "START".into(),
                effect: AuthorizationPolicyEffect::Allow.into(),
                priority: 10,
            }],
            catalog_version: 0,
            resource_grants: Vec::new(),
            resource_catalog: Vec::new(),
            actor_revoke_epochs: Vec::new(),
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        }
    }

    fn input(capabilities: &[String], proof_epoch: u64) -> AuthorizationInput<'_> {
        AuthorizationInput {
            tenant_id: "tenant-a",
            actor_id: "actor-1",
            roles: &[],
            capabilities,
            actor_proof_revoke_epoch: proof_epoch,
            evaluated_at_epoch_ms: 500,
            workflow_type: "order",
            workflow_version: "1",
            active_node_id: "start",
            action: "START",
        }
    }

    #[test]
    fn verifies_bundle_before_allowing_and_rejects_tampering() {
        let (signer, store) = signer_and_store();
        let encoded =
            AuthorizationBundleCodec::seal(bundle(1, 2), "key-1", &signer, limits()).unwrap();
        assert_eq!(
            store.install_signed_bundle(&encoded).unwrap(),
            InstallOutcome::Installed
        );
        let capabilities = ["workflow.start".to_owned()];
        assert_eq!(
            store.evaluate(&input(&capabilities, 2)).unwrap().decision,
            DecisionType::Allow
        );

        let mut tampered = encoded;
        let last = tampered.len() - 1;
        tampered[last] ^= 1;
        assert!(matches!(
            store.install_signed_bundle(&tampered),
            Err(AuthorizationStoreError::Artifact(_))
        ));
    }

    #[test]
    fn signed_revoke_epoch_invalidates_stale_actor_proof() {
        let (signer, store) = signer_and_store();
        let encoded =
            AuthorizationBundleCodec::seal(bundle(1, 2), "key-1", &signer, limits()).unwrap();
        store.install_signed_bundle(&encoded).unwrap();
        let update = AuthorizationRevokeEpochUpdate {
            schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            actor_id: "actor-1".into(),
            revoke_epoch: 5,
            bundle_sequence: 1,
            issued_at_epoch_ms: 450,
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        };
        let encoded_update =
            AuthorizationRevokeCodec::seal(update, "key-1", &signer, limits()).unwrap();
        store.apply_signed_revoke_update(&encoded_update).unwrap();

        let capabilities = ["workflow.start".to_owned()];
        let decision = store.evaluate(&input(&capabilities, 4)).unwrap();
        assert_eq!(decision.decision, DecisionType::Deny);
        assert_eq!(decision.deny_reason, Some(DenyReason::ActorProofRevoked));
        assert_eq!(decision.required_revoke_epoch, 5);
    }

    #[test]
    fn rejects_bundle_and_epoch_rollback() {
        let (signer, store) = signer_and_store();
        let current =
            AuthorizationBundleCodec::seal(bundle(2, 7), "key-1", &signer, limits()).unwrap();
        store.install_signed_bundle(&current).unwrap();

        let stale =
            AuthorizationBundleCodec::seal(bundle(1, 7), "key-1", &signer, limits()).unwrap();
        assert_eq!(
            store.install_signed_bundle(&stale),
            Err(AuthorizationStoreError::BundleSequenceRollback {
                current: 2,
                incoming: 1,
            })
        );

        let rollback = AuthorizationRevokeEpochUpdate {
            schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            actor_id: String::new(),
            revoke_epoch: 6,
            bundle_sequence: 2,
            issued_at_epoch_ms: 500,
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        };
        let encoded = AuthorizationRevokeCodec::seal(rollback, "key-1", &signer, limits()).unwrap();
        assert_eq!(
            store.apply_signed_revoke_update(&encoded),
            Err(AuthorizationStoreError::RevokeEpochRollback {
                current: 7,
                incoming: 6,
            })
        );
    }
}
