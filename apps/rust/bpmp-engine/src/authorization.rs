use bpmp_adapter_identity_jwt::JwtIdentityVerifier;
use bpmp_adapter_policy_bundle::VerifiedAuthorizationStore;
use bpmp_authz_contracts::{
    ActorProofCodec, AuthorizationKeyring, AuthorizationProofLimits, WorkloadProofCodec,
};
use bpmp_authz_engine::{AuthorizationInput, DecisionType, ResourceAuthorizationInput};
use bpmp_domain_core::{ActorId, PolicyVersion};

use crate::ports::{
    ActorProofKind, AuthorizationError, AuthorizationProviderPort, AuthorizationRequest,
    AuthorizedPrincipal,
};

struct VerifiedActor {
    tenant_id: String,
    actor_id: String,
    roles: Vec<String>,
    capabilities: Vec<String>,
    revoke_epoch: u64,
    audience_workload_id: Option<String>,
}

struct VerifiedRequestIdentity {
    actor: VerifiedActor,
    workload_id: String,
}

/// Fail-closed embedded identity and policy authorization adapter.
pub struct EmbeddedAuthorizationProvider {
    actor_keys: AuthorizationKeyring,
    workload_keys: AuthorizationKeyring,
    proof_limits: AuthorizationProofLimits,
    policies: VerifiedAuthorizationStore,
    jwt_verifier: Option<JwtIdentityVerifier>,
}

impl EmbeddedAuthorizationProvider {
    pub const fn new(
        actor_keys: AuthorizationKeyring,
        workload_keys: AuthorizationKeyring,
        proof_limits: AuthorizationProofLimits,
        policies: VerifiedAuthorizationStore,
    ) -> Self {
        Self {
            actor_keys,
            workload_keys,
            proof_limits,
            policies,
            jwt_verifier: None,
        }
    }

    #[must_use]
    pub fn with_jwt_verifier(mut self, verifier: JwtIdentityVerifier) -> Self {
        self.jwt_verifier = Some(verifier);
        self
    }

    pub const fn policies(&self) -> &VerifiedAuthorizationStore {
        &self.policies
    }

    /// Installs a newly published signed policy bundle in the local evaluator.
    pub fn install_policy_bundle(
        &self,
        bytes: &[u8],
    ) -> Result<
        bpmp_adapter_policy_bundle::InstallOutcome,
        bpmp_adapter_policy_bundle::AuthorizationStoreError,
    > {
        self.policies.install_signed_bundle(bytes)
    }
}

impl AuthorizationProviderPort for EmbeddedAuthorizationProvider {
    fn verify_proofs(&self, request: &AuthorizationRequest<'_>) -> Result<(), AuthorizationError> {
        self.verify_request_identity(request).map(|_| ())
    }

    fn authorize(
        &self,
        request: &AuthorizationRequest<'_>,
    ) -> Result<AuthorizedPrincipal, AuthorizationError> {
        let verified = self.verify_request_identity(request)?;
        let actor = verified.actor;

        let resource_decision = request
            .resource
            .as_ref()
            .map(|resource| {
                self.policies
                    .evaluate_resource_if_catalogued(&ResourceAuthorizationInput {
                        tenant_id: request.tenant_id.as_str(),
                        actor_id: &actor.actor_id,
                        roles: &actor.roles,
                        capabilities: &actor.capabilities,
                        actor_proof_revoke_epoch: actor.revoke_epoch,
                        evaluated_at_epoch_ms: request.evaluated_at_epoch_ms,
                        owner_id: resource.owner_id,
                        resource_type: resource.resource_type,
                        action: resource.action,
                        resource_id: resource.resource_id,
                        resource_owner_actor_id: resource
                            .resource_owned_by_actor
                            .then_some(actor.actor_id.as_str()),
                        resource_branch_path: resource.branch_path,
                        resource_queue_id: resource.queue_id,
                    })
            })
            .transpose()
            .map_err(|error| AuthorizationError::Unavailable(error.to_string()))?
            .flatten();

        let (policy_version, bundle_sequence, required_revoke_epoch, matched_grant_ids) =
            if let Some(decision) = resource_decision {
                if decision.decision == DecisionType::Deny {
                    return Err(AuthorizationError::Denied(
                        decision
                            .deny_reason
                            .map_or("UNKNOWN_DENY".to_owned(), |reason| reason.code().to_owned()),
                    ));
                }
                (
                    decision.policy_version,
                    decision.bundle_sequence,
                    decision.required_revoke_epoch,
                    decision.matched_grant_ids,
                )
            } else {
                let decision = self
                    .policies
                    .evaluate(&AuthorizationInput {
                        tenant_id: request.tenant_id.as_str(),
                        actor_id: &actor.actor_id,
                        roles: &actor.roles,
                        capabilities: &actor.capabilities,
                        actor_proof_revoke_epoch: actor.revoke_epoch,
                        evaluated_at_epoch_ms: request.evaluated_at_epoch_ms,
                        workflow_type: request.workflow_type.as_str(),
                        workflow_version: request.workflow_version.as_str(),
                        active_node_id: request.active_node_id,
                        action: request.action,
                    })
                    .map_err(|error| AuthorizationError::Unavailable(error.to_string()))?;
                if decision.decision == DecisionType::Deny {
                    return Err(AuthorizationError::Denied(
                        decision
                            .deny_reason
                            .map_or("UNKNOWN_DENY".to_owned(), |reason| reason.code().to_owned()),
                    ));
                }
                (
                    decision.policy_version,
                    decision.bundle_sequence,
                    decision.required_revoke_epoch,
                    decision.matched_grant_ids,
                )
            };

        Ok(AuthorizedPrincipal {
            actor_id: ActorId::new(actor.actor_id)
                .map_err(|_| AuthorizationError::InvalidVerifiedIdentity)?,
            roles: actor.roles,
            workload_id: verified.workload_id,
            policy_version: PolicyVersion::new(policy_version)
                .map_err(|_| AuthorizationError::InvalidVerifiedIdentity)?,
            bundle_sequence,
            revoke_epoch: required_revoke_epoch,
            matched_grant_ids,
        })
    }
}

impl EmbeddedAuthorizationProvider {
    fn verify_request_identity(
        &self,
        request: &AuthorizationRequest<'_>,
    ) -> Result<VerifiedRequestIdentity, AuthorizationError> {
        let actor = self.verify_actor(request)?;
        let workload = WorkloadProofCodec::open(
            request.workload_proof,
            &self.workload_keys,
            self.proof_limits,
        )
        .map_err(|error| AuthorizationError::InvalidWorkloadProof(error.to_string()))?;
        if actor.tenant_id != request.tenant_id.as_str()
            || workload.tenant_id != request.tenant_id.as_str()
            || workload.command_id != request.command_id.as_str()
        {
            return Err(AuthorizationError::ScopeMismatch);
        }
        if !valid_at(
            workload.issued_at_epoch_ms,
            workload.expires_at_epoch_ms,
            request.evaluated_at_epoch_ms,
        ) {
            return Err(AuthorizationError::WorkloadProofOutsideValidity);
        }
        if actor
            .audience_workload_id
            .as_deref()
            .is_some_and(|audience| audience != workload.workload_id)
        {
            return Err(AuthorizationError::WorkloadAudienceMismatch);
        }
        let required_revoke_epoch = self
            .policies
            .required_revoke_epoch(request.tenant_id.as_str(), &actor.actor_id)
            .map_err(|error| AuthorizationError::Unavailable(error.to_string()))?;
        if actor.revoke_epoch < required_revoke_epoch {
            return Err(AuthorizationError::Denied("ACTOR_PROOF_REVOKED".into()));
        }
        Ok(VerifiedRequestIdentity {
            actor,
            workload_id: workload.workload_id,
        })
    }

    fn verify_actor(
        &self,
        request: &AuthorizationRequest<'_>,
    ) -> Result<VerifiedActor, AuthorizationError> {
        match request.actor_proof_kind {
            ActorProofKind::SignedInternalContext => {
                let actor =
                    ActorProofCodec::open(request.actor_proof, &self.actor_keys, self.proof_limits)
                        .map_err(|error| {
                            AuthorizationError::InvalidActorProof(error.to_string())
                        })?;
                if actor.command_id != request.command_id.as_str() {
                    return Err(AuthorizationError::ScopeMismatch);
                }
                if !valid_at(
                    actor.issued_at_epoch_ms,
                    actor.expires_at_epoch_ms,
                    request.evaluated_at_epoch_ms,
                ) {
                    return Err(AuthorizationError::ActorProofOutsideValidity);
                }
                Ok(VerifiedActor {
                    tenant_id: actor.tenant_id,
                    actor_id: actor.actor_id,
                    roles: actor.roles,
                    capabilities: actor.capabilities,
                    revoke_epoch: actor.revoke_epoch,
                    audience_workload_id: Some(actor.audience_workload_id),
                })
            }
            ActorProofKind::OriginalJwt => {
                let verifier = self.jwt_verifier.as_ref().ok_or_else(|| {
                    AuthorizationError::Unavailable("JWT verifier is not configured".into())
                })?;
                let token = std::str::from_utf8(request.actor_proof).map_err(|_| {
                    AuthorizationError::InvalidActorProof("JWT is not UTF-8".into())
                })?;
                let claims = verifier
                    .verify(token, request.evaluated_at_epoch_ms / 1_000)
                    .map_err(|error| AuthorizationError::InvalidActorProof(error.to_string()))?;
                Ok(VerifiedActor {
                    tenant_id: claims.tenant_id,
                    actor_id: claims.actor_id,
                    roles: claims.roles,
                    capabilities: claims.capabilities,
                    revoke_epoch: claims.revoke_epoch,
                    audience_workload_id: None,
                })
            }
        }
    }
}

const fn valid_at(issued_at_epoch_ms: u64, expires_at_epoch_ms: u64, evaluated_at: u64) -> bool {
    issued_at_epoch_ms <= evaluated_at && evaluated_at < expires_at_epoch_ms
}
