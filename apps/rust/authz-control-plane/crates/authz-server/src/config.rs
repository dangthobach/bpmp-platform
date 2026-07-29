//! Server configuration loaded from environment variables.

use anyhow::{Context, Result};
use std::net::SocketAddr;

/// Full server configuration.
#[derive(Debug, Clone)]
pub struct ServerConfig {
    pub host: String,
    pub port: u16,
    pub grpc_port: u16,
    pub database_url: String,
    pub db_max_connections: u32,
    pub db_min_connections: u32,
    /// JWT JWKS URL (e.g. Keycloak: http://keycloak:8080/realms/{realm}/protocol/openid-connect/certs)
    pub jwt_jwks_url: String,
    pub jwt_audience: String,
    /// Default fail mode: "DENY" or "OPEN"
    pub fail_mode: String,
    /// ReBAC max traversal depth
    pub rebac_max_depth: u32,
    pub rebac_timeout_ms: u64,
    pub inactive_job_interval_secs: u64,
    pub inactive_job_threshold_days: i32,
    pub inactive_job_batch_size: i64,
    pub kafka_brokers: Vec<String>,
    pub kafka_client_id: String,
    pub kafka_security_protocol: String,
    pub kafka_ca_file: Option<String>,
    pub kafka_certificate_file: Option<String>,
    pub kafka_private_key_file: Option<String>,
    pub tenant_lifecycle_topic: String,
    pub tenant_readiness_topic: String,
    pub tenant_readiness_consumer_group: String,
    pub tenant_lifecycle_worker_id: String,
    pub tenant_lifecycle_batch_size: i64,
    pub tenant_lifecycle_lease_ms: i64,
    pub tenant_lifecycle_poll_ms: u64,
    pub kafka_max_message_bytes: usize,
}

impl ServerConfig {
    /// Loads configuration from environment variables.
    ///
    /// All required vars are validated at startup — fail-fast.
    pub fn from_env() -> Result<Self> {
        let kafka_brokers = required("KAFKA_BROKERS")?
            .split(',')
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .map(str::to_owned)
            .collect::<Vec<_>>();
        if kafka_brokers.is_empty() {
            anyhow::bail!("KAFKA_BROKERS must contain at least one broker");
        }
        let kafka_security_protocol = required("KAFKA_SECURITY_PROTOCOL")?;
        let (kafka_ca_file, kafka_certificate_file, kafka_private_key_file) =
            if kafka_security_protocol == "SSL" {
                (
                    Some(required("KAFKA_CA_FILE")?),
                    Some(required("KAFKA_CERTIFICATE_FILE")?),
                    Some(required("KAFKA_PRIVATE_KEY_FILE")?),
                )
            } else if kafka_security_protocol == "PLAINTEXT" {
                (None, None, None)
            } else {
                anyhow::bail!("KAFKA_SECURITY_PROTOCOL must be PLAINTEXT or SSL");
            };
        let config = Self {
            host: std::env::var("HOST").unwrap_or_else(|_| "0.0.0.0".to_owned()),
            port: std::env::var("PORT")
                .unwrap_or_else(|_| "8080".to_owned())
                .parse::<u16>()
                .context("Invalid PORT — must be a valid port number")?,
            grpc_port: std::env::var("GRPC_PORT")
                .unwrap_or_else(|_| "50051".to_owned())
                .parse::<u16>()
                .context("Invalid GRPC_PORT")?,
            database_url: std::env::var("DATABASE_URL").context("DATABASE_URL is required")?,
            db_max_connections: std::env::var("DB_MAX_CONNECTIONS")
                .unwrap_or_else(|_| "20".to_owned())
                .parse()
                .unwrap_or(20),
            db_min_connections: std::env::var("DB_MIN_CONNECTIONS")
                .unwrap_or_else(|_| "2".to_owned())
                .parse()
                .unwrap_or(2),
            jwt_jwks_url: std::env::var("JWT_JWKS_URL").context("JWT_JWKS_URL is required")?,
            jwt_audience: std::env::var("JWT_AUDIENCE").context("JWT_AUDIENCE is required")?,
            fail_mode: std::env::var("FAIL_MODE").unwrap_or_else(|_| "DENY".to_owned()),
            rebac_max_depth: std::env::var("REBAC_MAX_DEPTH")
                .unwrap_or_else(|_| "10".to_owned())
                .parse()
                .unwrap_or(10),
            rebac_timeout_ms: std::env::var("REBAC_TIMEOUT_MS")
                .unwrap_or_else(|_| "50".to_owned())
                .parse()
                .unwrap_or(50),
            inactive_job_interval_secs: std::env::var("INACTIVE_JOB_INTERVAL_SECS")
                .unwrap_or_else(|_| "86400".to_owned())
                .parse()
                .unwrap_or(86400),
            inactive_job_threshold_days: std::env::var("INACTIVE_JOB_THRESHOLD_DAYS")
                .unwrap_or_else(|_| "60".to_owned())
                .parse()
                .unwrap_or(60),
            inactive_job_batch_size: std::env::var("INACTIVE_JOB_BATCH_SIZE")
                .unwrap_or_else(|_| "1000".to_owned())
                .parse()
                .unwrap_or(1000),
            kafka_brokers,
            kafka_client_id: required("KAFKA_CLIENT_ID")?,
            kafka_security_protocol,
            kafka_ca_file,
            kafka_certificate_file,
            kafka_private_key_file,
            tenant_lifecycle_topic: required("TENANT_LIFECYCLE_TOPIC")?,
            tenant_readiness_topic: required("TENANT_READINESS_TOPIC")?,
            tenant_readiness_consumer_group: required("TENANT_READINESS_CONSUMER_GROUP")?,
            tenant_lifecycle_worker_id: required("TENANT_LIFECYCLE_WORKER_ID")?,
            tenant_lifecycle_batch_size: required("TENANT_LIFECYCLE_BATCH_SIZE")?
                .parse()
                .context("TENANT_LIFECYCLE_BATCH_SIZE is invalid")?,
            tenant_lifecycle_lease_ms: required("TENANT_LIFECYCLE_LEASE_MS")?
                .parse()
                .context("TENANT_LIFECYCLE_LEASE_MS is invalid")?,
            tenant_lifecycle_poll_ms: required("TENANT_LIFECYCLE_POLL_MS")?
                .parse()
                .context("TENANT_LIFECYCLE_POLL_MS is invalid")?,
            kafka_max_message_bytes: required("KAFKA_MAX_MESSAGE_BYTES")?
                .parse()
                .context("KAFKA_MAX_MESSAGE_BYTES is invalid")?,
        };
        if config.tenant_lifecycle_batch_size <= 0
            || config.tenant_lifecycle_lease_ms <= 0
            || config.tenant_lifecycle_poll_ms == 0
            || config.kafka_max_message_bytes == 0
        {
            anyhow::bail!("tenant lifecycle Kafka bounds must be positive");
        }
        Ok(config)
    }

    pub fn socket_addr(&self) -> Result<SocketAddr> {
        format!("{}:{}", self.host, self.port)
            .parse()
            .context("Invalid host/port combination")
    }
}

fn required(name: &str) -> Result<String> {
    let value = std::env::var(name).with_context(|| format!("{name} is required"))?;
    if value.trim().is_empty() {
        anyhow::bail!("{name} must not be empty");
    }
    Ok(value)
}
