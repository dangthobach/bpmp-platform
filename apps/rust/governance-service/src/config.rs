#![cfg_attr(not(target_os = "linux"), allow(dead_code))]

use std::fs;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};

use serde::Deserialize;
use thiserror::Error;

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeConfig {
    pub listen_addr: SocketAddr,
    pub health_addr: SocketAddr,
    pub postgres: PostgresConfig,
    pub tls: TlsConfig,
    pub engines: Vec<EngineEndpoint>,
    pub configuration_resolver: ConfigurationResolverConfig,
    pub kafka: KafkaConfig,
    pub key_lifecycle: KeyLifecycleConfig,
    pub worker: WorkerConfig,
    pub grpc: GrpcConfig,
}

impl RuntimeConfig {
    pub fn load(path: &Path) -> Result<Self, ConfigError> {
        let bytes = fs::read(path).map_err(ConfigError::Read)?;
        let value: Self = serde_json::from_slice(&bytes).map_err(ConfigError::Decode)?;
        value.validate()?;
        Ok(value)
    }

    fn validate(&self) -> Result<(), ConfigError> {
        if self.engines.is_empty()
            || self.kafka.brokers.is_empty()
            || self.kafka.configuration_topic.trim().is_empty()
            || self.kafka.consumer_group.trim().is_empty()
            || self.postgres.dsn.trim().is_empty()
            || self.postgres.max_connections == 0
            || self.postgres.acquire_timeout_ms == 0
            || self.grpc.max_decoding_bytes == 0
            || self.grpc.max_encoding_bytes == 0
            || self.grpc.request_timeout_ms == 0
            || self.grpc.authorized_client_certificate_sha256.is_empty()
            || self.grpc.authorized_methods.is_empty()
            || self.grpc.admission_rate_rps == 0
            || self.grpc.admission_burst == 0
            || self.worker.poll_interval_ms == 0
            || self.worker.shred_batch_size == 0
            || self.worker.shred_lease_ms == 0
            || self
                .key_lifecycle
                .revocation_barrier_endpoint
                .trim()
                .is_empty()
            || self.key_lifecycle.kms_endpoint.trim().is_empty()
            || self.tls.server_certificate.as_os_str().is_empty()
            || self.tls.server_private_key.as_os_str().is_empty()
            || self.tls.client_ca.as_os_str().is_empty()
        {
            return Err(ConfigError::Invalid);
        }
        if self.engines.iter().any(|engine| {
            engine.endpoint.trim().is_empty()
                || engine.tls_domain.trim().is_empty()
                || engine.timeout_ms == 0
                || engine.connect_max_attempts == 0
                || engine.connect_retry_ms == 0
        }) || self.configuration_resolver.endpoint.trim().is_empty()
            || self.configuration_resolver.tls_domain.trim().is_empty()
            || self
                .configuration_resolver
                .platform_reference
                .trim()
                .is_empty()
            || self
                .configuration_resolver
                .environment_reference
                .trim()
                .is_empty()
            || self.configuration_resolver.timeout_ms == 0
        {
            return Err(ConfigError::Invalid);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PostgresConfig {
    pub dsn: String,
    pub max_connections: u32,
    pub acquire_timeout_ms: u64,
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
pub struct EngineEndpoint {
    pub endpoint: String,
    pub tls_domain: String,
    pub timeout_ms: u64,
    pub connect_max_attempts: u32,
    pub connect_retry_ms: u64,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConfigurationResolverConfig {
    pub endpoint: String,
    pub tls_domain: String,
    pub platform_reference: String,
    pub environment_reference: String,
    pub timeout_ms: u64,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct KafkaConfig {
    pub brokers: Vec<String>,
    pub client_id: String,
    pub consumer_group: String,
    pub configuration_topic: String,
    pub session_timeout_ms: u64,
    pub max_message_bytes: usize,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct KeyLifecycleConfig {
    pub revocation_barrier_endpoint: String,
    pub kms_endpoint: String,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WorkerConfig {
    pub worker_id: String,
    pub poll_interval_ms: u64,
    pub shred_batch_size: u32,
    pub shred_lease_ms: u64,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GrpcConfig {
    pub max_decoding_bytes: usize,
    pub max_encoding_bytes: usize,
    pub request_timeout_ms: u64,
    pub authorized_client_certificate_sha256: Vec<String>,
    pub authorized_methods: Vec<String>,
    pub admission_rate_rps: u32,
    pub admission_burst: u32,
    pub reflection_enabled: bool,
}

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("read governance configuration: {0}")]
    Read(#[source] std::io::Error),
    #[error("decode governance configuration: {0}")]
    Decode(#[source] serde_json::Error),
    #[error("governance configuration is incomplete or unbounded")]
    Invalid,
}

#[cfg(test)]
mod tests {
    use std::fs;

    use serde_json::{Value, json};
    use tempfile::tempdir;

    use super::{ConfigError, RuntimeConfig};

    fn valid_config() -> Value {
        json!({
            "listen_addr": "127.0.0.1:7501",
            "health_addr": "127.0.0.1:7502",
            "postgres": {
                "dsn": "postgres://bpmp:secret@localhost/governance",
                "max_connections": 8,
                "acquire_timeout_ms": 3000
            },
            "tls": {
                "server_certificate": "server.pem",
                "server_private_key": "server-key.pem",
                "client_ca": "ca.pem"
            },
            "engines": [{
                "endpoint": "https://engine1:7101",
                "tls_domain": "engine1",
                "timeout_ms": 3000,
                "connect_max_attempts": 10,
                "connect_retry_ms": 100
            }],
            "configuration_resolver": {
                "endpoint": "https://configuration-service:7302",
                "tls_domain": "configuration-service",
                "platform_reference": "bpmp",
                "environment_reference": "production",
                "timeout_ms": 3000
            },
            "kafka": {
                "brokers": ["kafka:9092"],
                "client_id": "governance-service",
                "consumer_group": "bpmp.governance.configuration-reloader.v1",
                "configuration_topic": "bpmp.configuration.publications.v1",
                "session_timeout_ms": 6000,
                "max_message_bytes": 1_048_576
            },
            "key_lifecycle": {
                "revocation_barrier_endpoint": "https://keys/barrier",
                "kms_endpoint": "https://keys/shred"
            },
            "worker": {
                "worker_id": "governance-1",
                "poll_interval_ms": 250,
                "shred_batch_size": 32,
                "shred_lease_ms": 5000
            },
            "grpc": {
                "max_decoding_bytes": 1_048_576,
                "max_encoding_bytes": 1_048_576,
                "request_timeout_ms": 5000,
                "authorized_client_certificate_sha256": ["0000000000000000000000000000000000000000000000000000000000000000"],
                "authorized_methods": [
                    "/bpmp.governance.v1.GovernanceApprovalService/CreateApproval",
                    "/bpmp.governance.v1.GovernanceApprovalService/RecordApproval",
                    "/bpmp.governance.v1.GovernanceApprovalService/SubmitApproval",
                    "/bpmp.governance.v1.GovernanceApprovalService/GetApproval"
                ],
                "admission_rate_rps": 2000,
                "admission_burst": 200,
                "reflection_enabled": true
            }
        })
    }

    fn load(value: &Value) -> Result<RuntimeConfig, ConfigError> {
        let directory = tempdir().expect("create temporary directory");
        let path = directory.path().join("governance.json");
        fs::write(&path, serde_json::to_vec(value).expect("encode config")).expect("write config");
        RuntimeConfig::load(&path)
    }

    #[test]
    fn accepts_complete_bounded_configuration() {
        let config = load(&valid_config()).expect("configuration should be valid");
        assert_eq!(config.engines.len(), 1);
        assert_eq!(config.worker.shred_batch_size, 32);
    }

    #[test]
    fn rejects_unbounded_worker_and_empty_engine_cluster() {
        let mut value = valid_config();
        value["worker"]["shred_batch_size"] = json!(0);
        value["engines"] = json!([]);
        assert!(matches!(load(&value), Err(ConfigError::Invalid)));
    }

    #[test]
    fn rejects_unknown_fields() {
        let mut value = valid_config();
        value["hardcoded_override"] = json!(true);
        assert!(matches!(load(&value), Err(ConfigError::Decode(_))));
    }
}
