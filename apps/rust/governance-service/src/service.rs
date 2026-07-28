use bpmp_contracts::governance::v1::governance_approval_service_server::{
    GovernanceApprovalService, GovernanceApprovalServiceServer,
};
use bpmp_contracts::governance::v1::{
    ApprovalRequestStatus, CreateApprovalRequest, CreateApprovalResponse, GetApprovalRequest,
    GetApprovalResponse, PrepareAbortAndReconcileRequest, RecordApprovalRequest,
    RecordApprovalResponse, SubmitApprovalRequest, SubmitApprovalResponse,
};
use tonic::{Request, Response, Status};

use crate::engine::EngineCluster;
use crate::policy::{PolicyCache, PolicyScope};
use crate::store::GovernanceStore;

#[derive(Clone)]
pub struct GovernanceGrpcService {
    store: GovernanceStore,
    engines: EngineCluster,
    policies: PolicyCache,
}

impl GovernanceGrpcService {
    pub const fn new(
        store: GovernanceStore,
        engines: EngineCluster,
        policies: PolicyCache,
    ) -> Self {
        Self {
            store,
            engines,
            policies,
        }
    }

    pub fn into_server(
        self,
        max_decoding_bytes: usize,
        max_encoding_bytes: usize,
    ) -> GovernanceApprovalServiceServer<Self> {
        GovernanceApprovalServiceServer::new(self)
            .max_decoding_message_size(max_decoding_bytes)
            .max_encoding_message_size(max_encoding_bytes)
    }
}

#[tonic::async_trait]
impl GovernanceApprovalService for GovernanceGrpcService {
    async fn create_approval(
        &self,
        request: Request<CreateApprovalRequest>,
    ) -> Result<Response<CreateApprovalResponse>, Status> {
        let request = request.into_inner();
        let spec = request
            .spec
            .as_ref()
            .ok_or_else(|| Status::invalid_argument("spec is required"))?;
        validate_identity(&request.request_id, "request_id")?;
        validate_identity(&request.idempotency_key, "idempotency_key")?;
        validate_spec(spec)?;
        if request.created_at_epoch_ms == 0 {
            return Err(Status::invalid_argument(
                "created_at_epoch_ms must be positive",
            ));
        }
        let scope = scope(spec);
        let policy = self
            .policies
            .get(&scope)
            .await
            .map_err(|error| unavailable(&error))?;
        let prepared = self
            .engines
            .prepare(PrepareAbortAndReconcileRequest {
                spec: Some(spec.clone()),
            })
            .await
            .map_err(|error| engine_status(&error))?;
        if prepared.config_version != policy.config_version
            || prepared.policy_version != policy.policy_version
        {
            return Err(Status::aborted(
                "governance policy changed while preparing approval",
            ));
        }
        let approval = self
            .store
            .create(
                &request.request_id,
                &request.idempotency_key,
                spec,
                &prepared,
                request.created_at_epoch_ms,
            )
            .await
            .map_err(|error| store_status(&error))?;
        Ok(Response::new(CreateApprovalResponse {
            approval: Some(approval),
        }))
    }

    async fn record_approval(
        &self,
        request: Request<RecordApprovalRequest>,
    ) -> Result<Response<RecordApprovalResponse>, Status> {
        let request = request.into_inner();
        validate_identity(&request.tenant_id, "tenant_id")?;
        validate_identity(&request.request_id, "request_id")?;
        let approval = request
            .approval
            .as_ref()
            .ok_or_else(|| Status::invalid_argument("approval is required"))?;
        let role = bpmp_contracts::governance::v1::ApprovalRole::try_from(request.role)
            .map_err(|_| Status::invalid_argument("approval role is invalid"))?;
        let current = self
            .store
            .get(&request.tenant_id, &request.request_id)
            .await
            .map_err(|error| store_status(&error))?;
        let spec = current
            .spec
            .as_ref()
            .ok_or_else(|| Status::internal("stored approval spec is missing"))?;
        let policy = self
            .policies
            .get(&scope(spec))
            .await
            .map_err(|error| unavailable(&error))?;
        let updated = self
            .store
            .record_approval(
                &request.tenant_id,
                &request.request_id,
                role,
                approval,
                policy.policy.required_approver_count,
                approval.approved_at_epoch_ms,
            )
            .await
            .map_err(|error| store_status(&error))?;
        Ok(Response::new(RecordApprovalResponse {
            approval: Some(updated),
        }))
    }

    async fn submit_approval(
        &self,
        request: Request<SubmitApprovalRequest>,
    ) -> Result<Response<SubmitApprovalResponse>, Status> {
        let request = request.into_inner();
        for (value, field) in [
            (&request.tenant_id, "tenant_id"),
            (&request.request_id, "request_id"),
            (&request.command_id, "command_id"),
            (&request.idempotency_key, "idempotency_key"),
            (&request.correlation_id, "correlation_id"),
        ] {
            validate_identity(value, field)?;
        }
        if request.submitted_at_epoch_ms == 0 {
            return Err(Status::invalid_argument(
                "submitted_at_epoch_ms must be positive",
            ));
        }
        let current = self
            .store
            .get(&request.tenant_id, &request.request_id)
            .await
            .map_err(|error| store_status(&error))?;
        if ApprovalRequestStatus::try_from(current.status).ok()
            != Some(ApprovalRequestStatus::Approved)
            || current.requester_approvals.len() != 1
        {
            return Err(Status::failed_precondition(
                "approval request has not reached dual control",
            ));
        }
        let spec = current
            .spec
            .clone()
            .ok_or_else(|| Status::internal("stored approval spec is missing"))?;
        let policy = self
            .policies
            .get(&scope(&spec))
            .await
            .map_err(|error| unavailable(&error))?;
        if current.approver_approvals.len()
            < usize::try_from(policy.policy.required_approver_count)
                .map_err(|_| Status::internal("approver count overflow"))?
            || current.config_version != policy.config_version
            || current.policy_version != policy.policy_version
        {
            return Err(Status::aborted(
                "governance policy changed or approvals are insufficient",
            ));
        }
        let committed = self
            .engines
            .commit(
                bpmp_contracts::governance::v1::CommitAbortAndReconcileRequest {
                    spec: Some(spec),
                    pending_ledger_digest: current.pending_ledger_digest.clone(),
                    expected_version: current.expected_version,
                    command_id: request.command_id.clone(),
                    idempotency_key: request.idempotency_key,
                    correlation_id: request.correlation_id,
                    evaluated_at_epoch_ms: request.submitted_at_epoch_ms,
                    occurred_at_epoch_ms: request.submitted_at_epoch_ms,
                    requester: current.requester_approvals.first().cloned(),
                    approvers: current.approver_approvals,
                },
            )
            .await
            .map_err(|error| engine_status(&error))?;
        if committed.request_digest != current.request_digest {
            return Err(Status::data_loss(
                "engine committed a different governance request digest",
            ));
        }
        let updated = self
            .store
            .mark_committed(
                &request.tenant_id,
                &request.request_id,
                current.version,
                &committed.command_id,
                committed.committed_sequence,
                request.submitted_at_epoch_ms,
            )
            .await
            .map_err(|error| store_status(&error))?;
        Ok(Response::new(SubmitApprovalResponse {
            approval: Some(updated),
        }))
    }

    async fn get_approval(
        &self,
        request: Request<GetApprovalRequest>,
    ) -> Result<Response<GetApprovalResponse>, Status> {
        let request = request.into_inner();
        validate_identity(&request.tenant_id, "tenant_id")?;
        validate_identity(&request.request_id, "request_id")?;
        let approval = self
            .store
            .get(&request.tenant_id, &request.request_id)
            .await
            .map_err(|error| store_status(&error))?;
        Ok(Response::new(GetApprovalResponse {
            approval: Some(approval),
        }))
    }
}

fn scope(spec: &bpmp_contracts::governance::v1::AbortAndReconcileSpec) -> PolicyScope {
    PolicyScope {
        tenant_id: spec.tenant_id.clone(),
        workflow_type: spec.workflow_type.clone(),
        workflow_version: spec.workflow_version.clone(),
    }
}

fn validate_spec(
    spec: &bpmp_contracts::governance::v1::AbortAndReconcileSpec,
) -> Result<(), Status> {
    for (value, field) in [
        (&spec.tenant_id, "tenant_id"),
        (&spec.instance_id, "instance_id"),
        (&spec.workflow_type, "workflow_type"),
        (&spec.workflow_version, "workflow_version"),
        (&spec.policy_id, "policy_id"),
        (&spec.key_scope, "key_scope"),
        (&spec.reason_code, "reason_code"),
    ] {
        validate_identity(value, field)?;
    }
    if spec.legal_deadline_epoch_ms == 0 || spec.key_epoch == 0 {
        return Err(Status::invalid_argument(
            "legal deadline and key epoch must be positive",
        ));
    }
    Ok(())
}

fn validate_identity(value: &str, field: &'static str) -> Result<(), Status> {
    if value.trim().is_empty() || value.len() > 512 {
        Err(Status::invalid_argument(field))
    } else {
        Ok(())
    }
}

fn unavailable(error: &anyhow::Error) -> Status {
    Status::unavailable(error.to_string())
}

fn engine_status(error: &anyhow::Error) -> Status {
    if let Some(status) = error.downcast_ref::<Status>() {
        status.clone()
    } else {
        Status::unavailable(error.to_string())
    }
}

fn store_status(error: &anyhow::Error) -> Status {
    let message = error.to_string();
    if message.contains("does not exist") {
        Status::not_found(message)
    } else if message.contains("changed") || message.contains("does not match") {
        Status::aborted(message)
    } else {
        Status::failed_precondition(message)
    }
}
