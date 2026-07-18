use std::collections::BTreeMap;

use prost::Message;
use sha2::{Digest, Sha256};
use thiserror::Error;

use crate::Ed25519Signer;
use crate::authorization::v1::{
    AuthorizationPolicyBundle, AuthorizationPolicyEffect, AuthorizationRevokeEpochUpdate,
};
use crate::signing::Ed25519Verifier;

pub const AUTHORIZATION_BUNDLE_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct AuthorizationArtifactLimits {
    max_artifact_bytes: usize,
    max_grants: usize,
}

impl AuthorizationArtifactLimits {
    /// Creates deploy-time limits for untrusted authorization artifacts.
    ///
    /// # Errors
    ///
    /// Both limits must be greater than zero.
    pub const fn new(
        max_artifact_bytes: usize,
        max_grants: usize,
    ) -> Result<Self, AuthorizationArtifactError> {
        if max_artifact_bytes == 0 || max_grants == 0 {
            Err(AuthorizationArtifactError::InvalidLimits)
        } else {
            Ok(Self {
                max_artifact_bytes,
                max_grants,
            })
        }
    }
}

/// Signing operation used by the policy control plane.
pub trait AuthorizationArtifactSigner {
    fn sign(&self, digest: &[u8; 32]) -> Vec<u8>;
}

impl AuthorizationArtifactSigner for Ed25519Signer {
    fn sign(&self, digest: &[u8; 32]) -> Vec<u8> {
        self.sign_digest(digest)
    }
}

/// Immutable verification-key registry configured at the data-plane boundary.
pub struct AuthorizationKeyring {
    keys: BTreeMap<String, Ed25519Verifier>,
}

impl AuthorizationKeyring {
    pub const fn new() -> Self {
        Self {
            keys: BTreeMap::new(),
        }
    }

    /// Adds a public key under its rotation-safe identifier.
    ///
    /// # Errors
    ///
    /// Rejects empty/duplicate identifiers and malformed Ed25519 keys.
    pub fn insert(
        &mut self,
        key_id: impl Into<String>,
        public_key: &[u8; 32],
    ) -> Result<(), AuthorizationArtifactError> {
        let key_id = key_id.into();
        if key_id.is_empty() {
            return Err(AuthorizationArtifactError::InvalidKeyId);
        }
        if self.keys.contains_key(&key_id) {
            return Err(AuthorizationArtifactError::DuplicateKeyId(key_id));
        }
        let verifier = Ed25519Verifier::from_bytes(public_key)
            .map_err(|()| AuthorizationArtifactError::InvalidPublicKey)?;
        self.keys.insert(key_id, verifier);
        Ok(())
    }

    pub(crate) fn verify(
        &self,
        key_id: &str,
        digest: &[u8; 32],
        signature: &[u8],
    ) -> Result<(), AuthorizationArtifactError> {
        let verifier = self
            .keys
            .get(key_id)
            .ok_or_else(|| AuthorizationArtifactError::UnknownKey(key_id.to_owned()))?;
        verifier
            .verify_digest(digest, signature)
            .map_err(|()| AuthorizationArtifactError::InvalidSignature)
    }
}

impl Default for AuthorizationKeyring {
    fn default() -> Self {
        Self::new()
    }
}

pub struct AuthorizationBundleCodec;

impl AuthorizationBundleCodec {
    /// Canonicalizes, hashes, signs, and serializes a policy bundle.
    ///
    /// # Errors
    ///
    /// Rejects invalid schema, scope, validity, grant, or revocation data.
    pub fn seal(
        mut bundle: AuthorizationPolicyBundle,
        key_id: &str,
        signer: &dyn AuthorizationArtifactSigner,
        limits: AuthorizationArtifactLimits,
    ) -> Result<Vec<u8>, AuthorizationArtifactError> {
        validate_key_id(key_id)?.clone_into(&mut bundle.signing_key_id);
        canonicalize_bundle(&mut bundle);
        validate_bundle(&bundle, limits)?;
        let digest = bundle_digest(&bundle);
        bundle.content_hash = digest.to_vec();
        bundle.signature = signer.sign(&digest);
        let encoded = bundle.encode_to_vec();
        check_size(&encoded, limits)?;
        Ok(encoded)
    }

    /// Decodes and verifies a signed canonical policy bundle.
    ///
    /// # Errors
    ///
    /// Fails closed for oversized, malformed, non-canonical, stale-schema,
    /// hash-mismatched, unknown-key, or signature-invalid artifacts.
    pub fn open(
        bytes: &[u8],
        keyring: &AuthorizationKeyring,
        limits: AuthorizationArtifactLimits,
    ) -> Result<AuthorizationPolicyBundle, AuthorizationArtifactError> {
        check_size(bytes, limits)?;
        let bundle = AuthorizationPolicyBundle::decode(bytes)
            .map_err(|error| AuthorizationArtifactError::Decode(error.to_string()))?;
        if bundle.encode_to_vec() != bytes {
            return Err(AuthorizationArtifactError::NonCanonicalArtifact);
        }
        validate_bundle(&bundle, limits)?;
        let mut canonical = bundle.clone();
        canonicalize_bundle(&mut canonical);
        if canonical.grants != bundle.grants
            || canonical.actor_revoke_epochs != bundle.actor_revoke_epochs
        {
            return Err(AuthorizationArtifactError::NonCanonicalArtifact);
        }
        let digest = bundle_digest(&bundle);
        if bundle.content_hash.as_slice() != digest {
            return Err(AuthorizationArtifactError::HashMismatch);
        }
        keyring.verify(&bundle.signing_key_id, &digest, &bundle.signature)?;
        Ok(bundle)
    }
}

pub struct AuthorizationRevokeCodec;

impl AuthorizationRevokeCodec {
    /// Hashes, signs, and serializes an epoch update.
    ///
    /// # Errors
    ///
    /// Rejects invalid schema, tenant, key, sequence, or epoch fields.
    pub fn seal(
        mut update: AuthorizationRevokeEpochUpdate,
        key_id: &str,
        signer: &dyn AuthorizationArtifactSigner,
        limits: AuthorizationArtifactLimits,
    ) -> Result<Vec<u8>, AuthorizationArtifactError> {
        validate_key_id(key_id)?.clone_into(&mut update.signing_key_id);
        validate_revoke_update(&update)?;
        let digest = revoke_digest(&update);
        update.content_hash = digest.to_vec();
        update.signature = signer.sign(&digest);
        let encoded = update.encode_to_vec();
        check_size(&encoded, limits)?;
        Ok(encoded)
    }

    /// Decodes and verifies a signed revoke-epoch update.
    ///
    /// # Errors
    ///
    /// Fails closed on malformed content, hash mismatch, unknown keys, or an
    /// invalid signature.
    pub fn open(
        bytes: &[u8],
        keyring: &AuthorizationKeyring,
        limits: AuthorizationArtifactLimits,
    ) -> Result<AuthorizationRevokeEpochUpdate, AuthorizationArtifactError> {
        check_size(bytes, limits)?;
        let update = AuthorizationRevokeEpochUpdate::decode(bytes)
            .map_err(|error| AuthorizationArtifactError::Decode(error.to_string()))?;
        if update.encode_to_vec() != bytes {
            return Err(AuthorizationArtifactError::NonCanonicalArtifact);
        }
        validate_revoke_update(&update)?;
        let digest = revoke_digest(&update);
        if update.content_hash.as_slice() != digest {
            return Err(AuthorizationArtifactError::HashMismatch);
        }
        keyring.verify(&update.signing_key_id, &digest, &update.signature)?;
        Ok(update)
    }
}

fn canonicalize_bundle(bundle: &mut AuthorizationPolicyBundle) {
    for grant in &mut bundle.grants {
        grant.actor_ids.sort_unstable();
        grant.actor_ids.dedup();
        grant.roles.sort_unstable();
        grant.roles.dedup();
        grant.required_capabilities.sort_unstable();
        grant.required_capabilities.dedup();
    }
    bundle
        .grants
        .sort_unstable_by(|left, right| left.grant_id.cmp(&right.grant_id));
    bundle
        .actor_revoke_epochs
        .sort_unstable_by(|left, right| left.actor_id.cmp(&right.actor_id));
}

fn validate_bundle(
    bundle: &AuthorizationPolicyBundle,
    limits: AuthorizationArtifactLimits,
) -> Result<(), AuthorizationArtifactError> {
    validate_schema(bundle.schema_version)?;
    validate_nonempty("tenant_id", &bundle.tenant_id)?;
    validate_nonempty("policy_version", &bundle.policy_version)?;
    validate_key_id(&bundle.signing_key_id)?;
    if bundle.bundle_sequence == 0 {
        return Err(AuthorizationArtifactError::InvalidField("bundle_sequence"));
    }
    if bundle.valid_from_epoch_ms >= bundle.expires_at_epoch_ms {
        return Err(AuthorizationArtifactError::InvalidValidityWindow);
    }
    if bundle.grants.len() > limits.max_grants {
        return Err(AuthorizationArtifactError::TooManyGrants {
            actual: bundle.grants.len(),
            maximum: limits.max_grants,
        });
    }
    for pair in bundle.grants.windows(2) {
        if pair[0].grant_id == pair[1].grant_id {
            return Err(AuthorizationArtifactError::DuplicateGrantId(
                pair[0].grant_id.clone(),
            ));
        }
    }
    for grant in &bundle.grants {
        validate_nonempty("grant_id", &grant.grant_id)?;
        validate_nonempty_values("actor_ids", &grant.actor_ids)?;
        validate_nonempty_values("roles", &grant.roles)?;
        validate_nonempty_values("required_capabilities", &grant.required_capabilities)?;
        validate_selector("workflow_type", &grant.workflow_type)?;
        validate_selector("workflow_version", &grant.workflow_version)?;
        validate_selector("active_node_id", &grant.active_node_id)?;
        validate_selector("action", &grant.action)?;
        let effect = AuthorizationPolicyEffect::try_from(grant.effect)
            .map_err(|_| AuthorizationArtifactError::InvalidPolicyEffect(grant.effect))?;
        if effect == AuthorizationPolicyEffect::Unspecified {
            return Err(AuthorizationArtifactError::InvalidPolicyEffect(
                grant.effect,
            ));
        }
    }
    for pair in bundle.actor_revoke_epochs.windows(2) {
        if pair[0].actor_id == pair[1].actor_id {
            return Err(AuthorizationArtifactError::DuplicateActorEpoch(
                pair[0].actor_id.clone(),
            ));
        }
    }
    for actor in &bundle.actor_revoke_epochs {
        validate_nonempty("actor_id", &actor.actor_id)?;
        if actor.revoke_epoch < bundle.revoke_epoch {
            return Err(AuthorizationArtifactError::ActorEpochBelowTenantEpoch);
        }
    }
    Ok(())
}

fn validate_revoke_update(
    update: &AuthorizationRevokeEpochUpdate,
) -> Result<(), AuthorizationArtifactError> {
    validate_schema(update.schema_version)?;
    validate_nonempty("tenant_id", &update.tenant_id)?;
    validate_key_id(&update.signing_key_id)?;
    if update.bundle_sequence == 0 {
        return Err(AuthorizationArtifactError::InvalidField("bundle_sequence"));
    }
    if update.revoke_epoch == 0 {
        return Err(AuthorizationArtifactError::InvalidField("revoke_epoch"));
    }
    if update.issued_at_epoch_ms == 0 {
        return Err(AuthorizationArtifactError::InvalidField(
            "issued_at_epoch_ms",
        ));
    }
    Ok(())
}

fn validate_schema(schema_version: u32) -> Result<(), AuthorizationArtifactError> {
    if schema_version == AUTHORIZATION_BUNDLE_SCHEMA_VERSION {
        Ok(())
    } else {
        Err(AuthorizationArtifactError::UnsupportedSchema {
            expected: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
            actual: schema_version,
        })
    }
}

fn validate_key_id(key_id: &str) -> Result<&str, AuthorizationArtifactError> {
    if key_id.is_empty() {
        Err(AuthorizationArtifactError::InvalidKeyId)
    } else {
        Ok(key_id)
    }
}

fn validate_nonempty(field: &'static str, value: &str) -> Result<(), AuthorizationArtifactError> {
    if value.is_empty() {
        Err(AuthorizationArtifactError::InvalidField(field))
    } else {
        Ok(())
    }
}

fn validate_selector(field: &'static str, value: &str) -> Result<(), AuthorizationArtifactError> {
    validate_nonempty(field, value)
}

fn validate_nonempty_values(
    field: &'static str,
    values: &[String],
) -> Result<(), AuthorizationArtifactError> {
    if values.iter().any(String::is_empty) {
        Err(AuthorizationArtifactError::InvalidField(field))
    } else {
        Ok(())
    }
}

fn check_size(
    bytes: &[u8],
    limits: AuthorizationArtifactLimits,
) -> Result<(), AuthorizationArtifactError> {
    if bytes.len() > limits.max_artifact_bytes {
        Err(AuthorizationArtifactError::ArtifactTooLarge {
            actual: bytes.len(),
            maximum: limits.max_artifact_bytes,
        })
    } else {
        Ok(())
    }
}

fn bundle_digest(bundle: &AuthorizationPolicyBundle) -> [u8; 32] {
    let mut unsigned = bundle.clone();
    unsigned.content_hash.clear();
    unsigned.signature.clear();
    Sha256::digest(unsigned.encode_to_vec()).into()
}

fn revoke_digest(update: &AuthorizationRevokeEpochUpdate) -> [u8; 32] {
    let mut unsigned = update.clone();
    unsigned.content_hash.clear();
    unsigned.signature.clear();
    Sha256::digest(unsigned.encode_to_vec()).into()
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum AuthorizationArtifactError {
    #[error("authorization artifact limits must be greater than zero")]
    InvalidLimits,
    #[error("unsupported authorization artifact schema {actual}; expected {expected}")]
    UnsupportedSchema { expected: u32, actual: u32 },
    #[error("authorization artifact cannot be decoded: {0}")]
    Decode(String),
    #[error("authorization artifact exceeds {maximum} bytes: {actual}")]
    ArtifactTooLarge { actual: usize, maximum: usize },
    #[error("authorization artifact is not canonically ordered")]
    NonCanonicalArtifact,
    #[error("authorization artifact content hash does not match its payload")]
    HashMismatch,
    #[error("authorization artifact signature is invalid")]
    InvalidSignature,
    #[error("authorization signing key identifier is invalid")]
    InvalidKeyId,
    #[error("authorization signing key identifier is duplicated: {0}")]
    DuplicateKeyId(String),
    #[error("authorization signing key is unknown: {0}")]
    UnknownKey(String),
    #[error("Ed25519 public key is invalid")]
    InvalidPublicKey,
    #[error("authorization artifact field is invalid: {0}")]
    InvalidField(&'static str),
    #[error("authorization bundle validity window is empty or reversed")]
    InvalidValidityWindow,
    #[error("authorization bundle has {actual} grants; maximum is {maximum}")]
    TooManyGrants { actual: usize, maximum: usize },
    #[error("authorization grant identifier is duplicated: {0}")]
    DuplicateGrantId(String),
    #[error("actor revoke epoch is duplicated: {0}")]
    DuplicateActorEpoch(String),
    #[error("actor revoke epoch is below the tenant revoke epoch")]
    ActorEpochBelowTenantEpoch,
    #[error("authorization policy effect value is invalid: {0}")]
    InvalidPolicyEffect(i32),
}

#[cfg(test)]
mod tests {
    use crate::Ed25519Signer;
    use crate::authorization::v1::{
        ActorRevokeEpoch, AuthorizationPolicyBundle, AuthorizationPolicyEffect,
        AuthorizationPolicyGrant,
    };

    use super::*;

    fn bundle() -> AuthorizationPolicyBundle {
        AuthorizationPolicyBundle {
            schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            bundle_sequence: 9,
            policy_version: "policy-v9".into(),
            revoke_epoch: 3,
            valid_from_epoch_ms: 100,
            expires_at_epoch_ms: 1_000,
            grants: vec![
                AuthorizationPolicyGrant {
                    grant_id: "z-grant".into(),
                    actor_ids: vec!["actor-z".into(), "actor-a".into()],
                    roles: vec!["reviewer".into(), "admin".into()],
                    required_capabilities: Vec::new(),
                    workflow_type: "order".into(),
                    workflow_version: "1".into(),
                    active_node_id: "review".into(),
                    action: "APPROVE".into(),
                    effect: AuthorizationPolicyEffect::Allow.into(),
                    priority: 1,
                },
                AuthorizationPolicyGrant {
                    grant_id: "a-grant".into(),
                    actor_ids: Vec::new(),
                    roles: vec!["blocked".into()],
                    required_capabilities: Vec::new(),
                    workflow_type: "*".into(),
                    workflow_version: "*".into(),
                    active_node_id: "*".into(),
                    action: "APPROVE".into(),
                    effect: AuthorizationPolicyEffect::Deny.into(),
                    priority: 100,
                },
            ],
            actor_revoke_epochs: vec![
                ActorRevokeEpoch {
                    actor_id: "actor-z".into(),
                    revoke_epoch: 4,
                },
                ActorRevokeEpoch {
                    actor_id: "actor-a".into(),
                    revoke_epoch: 5,
                },
            ],
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        }
    }

    fn signer_and_keyring() -> (Ed25519Signer, AuthorizationKeyring) {
        let signer = Ed25519Signer::from_bytes(&[11; 32]);
        let mut keyring = AuthorizationKeyring::new();
        keyring
            .insert("rotation-key-2", &signer.verifying_key_bytes())
            .unwrap();
        (signer, keyring)
    }

    fn limits() -> AuthorizationArtifactLimits {
        AuthorizationArtifactLimits::new(1024 * 1024, 1_000).unwrap()
    }

    #[test]
    fn seal_canonicalizes_and_open_verifies() {
        let (signer, keyring) = signer_and_keyring();
        let encoded =
            AuthorizationBundleCodec::seal(bundle(), "rotation-key-2", &signer, limits()).unwrap();
        let opened = AuthorizationBundleCodec::open(&encoded, &keyring, limits()).unwrap();
        assert_eq!(opened.grants[0].grant_id, "a-grant");
        assert_eq!(opened.grants[1].actor_ids, ["actor-a", "actor-z"]);
        assert_eq!(opened.actor_revoke_epochs[0].actor_id, "actor-a");
    }

    #[test]
    fn unknown_key_and_tampering_fail_closed() {
        let (signer, _) = signer_and_keyring();
        let encoded =
            AuthorizationBundleCodec::seal(bundle(), "rotation-key-2", &signer, limits()).unwrap();
        assert!(matches!(
            AuthorizationBundleCodec::open(&encoded, &AuthorizationKeyring::new(), limits()),
            Err(AuthorizationArtifactError::UnknownKey(_))
        ));

        let (_, keyring) = signer_and_keyring();
        let mut tampered = encoded;
        let last = tampered.len() - 1;
        tampered[last] ^= 1;
        assert!(AuthorizationBundleCodec::open(&tampered, &keyring, limits()).is_err());
    }
}
