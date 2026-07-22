#![cfg_attr(not(target_os = "linux"), allow(dead_code))]

use std::fs;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use serde::Deserialize;
use thiserror::Error;

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeConfig {
    pub listen_addr: SocketAddr,
    pub data_path: PathBuf,
    pub tls: TlsConfig,
    pub wir: WirConfig,
    pub authorization: AuthorizationConfig,
    pub payload_keys: Vec<PayloadKeyConfig>,
    pub rocksdb: RocksDbRuntimeConfig,
    pub grpc: GrpcConfig,
    pub workers: WorkerConfig,
    pub kafka: KafkaConfig,
    pub wasm_modules: Vec<WasmModuleConfig>,
}

impl RuntimeConfig {
    pub fn load(path: &Path) -> Result<Self, RuntimeConfigError> {
        let bytes = fs::read(path).map_err(RuntimeConfigError::Read)?;
        let config: Self = serde_json::from_slice(&bytes).map_err(RuntimeConfigError::Decode)?;
        config.validate()?;
        Ok(config)
    }

    fn validate(&self) -> Result<(), RuntimeConfigError> {
        if self.wir.artifacts.is_empty()
            || self.wir.configurations.is_empty()
            || self.authorization.policy_bundles.is_empty()
            || self.authorization.actor_keys.is_empty()
            || self.authorization.workload_keys.is_empty()
            || self.authorization.policy_keys.is_empty()
            || self.payload_keys.is_empty()
            || self.kafka.brokers.is_empty()
            || self.kafka.topic.trim().is_empty()
            || self
                .authorization
                .internal_dispatch
                .actor_id
                .trim()
                .is_empty()
            || self
                .authorization
                .internal_dispatch
                .workload_id
                .trim()
                .is_empty()
            || self
                .authorization
                .internal_dispatch
                .actor_signing_key_id
                .trim()
                .is_empty()
            || self
                .authorization
                .internal_dispatch
                .workload_signing_key_id
                .trim()
                .is_empty()
            || self.authorization.internal_dispatch.proof_ttl_ms == 0
            || self.workers.boundary.worker_id.trim().is_empty()
            || self.tls.server_certificate.as_os_str().is_empty()
            || self.tls.server_private_key.as_os_str().is_empty()
            || self.tls.client_ca.as_os_str().is_empty()
        {
            return Err(RuntimeConfigError::Invalid(
                "artifact, key, policy, and Kafka collections must not be empty",
            ));
        }
        for value in [
            self.workers.boundary.projection_batch_size,
            self.workers.boundary.dispatch_batch_size,
            self.workers.boundary.max_dispatch_attempts,
            self.workers.boundary.max_expression_bytes,
            self.workers.boundary.max_signal_id_bytes,
            self.workers.boundary.max_reference_bytes,
            self.workers.boundary.max_subscriptions_per_instance,
        ] {
            if value == 0 {
                return Err(RuntimeConfigError::Invalid(
                    "boundary worker bounds must be positive",
                ));
            }
        }
        for value in [
            self.grpc.max_decoding_bytes,
            self.grpc.max_encoding_bytes,
            self.workers.outbox_batch_size,
            self.workers.local_task_batch_size,
        ] {
            if value == 0 {
                return Err(RuntimeConfigError::Invalid(
                    "configured bounds must be positive",
                ));
            }
        }
        for value in [
            self.workers.poll_interval_ms,
            self.workers.outbox_initial_retry_ms,
            self.workers.outbox_max_retry_ms,
        ] {
            if value == 0 {
                return Err(RuntimeConfigError::Invalid(
                    "configured durations must be positive",
                ));
            }
        }
        if self.workers.outbox_max_retry_ms < self.workers.outbox_initial_retry_ms
            || self.workers.outbox_retry_multiplier_millis < 1_000
            || self.workers.outbox_max_attempts == 0
        {
            return Err(RuntimeConfigError::Invalid(
                "outbox retry policy is invalid",
            ));
        }
        validate_wasm_modules(&self.wasm_modules)?;
        Ok(())
    }

    pub const fn poll_interval(&self) -> Duration {
        Duration::from_millis(self.workers.poll_interval_ms)
    }
}

fn validate_wasm_modules(modules: &[WasmModuleConfig]) -> Result<(), RuntimeConfigError> {
    if modules.iter().any(|module| {
        module.implementation_ref.trim().is_empty()
            || module.implementation_version.trim().is_empty()
            || module.path.as_os_str().is_empty()
            || !is_sha256_version(&module.implementation_version)
            || module
                .service_task_types
                .iter()
                .any(|task_type| task_type.trim().is_empty())
    }) {
        return Err(RuntimeConfigError::Invalid(
            "WASM module registry entry is invalid",
        ));
    }
    Ok(())
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TlsConfig {
    pub server_certificate: PathBuf,
    pub server_private_key: PathBuf,
    pub client_ca: PathBuf,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WirConfig {
    pub verification_key: PathBuf,
    pub artifacts: Vec<PathBuf>,
    pub configurations: Vec<PathBuf>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorizationConfig {
    pub actor_keys: Vec<VerificationKeyConfig>,
    pub workload_keys: Vec<VerificationKeyConfig>,
    pub policy_keys: Vec<VerificationKeyConfig>,
    pub policy_bundles: Vec<PathBuf>,
    pub jwks: PathBuf,
    pub jwt_issuers: Vec<String>,
    pub jwt_audiences: Vec<String>,
    pub jwt_algorithms: Vec<String>,
    pub max_proof_bytes: usize,
    pub max_roles: usize,
    pub max_capabilities: usize,
    pub max_policy_bytes: usize,
    pub max_policy_grants: usize,
    pub max_jwks_keys: usize,
    pub clock_skew_seconds: u64,
    pub internal_dispatch: InternalDispatchConfig,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InternalDispatchConfig {
    pub actor_signing_key: PathBuf,
    pub actor_signing_key_id: String,
    pub workload_signing_key: PathBuf,
    pub workload_signing_key_id: String,
    pub actor_id: String,
    pub workload_id: String,
    pub roles: Vec<String>,
    pub capabilities: Vec<String>,
    pub proof_ttl_ms: u64,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerificationKeyConfig {
    pub key_id: String,
    pub path: PathBuf,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PayloadKeyConfig {
    pub key_scope: String,
    pub key_version: String,
    pub key_epoch: u64,
    pub path: PathBuf,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RocksDbRuntimeConfig {
    pub max_open_files: i32,
    pub write_buffer_size_bytes: usize,
    pub max_background_jobs: i32,
    pub max_replay_events: usize,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GrpcConfig {
    pub max_decoding_bytes: usize,
    pub max_encoding_bytes: usize,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WorkerConfig {
    pub poll_interval_ms: u64,
    pub outbox_batch_size: usize,
    pub outbox_max_attempts: u32,
    pub outbox_initial_retry_ms: u64,
    pub outbox_max_retry_ms: u64,
    pub outbox_retry_multiplier_millis: u32,
    pub boundary: BoundaryWorkerConfig,
    pub local_task_batch_size: usize,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WasmModuleConfig {
    pub implementation_ref: String,
    pub implementation_version: String,
    pub path: PathBuf,
    #[serde(default)]
    pub service_task_types: Vec<String>,
}

fn is_sha256_version(value: &str) -> bool {
    value.strip_prefix("sha256:").is_some_and(|digest| {
        digest.len() == 64 && digest.bytes().all(|byte| byte.is_ascii_hexdigit())
    })
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BoundaryWorkerConfig {
    pub projection_batch_size: u32,
    pub dispatch_batch_size: u32,
    pub max_dispatch_attempts: u32,
    pub retry_delay_ms: u64,
    pub lease_duration_ms: u64,
    pub max_timer_horizon_ms: u64,
    pub max_expression_bytes: u32,
    pub worker_id: String,
    pub max_signal_id_bytes: u32,
    pub max_reference_bytes: u32,
    pub max_subscriptions_per_instance: u32,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct KafkaConfig {
    pub brokers: Vec<String>,
    pub topic: String,
    pub client_id: String,
    pub message_timeout_ms: u64,
}

#[derive(Debug, Error)]
pub enum RuntimeConfigError {
    #[error("read runtime configuration: {0}")]
    Read(std::io::Error),
    #[error("decode runtime configuration: {0}")]
    Decode(serde_json::Error),
    #[error("invalid runtime configuration: {0}")]
    Invalid(&'static str),
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn incomplete_configuration_fails_closed() {
        let decoded = serde_json::from_slice::<RuntimeConfig>(br"{}");
        assert!(decoded.is_err());
    }

    #[test]
    fn wasm_versions_must_be_full_sha256_pins() {
        assert!(is_sha256_version(&format!("sha256:{}", "a".repeat(64))));
        assert!(!is_sha256_version("latest"));
        assert!(!is_sha256_version("sha256:abc"));
    }
}
