use std::collections::BTreeMap;
use std::sync::Arc;

use bpmp_domain_core::{
    ConfigVersion, CorrelationId, InstanceId, NodeId, PolicyVersion, TenantId, WorkflowType,
    WorkflowValue, WorkflowVersion,
};
use thiserror::Error;

use crate::{LocalTaskActivation, LocalTaskKind};

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RemoteTask {
    pub task_id: String,
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub node_id: NodeId,
    pub task_type: String,
    pub activation_event_id: String,
    pub activation_sequence: u64,
    pub activated_at_epoch_ms: u64,
    pub correlation_id: CorrelationId,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
}

impl TryFrom<&LocalTaskActivation> for RemoteTask {
    type Error = RemoteTaskError;

    fn try_from(value: &LocalTaskActivation) -> Result<Self, Self::Error> {
        if value.kind != LocalTaskKind::Service {
            return Err(RemoteTaskError::UnsupportedTaskKind);
        }
        Ok(Self {
            task_id: value.event_id.clone(),
            tenant_id: value.tenant_id.clone(),
            instance_id: InstanceId::new(value.instance_id.clone())
                .map_err(|_| RemoteTaskError::InvalidActivation)?,
            workflow_type: value.workflow_type.clone(),
            workflow_version: value.workflow_version.clone(),
            node_id: value.node_id.clone(),
            task_type: value.task_type.clone(),
            activation_event_id: value.event_id.clone(),
            activation_sequence: value.event_sequence,
            activated_at_epoch_ms: value.occurred_at_epoch_ms,
            correlation_id: value.correlation_id.clone(),
            config_version: value.config_version.clone(),
            policy_version: value.policy_version.clone(),
        })
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RemoteTaskClaim {
    pub task: RemoteTask,
    pub assignment_id: String,
    pub worker_id: String,
    pub session_id: String,
    pub attempt: u32,
    pub lease_version: u64,
    pub lease_until_epoch_ms: u64,
    pub assignment_token_digest: Vec<u8>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RemoteTaskClaimRequest {
    pub tenant_id: TenantId,
    pub task_id: String,
    pub assignment_id: String,
    pub worker_id: String,
    pub session_id: String,
    pub now_epoch_ms: u64,
    pub lease_until_epoch_ms: u64,
    pub assignment_token_digest: Vec<u8>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum RemoteTaskEnqueueOutcome {
    Enqueued,
    Duplicate,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum RemoteTaskFailureOutcome {
    RetryScheduled,
    DeadLettered,
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteTaskIngressPort: Send + Sync {
    fn enqueue_remote_task(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<RemoteTaskEnqueueOutcome, RemoteTaskError>;
}

impl<T: RemoteTaskIngressPort + ?Sized> RemoteTaskIngressPort for Arc<T> {
    fn enqueue_remote_task(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<RemoteTaskEnqueueOutcome, RemoteTaskError> {
        (**self).enqueue_remote_task(activation)
    }
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteTaskStorePort: RemoteTaskIngressPort {
    fn ready_tasks(
        &self,
        now_epoch_ms: u64,
        limit: usize,
    ) -> Result<Vec<RemoteTask>, RemoteTaskError>;

    fn claim_remote_task(
        &self,
        request: &RemoteTaskClaimRequest,
    ) -> Result<Option<RemoteTaskClaim>, RemoteTaskError>;

    fn complete_remote_task(&self, claim: &RemoteTaskClaim) -> Result<(), RemoteTaskError>;

    fn fail_remote_task(
        &self,
        claim: &RemoteTaskClaim,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<RemoteTaskFailureOutcome, RemoteTaskError>;

    fn expired_claims(
        &self,
        now_epoch_ms: u64,
        limit: usize,
    ) -> Result<Vec<RemoteTaskClaim>, RemoteTaskError>;
}

impl<T: RemoteTaskStorePort + ?Sized> RemoteTaskStorePort for Arc<T> {
    fn ready_tasks(
        &self,
        now_epoch_ms: u64,
        limit: usize,
    ) -> Result<Vec<RemoteTask>, RemoteTaskError> {
        (**self).ready_tasks(now_epoch_ms, limit)
    }

    fn claim_remote_task(
        &self,
        request: &RemoteTaskClaimRequest,
    ) -> Result<Option<RemoteTaskClaim>, RemoteTaskError> {
        (**self).claim_remote_task(request)
    }

    fn complete_remote_task(&self, claim: &RemoteTaskClaim) -> Result<(), RemoteTaskError> {
        (**self).complete_remote_task(claim)
    }

    fn fail_remote_task(
        &self,
        claim: &RemoteTaskClaim,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<RemoteTaskFailureOutcome, RemoteTaskError> {
        (**self).fail_remote_task(claim, retry_at_epoch_ms, dead_letter)
    }

    fn expired_claims(
        &self,
        now_epoch_ms: u64,
        limit: usize,
    ) -> Result<Vec<RemoteTaskClaim>, RemoteTaskError> {
        (**self).expired_claims(now_epoch_ms, limit)
    }
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteTaskCompletionPort: Send + Sync {
    fn complete(
        &self,
        claim: &RemoteTaskClaim,
        outputs: BTreeMap<String, WorkflowValue>,
        occurred_at_epoch_ms: u64,
    ) -> Result<(), RemoteTaskError>;
}

impl<T: RemoteTaskCompletionPort + ?Sized> RemoteTaskCompletionPort for Arc<T> {
    fn complete(
        &self,
        claim: &RemoteTaskClaim,
        outputs: BTreeMap<String, WorkflowValue>,
        occurred_at_epoch_ms: u64,
    ) -> Result<(), RemoteTaskError> {
        (**self).complete(claim, outputs, occurred_at_epoch_ms)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum RemoteTaskError {
    #[error("only unbound service tasks may be dispatched remotely")]
    UnsupportedTaskKind,
    #[error("remote task activation is invalid")]
    InvalidActivation,
    #[error("remote task operation has an invalid bound or identity")]
    InvalidRequest,
    #[error("remote task lease is stale or owned by another session")]
    StaleLease,
    #[error("remote task state changed concurrently")]
    Conflict,
    #[error("remote task was not found")]
    NotFound,
    #[error("remote task operation must be forwarded to Raft leader {leader_id:?}")]
    NotLeader {
        leader_id: Option<u64>,
        leader_address: Option<String>,
    },
    #[error("remote task storage is unavailable: {0}")]
    Store(String),
    #[error("remote task completion command failed: {0}")]
    Completion(String),
}
