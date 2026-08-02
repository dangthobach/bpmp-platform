use std::time::Duration;

use anyhow::{Context, Result};
use bpmp_contracts::governance::v1::engine_governance_service_client::EngineGovernanceServiceClient;
use bpmp_contracts::governance::v1::{
    CommitAbortAndReconcileRequest, CommitAbortAndReconcileResponse,
    PrepareAbortAndReconcileRequest, PrepareAbortAndReconcileResponse,
};
use bpmp_transport_observability::inject_tonic_metadata;
use tonic::Code;
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity};

use crate::config::{EngineEndpoint, TlsConfig};

#[derive(Clone)]
pub struct EngineCluster {
    clients: Vec<EngineGovernanceServiceClient<Channel>>,
}

impl EngineCluster {
    pub async fn connect(endpoints: &[EngineEndpoint], tls: &TlsConfig) -> Result<Self> {
        let certificate = tokio::fs::read(&tls.server_certificate).await?;
        let private_key = tokio::fs::read(&tls.server_private_key).await?;
        let ca = tokio::fs::read(&tls.client_ca).await?;
        let mut clients = Vec::with_capacity(endpoints.len());
        for configured in endpoints {
            let endpoint = Endpoint::from_shared(configured.endpoint.clone())?
                .connect_timeout(Duration::from_millis(configured.timeout_ms))
                .timeout(Duration::from_millis(configured.timeout_ms))
                .tls_config(
                    ClientTlsConfig::new()
                        .domain_name(configured.tls_domain.clone())
                        .ca_certificate(Certificate::from_pem(ca.clone()))
                        .identity(Identity::from_pem(certificate.clone(), private_key.clone())),
                )?;
            let mut channel = None;
            let mut last_error = None;
            for attempt in 1..=configured.connect_max_attempts {
                match endpoint.clone().connect().await {
                    Ok(value) => {
                        channel = Some(value);
                        break;
                    }
                    Err(error) => {
                        last_error = Some(error);
                        if attempt < configured.connect_max_attempts {
                            tokio::time::sleep(Duration::from_millis(configured.connect_retry_ms))
                                .await;
                        }
                    }
                }
            }
            let channel = channel.with_context(|| {
                format!(
                    "connect governance engine {} after {} attempts: {}",
                    configured.endpoint,
                    configured.connect_max_attempts,
                    last_error
                        .map_or_else(|| "unknown error".to_owned(), |error| error.to_string())
                )
            })?;
            clients.push(EngineGovernanceServiceClient::new(channel));
        }
        Ok(Self { clients })
    }

    pub async fn prepare(
        &self,
        request: PrepareAbortAndReconcileRequest,
    ) -> Result<PrepareAbortAndReconcileResponse> {
        let mut last = None;
        for client in &self.clients {
            let mut outbound = tonic::Request::new(request.clone());
            inject_tonic_metadata(&mut outbound);
            match client.clone().prepare_abort_and_reconcile(outbound).await {
                Ok(response) => return Ok(response.into_inner()),
                Err(status) if retryable(status.code()) => last = Some(status),
                Err(status) => return Err(anyhow::Error::new(status)),
            }
        }
        Err(anyhow::Error::new(
            last.context("no governance engine endpoint is configured")?,
        ))
    }

    pub async fn commit(
        &self,
        request: CommitAbortAndReconcileRequest,
    ) -> Result<CommitAbortAndReconcileResponse> {
        let mut last = None;
        for client in &self.clients {
            let mut outbound = tonic::Request::new(request.clone());
            inject_tonic_metadata(&mut outbound);
            match client.clone().commit_abort_and_reconcile(outbound).await {
                Ok(response) => return Ok(response.into_inner()),
                Err(status) if retryable(status.code()) => last = Some(status),
                Err(status) => return Err(anyhow::Error::new(status)),
            }
        }
        Err(anyhow::Error::new(
            last.context("no governance engine endpoint is configured")?,
        ))
    }
}

const fn retryable(code: Code) -> bool {
    matches!(
        code,
        Code::Unavailable | Code::DeadlineExceeded | Code::ResourceExhausted
    )
}
