use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use bpmp_contracts::configuration::v1 as configurationv1;
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
    key_cache_ttl_ms: u64,
}

#[derive(Serialize)]
struct ShredRequest<'a> {
    tenant_id: &'a str,
    key_scope: &'a str,
    key_epoch: u64,
    committed_command_id: &'a str,
    reconciliation_batch_size: u32,
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
                    let retry_delay_ms = retry_delay(retry, job.shred_attempts);
                    self.store
                        .retry_key_shred(
                            &spec.tenant_id,
                            &job.request_id,
                            &self.worker.worker_id,
                            retry_delay_ms,
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
        let retry = policy
            .policy
            .kms_retry
            .as_ref()
            .context("KMS retry policy is missing")?;
        let mut delay_ms = retry.initial_backoff_ms;
        for attempt in 1..=retry.max_attempts {
            let result = self
                .client
                .post(&self.endpoints.revocation_barrier_endpoint)
                .timeout(Duration::from_millis(
                    policy.policy.revocation_barrier_timeout_ms,
                ))
                .json(&BarrierRequest {
                    tenant_id: &spec.tenant_id,
                    key_scope: &spec.key_scope,
                    key_epoch: spec.key_epoch,
                    committed_command_id: command_id,
                    timeout_ms: policy.policy.revocation_barrier_timeout_ms,
                    key_cache_ttl_ms: policy.policy.key_cache_ttl_ms,
                })
                .send()
                .await
                .and_then(reqwest::Response::error_for_status);
            match result {
                Ok(_) => break,
                Err(error) if attempt == retry.max_attempts => {
                    return Err(error).context("revocation barrier was not acknowledged");
                }
                Err(_) => {
                    tokio::time::sleep(Duration::from_millis(delay_ms)).await;
                    delay_ms = next_delay(delay_ms, retry);
                }
            }
        }
        delay_ms = retry.initial_backoff_ms;
        for attempt in 1..=retry.max_attempts {
            let result = self
                .client
                .post(&self.endpoints.kms_endpoint)
                .timeout(Duration::from_millis(policy.policy.kms_request_timeout_ms))
                .json(&ShredRequest {
                    tenant_id: &spec.tenant_id,
                    key_scope: &spec.key_scope,
                    key_epoch: spec.key_epoch,
                    committed_command_id: command_id,
                    reconciliation_batch_size: policy.policy.reconciliation_batch_size,
                })
                .send()
                .await
                .and_then(reqwest::Response::error_for_status);
            match result {
                Ok(_) => break,
                Err(error) if attempt == retry.max_attempts => {
                    return Err(error).context("KMS key shred was not acknowledged");
                }
                Err(_) => {
                    tokio::time::sleep(Duration::from_millis(delay_ms)).await;
                    delay_ms = next_delay(delay_ms, retry);
                }
            }
        }
        Ok(())
    }
}

fn retry_delay(retry: &configurationv1::RetryPolicy, attempts: u32) -> u64 {
    let mut delay = retry.initial_backoff_ms;
    for _ in 1..attempts {
        delay = next_delay(delay, retry);
    }
    delay
}

fn next_delay(current: u64, retry: &configurationv1::RetryPolicy) -> u64 {
    current
        .saturating_mul(u64::from(retry.multiplier_millis))
        .saturating_div(1_000)
        .min(retry.max_backoff_ms)
}

fn now_epoch_ms() -> Result<u64> {
    let value = SystemTime::now().duration_since(UNIX_EPOCH)?;
    u64::try_from(value.as_millis()).context("system time exceeds u64 milliseconds")
}
