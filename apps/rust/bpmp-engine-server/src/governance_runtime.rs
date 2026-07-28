use std::sync::Arc;

use bpmp_adapter_rocksdb::RocksDbWorkflowStore;
use bpmp_contracts::governance::v1::{
    AbortAndReconcileSpec as WireSpec, CommitAbortAndReconcileRequest,
    CommitAbortAndReconcileResponse, PrepareAbortAndReconcileRequest,
    PrepareAbortAndReconcileResponse, SignedApproval as WireApproval,
};
use bpmp_contracts::storage::v1::StoredCommandResult;
use bpmp_domain_core::{
    CommandId, CorrelationId, InstanceId, InstanceState, TenantId, WorkflowType, WorkflowVersion,
    evolve,
};
use bpmp_engine::{
    ConfigurationLookup, ConfigurationProviderPort, EngineGovernanceHandlerPort,
    GovernanceCommandContext, GovernanceTransportError, RuntimeRegistry, StoreError,
    WorkflowStorePort, prepare_abort_and_reconcile,
};
use bpmp_governance_domain::{
    AbortAndReconcileRequest, DualControlProof, SignedApproval, abort_request_digest,
    pending_ledger_digest,
};
use bpmp_payload_crypto::PayloadCryptoPort;
use bpmp_raft_state_machine::ApplyOutcome;
use prost::Message;

use crate::raft_runtime::RaftWorkflowStore;

pub struct AuthoritativeGovernanceHandler<C> {
    local: Arc<RocksDbWorkflowStore<C>>,
    raft: Arc<RaftWorkflowStore<C>>,
    registry: Arc<RuntimeRegistry>,
}

impl<C> AuthoritativeGovernanceHandler<C> {
    pub const fn new(
        local: Arc<RocksDbWorkflowStore<C>>,
        raft: Arc<RaftWorkflowStore<C>>,
        registry: Arc<RuntimeRegistry>,
    ) -> Self {
        Self {
            local,
            raft,
            registry,
        }
    }
}

struct PreparedScope {
    tenant_id: TenantId,
    instance_id: InstanceId,
    workflow_type: WorkflowType,
    workflow_version: WorkflowVersion,
    request: AbortAndReconcileRequest,
    pending_entries: Vec<bpmp_governance_domain::CompensationLedgerEntry>,
    expected_version: u64,
    state: InstanceState,
}

impl<C> AuthoritativeGovernanceHandler<C>
where
    C: PayloadCryptoPort + 'static,
{
    fn load_scope(&self, spec: &WireSpec) -> Result<PreparedScope, GovernanceTransportError> {
        let tenant_id = TenantId::new(spec.tenant_id.clone()).map_err(invalid("tenant_id"))?;
        let instance_id =
            InstanceId::new(spec.instance_id.clone()).map_err(invalid("instance_id"))?;
        let workflow_type =
            WorkflowType::new(spec.workflow_type.clone()).map_err(invalid("workflow_type"))?;
        let workflow_version = WorkflowVersion::new(spec.workflow_version.clone())
            .map_err(invalid("workflow_version"))?;
        if spec.policy_id.trim().is_empty()
            || spec.legal_deadline_epoch_ms == 0
            || spec.key_scope.trim().is_empty()
            || spec.key_epoch == 0
            || spec.reason_code.trim().is_empty()
        {
            return Err(GovernanceTransportError::InvalidField("spec"));
        }
        let policy = self
            .registry
            .governance_policy(&tenant_id, &workflow_type, &workflow_version)
            .map_err(|error| GovernanceTransportError::Unavailable(error.to_string()))?;
        let loaded = self
            .local
            .load(&tenant_id, &instance_id)
            .map_err(|error| store_error(&error))?;
        if loaded.snapshot.as_ref().is_some_and(|snapshot| {
            snapshot.workflow_type != workflow_type || snapshot.workflow_version != workflow_version
        }) || loaded.events.iter().any(|event| {
            event.metadata.workflow_type != workflow_type
                || event.metadata.workflow_version != workflow_version
        }) {
            return Err(GovernanceTransportError::Conflict(
                "workflow type or version does not match authoritative stream metadata".into(),
            ));
        }
        let mut state = loaded
            .snapshot
            .map_or_else(InstanceState::default, |snapshot| snapshot.state);
        for event in loaded.events {
            state = evolve(state, &event.event);
        }
        let max_entries = usize::try_from(policy.policy.max_pending_ledger_entries)
            .map_err(|_| GovernanceTransportError::Unavailable("ledger bound overflow".into()))?;
        let pending_entries = self
            .local
            .load_pending_compensation_entries(&tenant_id, &instance_id, max_entries)
            .map_err(|error| store_error(&error))?;
        let request = AbortAndReconcileRequest {
            tenant_id: spec.tenant_id.clone(),
            instance_id: spec.instance_id.clone(),
            policy_id: spec.policy_id.clone(),
            legal_deadline_epoch_ms: spec.legal_deadline_epoch_ms,
            key_scope: spec.key_scope.clone(),
            key_epoch: spec.key_epoch,
            pending_ledger_digest: pending_ledger_digest(&pending_entries),
            reason_code: spec.reason_code.clone(),
        };
        Ok(PreparedScope {
            tenant_id,
            instance_id,
            workflow_type,
            workflow_version,
            request,
            pending_entries,
            expected_version: loaded.version,
            state,
        })
    }
}

impl<C> EngineGovernanceHandlerPort for AuthoritativeGovernanceHandler<C>
where
    C: PayloadCryptoPort + 'static,
{
    fn prepare(
        &self,
        request: PrepareAbortAndReconcileRequest,
    ) -> Result<PrepareAbortAndReconcileResponse, GovernanceTransportError> {
        let spec = request
            .spec
            .as_ref()
            .ok_or(GovernanceTransportError::InvalidField("spec"))?;
        let prepared = self.load_scope(spec)?;
        let policy = self
            .registry
            .governance_policy(
                &prepared.tenant_id,
                &prepared.workflow_type,
                &prepared.workflow_version,
            )
            .map_err(|error| GovernanceTransportError::Unavailable(error.to_string()))?;
        Ok(PrepareAbortAndReconcileResponse {
            request_digest: abort_request_digest(&prepared.request).to_vec(),
            pending_ledger_digest: prepared.request.pending_ledger_digest.to_vec(),
            expected_version: prepared.expected_version,
            config_version: policy.config_version.to_string(),
            policy_version: policy.policy_version.to_string(),
        })
    }

    fn commit(
        &self,
        request: CommitAbortAndReconcileRequest,
    ) -> Result<CommitAbortAndReconcileResponse, GovernanceTransportError> {
        let spec = request
            .spec
            .as_ref()
            .ok_or(GovernanceTransportError::InvalidField("spec"))?;
        let prepared = self.load_scope(spec)?;
        let (proof, command_id) = validate_commit_binding(&request, &prepared)?;
        let policy = self
            .registry
            .governance_policy(
                &prepared.tenant_id,
                &prepared.workflow_type,
                &prepared.workflow_version,
            )
            .map_err(|error| GovernanceTransportError::Unavailable(error.to_string()))?;
        let configuration = self
            .registry
            .resolve(&ConfigurationLookup {
                tenant_id: prepared.tenant_id.clone(),
                workflow_type: prepared.workflow_type.clone(),
                workflow_version: prepared.workflow_version.clone(),
            })
            .map_err(|error| GovernanceTransportError::Unavailable(error.to_string()))?;
        let context = GovernanceCommandContext {
            tenant_id: prepared.tenant_id.clone(),
            instance_id: prepared.instance_id.clone(),
            command_id: command_id.clone(),
            correlation_id: CorrelationId::new(request.correlation_id.clone())
                .map_err(invalid("correlation_id"))?,
            workflow_type: prepared.workflow_type,
            workflow_version: prepared.workflow_version,
            config_version: policy.config_version,
            policy_version: policy.policy_version,
            operational_key_scope: configuration.engine.authorization_audit_key_scope,
            expected_version: prepared.expected_version,
            evaluated_at_epoch_ms: request.evaluated_at_epoch_ms,
            occurred_at_epoch_ms: request.occurred_at_epoch_ms,
        };
        let plan = prepare_abort_and_reconcile(
            &prepared.state,
            &prepared.request,
            &prepared.pending_entries,
            &proof,
            &policy.policy,
            &context,
        )
        .map_err(|error| GovernanceTransportError::Denied(error.to_string()))?;
        let digest = plan.decision.request_digest;
        let batch = self
            .local
            .prepare_governance_batch(
                &plan,
                idempotency_scope(
                    &prepared.tenant_id,
                    &proof.requester.actor_id,
                    &request.idempotency_key,
                ),
            )
            .map_err(|error| store_error(&error))?;
        let response = self
            .raft
            .propose_prepared(batch)
            .map_err(|error| store_error(&error))?;
        let duplicate = match response.outcome {
            ApplyOutcome::Applied => false,
            ApplyOutcome::Duplicate => true,
            ApplyOutcome::PreconditionFailed { .. } => {
                return Err(GovernanceTransportError::Conflict(
                    "authoritative state changed before governance commit".into(),
                ));
            }
            ApplyOutcome::Rejected { reason } => {
                return Err(GovernanceTransportError::Denied(reason));
            }
        };
        let stored = StoredCommandResult::decode(response.response_payload.as_slice())
            .map_err(|error| GovernanceTransportError::Unavailable(error.to_string()))?;
        Ok(CommitAbortAndReconcileResponse {
            command_id: stored.command_id,
            committed_sequence: stored.version,
            duplicate,
            request_digest: digest.to_vec(),
        })
    }
}

fn validate_commit_binding(
    request: &CommitAbortAndReconcileRequest,
    prepared: &PreparedScope,
) -> Result<(DualControlProof, CommandId), GovernanceTransportError> {
    if request.expected_version != prepared.expected_version
        || request.pending_ledger_digest.as_slice()
            != prepared.request.pending_ledger_digest.as_slice()
    {
        return Err(GovernanceTransportError::Conflict(
            "workflow version or pending ledger changed after approval preparation".into(),
        ));
    }
    if request.idempotency_key.trim().is_empty()
        || request.evaluated_at_epoch_ms == 0
        || request.occurred_at_epoch_ms == 0
    {
        return Err(GovernanceTransportError::InvalidField(
            "idempotency_key or timestamp",
        ));
    }
    let requester = request
        .requester
        .as_ref()
        .ok_or(GovernanceTransportError::InvalidField("requester"))
        .and_then(map_approval)?;
    let proof = DualControlProof {
        requester,
        approvers: request
            .approvers
            .iter()
            .map(map_approval)
            .collect::<Result<Vec<_>, _>>()?,
    };
    let command_id = CommandId::new(request.command_id.clone()).map_err(invalid("command_id"))?;
    Ok((proof, command_id))
}

fn map_approval(value: &WireApproval) -> Result<SignedApproval, GovernanceTransportError> {
    Ok(SignedApproval {
        request_digest: exact_digest(&value.request_digest, "approval.request_digest")?,
        tenant_id: value.tenant_id.clone(),
        actor_id: value.actor_id.clone(),
        capability: value.capability.clone(),
        auth_assurance: value.auth_assurance.clone(),
        approved_at_epoch_ms: value.approved_at_epoch_ms,
        expires_at_epoch_ms: value.expires_at_epoch_ms,
        key_id: value.key_id.clone(),
        signature: value.signature.clone(),
    })
}

fn exact_digest(bytes: &[u8], field: &'static str) -> Result<[u8; 32], GovernanceTransportError> {
    bytes
        .try_into()
        .map_err(|_| GovernanceTransportError::InvalidField(field))
}

fn invalid<E>(field: &'static str) -> impl FnOnce(E) -> GovernanceTransportError {
    move |_| GovernanceTransportError::InvalidField(field)
}

fn store_error(error: &StoreError) -> GovernanceTransportError {
    match error {
        StoreError::VersionConflict { .. }
        | StoreError::IdempotencyConflict
        | StoreError::InvalidGovernanceTransition(_) => {
            GovernanceTransportError::Conflict(error.to_string())
        }
        StoreError::NotLeader { .. } | StoreError::Unavailable(_) => {
            GovernanceTransportError::Unavailable(error.to_string())
        }
        _ => GovernanceTransportError::Denied(error.to_string()),
    }
}

fn idempotency_scope(tenant: &TenantId, actor_id: &str, key: &str) -> Vec<u8> {
    let mut value = Vec::new();
    for component in [tenant.as_str(), actor_id, key] {
        let bytes = component.as_bytes();
        value.extend_from_slice(&(bytes.len() as u64).to_be_bytes());
        value.extend_from_slice(bytes);
    }
    value
}
