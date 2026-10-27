use std::sync::Arc;

use bpmp_contracts::governance::v1::engine_governance_service_server::{
    EngineGovernanceService, EngineGovernanceServiceServer,
};
use bpmp_contracts::governance::v1::{
    CommitAbortAndReconcileRequest, CommitAbortAndReconcileResponse,
    PrepareAbortAndReconcileRequest, PrepareAbortAndReconcileResponse, RevokeDataKeyCacheRequest,
    RevokeDataKeyCacheResponse,
};
use thiserror::Error;
use tonic::{Request, Response, Status};

use bpmp_transport_observability::WorkloadAuthorization;

pub trait EngineGovernanceHandlerPort: Send + Sync + 'static {
    /// Returns a digest bound to the current authoritative workflow and ledger.
    ///
    /// # Errors
    ///
    /// Fails closed for invalid scope, missing policy, corrupt state, or an
    /// unavailable authoritative store.
    fn prepare(
        &self,
        request: PrepareAbortAndReconcileRequest,
    ) -> Result<PrepareAbortAndReconcileResponse, GovernanceTransportError>;

    /// Revalidates proof and current state before proposing one atomic Raft batch.
    ///
    /// # Errors
    ///
    /// No transition is committed when scope, version, policy, proof, ledger,
    /// cryptography, or quorum validation fails.
    fn commit(
        &self,
        request: CommitAbortAndReconcileRequest,
    ) -> Result<CommitAbortAndReconcileResponse, GovernanceTransportError>;

    /// Installs the local cache revocation fence after the compliance terminal
    /// transition commits and before governance destroys the wrapped DEK.
    ///
    /// # Errors
    /// Fails closed for invalid scope or when the local fence cannot be installed.
    fn revoke_data_key_cache(
        &self,
        request: RevokeDataKeyCacheRequest,
    ) -> Result<RevokeDataKeyCacheResponse, GovernanceTransportError>;
}

#[derive(Clone)]
pub struct GrpcEngineGovernanceService<H> {
    handler: Arc<H>,
    revocation_authorization: Option<WorkloadAuthorization>,
}

impl<H> GrpcEngineGovernanceService<H> {
    pub fn new(handler: H) -> Self {
        Self {
            handler: Arc::new(handler),
            revocation_authorization: None,
        }
    }

    #[must_use]
    pub fn with_revocation_authorization(mut self, authorization: WorkloadAuthorization) -> Self {
        self.revocation_authorization = Some(authorization);
        self
    }

    pub fn into_server(
        self,
        max_decoding_bytes: usize,
        max_encoding_bytes: usize,
    ) -> EngineGovernanceServiceServer<Self>
    where
        H: EngineGovernanceHandlerPort,
    {
        EngineGovernanceServiceServer::new(self)
            .max_decoding_message_size(max_decoding_bytes)
            .max_encoding_message_size(max_encoding_bytes)
    }
}

#[tonic::async_trait]
impl<H> EngineGovernanceService for GrpcEngineGovernanceService<H>
where
    H: EngineGovernanceHandlerPort,
{
    async fn prepare_abort_and_reconcile(
        &self,
        request: Request<PrepareAbortAndReconcileRequest>,
    ) -> Result<Response<PrepareAbortAndReconcileResponse>, Status> {
        let handler = Arc::clone(&self.handler);
        tokio::task::spawn_blocking(move || handler.prepare(request.into_inner()))
            .await
            .map_err(|error| Status::internal(format!("governance worker failed: {error}")))?
            .map(Response::new)
            .map_err(Status::from)
    }

    async fn commit_abort_and_reconcile(
        &self,
        request: Request<CommitAbortAndReconcileRequest>,
    ) -> Result<Response<CommitAbortAndReconcileResponse>, Status> {
        let handler = Arc::clone(&self.handler);
        tokio::task::spawn_blocking(move || handler.commit(request.into_inner()))
            .await
            .map_err(|error| Status::internal(format!("governance worker failed: {error}")))?
            .map(Response::new)
            .map_err(Status::from)
    }

    async fn revoke_data_key_cache(
        &self,
        request: Request<RevokeDataKeyCacheRequest>,
    ) -> Result<Response<RevokeDataKeyCacheResponse>, Status> {
        if !self
            .revocation_authorization
            .as_ref()
            .is_some_and(|authorization| {
                authorization.is_tonic_request_authorized(
                    &request,
                    "/bpmp.governance.v1.EngineGovernanceService/RevokeDataKeyCache",
                )
            })
        {
            return Err(Status::permission_denied(
                "governance workload identity is not authorized for key revocation",
            ));
        }
        let handler = Arc::clone(&self.handler);
        tokio::task::spawn_blocking(move || handler.revoke_data_key_cache(request.into_inner()))
            .await
            .map_err(|error| Status::internal(format!("governance worker failed: {error}")))?
            .map(Response::new)
            .map_err(Status::from)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum GovernanceTransportError {
    #[error("governance request field is invalid: {0}")]
    InvalidField(&'static str),
    #[error("governance request is stale: {0}")]
    Conflict(String),
    #[error("governance request is denied: {0}")]
    Denied(String),
    #[error("authoritative governance service is unavailable: {0}")]
    Unavailable(String),
}

impl From<GovernanceTransportError> for Status {
    fn from(error: GovernanceTransportError) -> Self {
        match error {
            GovernanceTransportError::InvalidField(message) => Self::invalid_argument(message),
            GovernanceTransportError::Conflict(message) => Self::aborted(message),
            GovernanceTransportError::Denied(message) => Self::permission_denied(message),
            GovernanceTransportError::Unavailable(message) => Self::unavailable(message),
        }
    }
}
