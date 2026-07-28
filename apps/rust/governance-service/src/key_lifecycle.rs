use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use reqwest::Client;
use serde::Serialize;
use tracing::{error, info};

use crate::config::{KeyLifecycleConfig, WorkerConfig};
use crate::policy::{PolicyCache, PolicyScope, ResolvedPolicy};
use crate::store::GovernanceStore;

#[derive(Clone)]
pub struct KeyLifecycleWorker {
    store: GovernanceStore,
    policies: PolicyCache,
    client: Client,
    endpoints: KeyLifecycleConfig,
    worker: WorkerConfig,
}

#[derive(Serialize)]
struct BarrierRequest<'a> {
    tenant_id: &'a str,
    key_scope: &'a str,
    key_epoch: u64,
    committed_command_id: &'a str,
    timeout_ms: u64,
}

#[derive(Serialize)]
struct ShredRequest<'a> {
    tenant_id: &'a str,
    key_scope: &'a str,
    key_epoch: u64,
    committed_command_id: &'a str,
}

impl KeyLifecycleWorker {
    pub fn new(
        store: GovernanceStore,
        policies: PolicyCache,
        endpoints: KeyLifecycleConfig,
        worker: WorkerConfig,
    ) -> Self {
        Self {
            store,
            policies,
            client: Client::new(),
            endpoints,
            worker,
        }
    }

    pub async fn run(self) -> Result<()> {
        loop {
            if let Err(error) = self.run_once().await {
                error!(%error, "governance key lifecycle batch failed");
            }
            tokio::time::sleep(Duration::from_millis(self.worker.poll_interval_ms)).await;
        }
    }

    async fn run_once(&self) -> Result<()> {
        let jobs = self
            .store
            .claim_key_shreds(
                &self.worker.worker_id,
                self.worker.shred_batch_size,
                self.worker.shred_lease_ms,
            )
            .await?;
        for job in jobs {
            let spec = job.spec.as_ref().context("key shred job has no spec")?;
            let scope = PolicyScope {
                tenant_id: spec.tenant_id.clone(),
                workflow_type: spec.workflow_type.clone(),
                workflow_version: spec.workflow_version.clone(),
            };
            let policy = self.policies.get(&scope).await?;
            match self.execute(spec, &job.committed_command_id, &policy).await {
                Ok(()) => {
                    self.store
                        .complete_key_shred(
                            &spec.tenant_id,
                            &job.request_id,
                            &self.worker.worker_id,
                            now_epoch_ms()?,
                        )
                        .await?;
                    info!(
                        tenant_id = spec.tenant_id,
                        request_id = job.request_id,
                        "completed governance revocation barrier and key shred"
                    );
                }
                Err(error) => {
                    let retry = policy
                        .policy
                        .kms_retry
                        .as_ref()
                        .context("KMS retry policy is missing")?;
                    self.store
                        .retry_key_shred(
                            &spec.tenant_id,
                            &job.request_id,
                            &self.worker.worker_id,
                            retry.initial_backoff_ms,
                            &error.to_string(),
                            now_epoch_ms()?,
                        )
                        .await?;
                }
            }
        }
        Ok(())
    }

    async fn execute(
        &self,
        spec: &bpmp_contracts::governance::v1::AbortAndReconcileSpec,
        command_id: &str,
        policy: &ResolvedPolicy,
    ) -> Result<()> {
        let timeout = Duration::from_millis(policy.policy.kms_request_timeout_ms);
        self.client
            .post(&self.endpoints.revocation_barrier_endpoint)
            .timeout(timeout)
            .json(&BarrierRequest {
                tenant_id: &spec.tenant_id,
                key_scope: &spec.key_scope,
                key_epoch: spec.key_epoch,
                committed_command_id: command_id,
                timeout_ms: policy.policy.revocation_barrier_timeout_ms,
            })
            .send()
            .await?
            .error_for_status()
            .context("revocation barrier was not acknowledged")?;
        self.client
            .post(&self.endpoints.kms_endpoint)
            .timeout(timeout)
            .json(&ShredRequest {
                tenant_id: &spec.tenant_id,
                key_scope: &spec.key_scope,
                key_epoch: spec.key_epoch,
                committed_command_id: command_id,
            })
            .send()
            .await?
            .error_for_status()
            .context("KMS key shred was not acknowledged")?;
        Ok(())
    }
}

fn now_epoch_ms() -> Result<u64> {
    let value = SystemTime::now().duration_since(UNIX_EPOCH)?;
    u64::try_from(value.as_millis()).context("system time exceeds u64 milliseconds")
}
