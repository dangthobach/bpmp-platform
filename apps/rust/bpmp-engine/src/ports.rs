use std::sync::Arc;

use bpmp_domain_core::{
    ActorId, CommandId, ConfigError, IdempotencyKey, InstanceId, PolicyVersion,
    ResolvedConfigSnapshot, TenantId, WorkflowType, WorkflowVersion,
};
use thiserror::Error;

use crate::{AuthorizationAudit, CommittedResult, EventEnvelope, SnapshotEnvelope};

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum ActorProofKind {
    OriginalJwt,
    SignedInternalContext,
}

pub struct AuthorizationRequest<'a> {
    pub tenant_id: &'a TenantId,
    pub command_id: &'a CommandId,
    pub evaluated_at_epoch_ms: u64,
    pub actor_proof: &'a [u8],
    pub actor_proof_kind: ActorProofKind,
    pub workload_proof: &'a [u8],
    pub workflow_type: &'a WorkflowType,
    pub workflow_version: &'a WorkflowVersion,
    pub active_node_id: &'a str,
    pub action: &'a str,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct AuthorizedPrincipal {
    pub actor_id: ActorId,
    pub roles: Vec<String>,
    pub workload_id: String,
    pub policy_version: PolicyVersion,
    pub bundle_sequence: u64,
    pub revoke_epoch: u64,
    pub matched_grant_ids: Vec<String>,
}

pub trait AuthorizationProviderPort: Send + Sync {
    /// Verifies both identities and authorizes the concrete transition.
    ///
    /// # Errors
    ///
    /// Every verification, scope, validity, revocation, or policy failure is
    /// fail-closed and returned as [`AuthorizationError`].
    fn authorize(
        &self,
        request: &AuthorizationRequest<'_>,
    ) -> Result<AuthorizedPrincipal, AuthorizationError>;
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum AuthorizationError {
    #[error("actor proof is invalid: {0}")]
    InvalidActorProof(String),
    #[error("workload proof is invalid: {0}")]
    InvalidWorkloadProof(String),
    #[error("authorization proof scope does not match the command")]
    ScopeMismatch,
    #[error("actor proof is not valid at the injected evaluation time")]
    ActorProofOutsideValidity,
    #[error("workload proof is not valid at the injected evaluation time")]
    WorkloadProofOutsideValidity,
    #[error("actor proof audience does not match the verified workload")]
    WorkloadAudienceMismatch,
    #[error("authorization policy denied the transition: {0}")]
    Denied(String),
    #[error("authorization state is unavailable: {0}")]
    Unavailable(String),
    #[error("verified authorization identity or policy version is invalid")]
    InvalidVerifiedIdentity,
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
pub struct ConfigurationLookup {
    pub tenant_id: TenantId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
}

pub trait ConfigurationProviderPort: Send + Sync {
    /// Resolves the last valid published snapshot for the complete workflow scope.
    ///
    /// # Errors
    ///
    /// Returns [`ConfigError`] when no valid snapshot exists or validation fails.
    fn resolve(&self, lookup: &ConfigurationLookup) -> Result<ResolvedConfigSnapshot, ConfigError>;
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct LoadedInstance {
    pub snapshot: Option<SnapshotEnvelope>,
    pub events: Vec<EventEnvelope>,
    pub version: u64,
}

pub struct CommitRequest {
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub actor_id: ActorId,
    pub idempotency_key: IdempotencyKey,
    pub command_id: CommandId,
    pub expected_version: u64,
    pub events: Vec<EventEnvelope>,
    pub snapshot: Option<SnapshotEnvelope>,
    pub authorization_audit: AuthorizationAudit,
    pub result: CommittedResult,
}

impl CommitRequest {
    /// Validates cross-record scope before an adapter creates its atomic batch.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::InvalidAuthorizationAudit`] when audit metadata
    /// does not describe exactly this committed command and transition.
    pub fn validate_authorization_audit(&self) -> Result<(), StoreError> {
        let audit = &self.authorization_audit;
        if audit.tenant_id != self.tenant_id
            || audit.actor_id != self.actor_id
            || audit.command_id != self.command_id
            || audit.policy_version != self.result.policy_version
            || audit.config_version != self.result.config_version
            || audit.decision_id.is_empty()
            || audit.workload_id.is_empty()
            || audit.action.is_empty()
            || audit.matched_grant_ids.is_empty()
            || self.events.iter().any(|event| {
                event.metadata.tenant_id != audit.tenant_id
                    || event.metadata.actor_id != audit.actor_id
                    || event.metadata.causation_command_id != audit.command_id
                    || event.metadata.correlation_id != audit.correlation_id
                    || event.metadata.policy_version != audit.policy_version
                    || event.metadata.config_version != audit.config_version
            })
        {
            return Err(StoreError::InvalidAuthorizationAudit);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum CommitOutcome {
    Committed(CommittedResult),
    Duplicate(CommittedResult),
}

pub trait WorkflowStorePort: Send + Sync {
    /// Finds a previously committed result in the authorized actor scope.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] when the key conflicts or storage is unavailable.
    fn lookup_idempotency(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError>;

    /// Loads the committed event stream for one tenant-scoped instance.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] when committed data cannot be read safely.
    fn load(
        &self,
        tenant_id: &TenantId,
        instance_id: &InstanceId,
    ) -> Result<LoadedInstance, StoreError>;

    /// Atomically appends events and stores the idempotent command result.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] on version/idempotency conflict, invalid sequence,
    /// or storage failure. No partial event append may be visible on error.
    fn commit(&self, request: CommitRequest) -> Result<CommitOutcome, StoreError>;
}

impl<T: WorkflowStorePort + ?Sized> WorkflowStorePort for Arc<T> {
    fn lookup_idempotency(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError> {
        (**self).lookup_idempotency(tenant_id, actor_id, idempotency_key, command_id)
    }

    fn load(
        &self,
        tenant_id: &TenantId,
        instance_id: &InstanceId,
    ) -> Result<LoadedInstance, StoreError> {
        (**self).load(tenant_id, instance_id)
    }

    fn commit(&self, request: CommitRequest) -> Result<CommitOutcome, StoreError> {
        (**self).commit(request)
    }
}

impl<T: ConfigurationProviderPort + ?Sized> ConfigurationProviderPort for Arc<T> {
    fn resolve(&self, lookup: &ConfigurationLookup) -> Result<ResolvedConfigSnapshot, ConfigError> {
        (**self).resolve(lookup)
    }
}

impl<T: AuthorizationProviderPort + ?Sized> AuthorizationProviderPort for Arc<T> {
    fn authorize(
        &self,
        request: &AuthorizationRequest<'_>,
    ) -> Result<AuthorizedPrincipal, AuthorizationError> {
        (**self).authorize(request)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum StoreError {
    #[error("workflow stream version conflict: expected {expected}, actual {actual}")]
    VersionConflict { expected: u64, actual: u64 },
    #[error("idempotency key was already used by another command")]
    IdempotencyConflict,
    #[error("event sequence is not contiguous")]
    NonContiguousSequence,
    #[error("event id has already been committed in this tenant")]
    DuplicateEvent,
    #[error("snapshot scope or sequence is invalid for the commit")]
    InvalidSnapshot,
    #[error("authorization audit does not match the committed command")]
    InvalidAuthorizationAudit,
    #[error("governance transition is stale, malformed, or inconsistent: {0}")]
    InvalidGovernanceTransition(String),
    #[error("payload cryptography is unavailable or rejected the payload")]
    CryptoUnavailable,
    #[error("durable event data is corrupt or incompatible: {0}")]
    CorruptData(String),
    #[error("replay exceeds the configured in-memory event bound {configured_limit}")]
    ReplayLimitExceeded { configured_limit: usize },
    #[error("store operation failed: {0}")]
    Unavailable(String),
}
