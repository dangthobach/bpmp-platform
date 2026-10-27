use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use bpmp_adapter_kms::VaultTransitKeyManagement;
use bpmp_contracts::configuration::v1 as configurationv1;
use bpmp_domain_core::KeyScope;
use tracing::{error, info};

use crate::config::WorkerConfig;
use crate::engine::EngineCluster;
use crate::policy::{PolicyCache, PolicyScope, ResolvedPolicy};
use crate::store::GovernanceStore;

#[derive(Clone)]
pub struct KeyLifecycleWorker {
    store: GovernanceStore,
    policies: PolicyCache,
    worker: WorkerConfig,
    kms: Arc<VaultTransitKeyManagement>,
    engines: EngineCluster,
}

impl KeyLifecycleWorker {
    pub fn new(
        store: GovernanceStore,
        policies: PolicyCache,
        worker: WorkerConfig,
        kms: VaultTransitKeyManagement,
        engines: EngineCluster,
    ) -> Self {
        Self {
            store,
            policies,
            worker,
            kms: Arc::new(kms),
            engines,
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
            let result = self.engines.revoke_data_key_cache(spec, command_id).await;
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
        let key_scope = KeyScope::new(spec.key_scope.clone())?;
        for attempt in 1..=retry.max_attempts {
            let kms = self.kms.clone();
            let scope = key_scope.clone();
            let result = tokio::task::spawn_blocking(move || kms.shred(&scope))
                .await
                .context("KMS shred task panicked")?;
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
