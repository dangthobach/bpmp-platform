//! Application configuration loaded from environment variables.
//!
//! All required vars are validated at startup — fail-fast.

use anyhow::{Context, Result};
use std::net::SocketAddr;
use std::path::PathBuf;
use std::time::Duration;

#[derive(Debug, Clone)]
pub struct AppConfig {
    pub host: String,
    pub port: u16,
    pub https_port: Option<u16>,
    pub https_certificate_path: Option<PathBuf>,
    pub https_private_key_path: Option<PathBuf>,
    pub https_client_ca_path: Option<PathBuf>,
    pub server_request_timeout_ms: u64,
    pub server_max_body_bytes: usize,
    pub rate_limit_requests_per_second: u32,
    pub rate_limit_burst: u32,
    pub database_url: String,
    pub db_max_connections: u32,
    pub db_min_connections: u32,

    /// AuthZ PDP base URL (e.g. http://authz-server:8080).
    pub authz_pdp_url: String,
    pub authz_service_token_file: PathBuf,
    pub authz_timeout_ms: u64,
    pub authz_cache_capacity: u64,
    pub authz_cache_ttl_secs: u64,

    /// JWT verification (Keycloak JWKS).
    pub jwt_jwks_url: String,
    pub jwt_audience: String,

    /// Optional verifier configuration for Engine's signed branch-scope
    /// lookup calls. The endpoint stays unavailable until explicitly enabled.
    pub branch_scope_workload_keys_file: Option<PathBuf>,

    /// Outbox publisher.
    pub kafka_brokers: String,
    pub outbox_poll_interval_ms: u64,
    pub outbox_batch_size: i64,

    /// Telemetry.
    pub otlp_endpoint: Option<String>,
    pub service_name: String,
}

impl AppConfig {
    pub fn from_env() -> Result<Self> {
        let config = Self {
            host: env_or("HOST", "0.0.0.0"),
            port: env_parse("PORT", 9090)?,
            https_port: env_optional_parse("HTTPS_PORT")?,
            https_certificate_path: std::env::var("HTTPS_CERTIFICATE_PATH")
                .ok()
                .map(PathBuf::from),
            https_private_key_path: std::env::var("HTTPS_PRIVATE_KEY_PATH")
                .ok()
                .map(PathBuf::from),
            https_client_ca_path: std::env::var("HTTPS_CLIENT_CA_PATH")
                .ok()
                .map(PathBuf::from),
            server_request_timeout_ms: env_required_parse("SERVER_REQUEST_TIMEOUT_MS")?,
            server_max_body_bytes: env_required_parse("SERVER_MAX_BODY_BYTES")?,
            rate_limit_requests_per_second: env_required_parse("RATE_LIMIT_RPS")?,
            rate_limit_burst: env_required_parse("RATE_LIMIT_BURST")?,
            database_url: std::env::var("DATABASE_URL").context("DATABASE_URL is required")?,
            db_max_connections: env_parse("DB_MAX_CONNECTIONS", 20)?,
            db_min_connections: env_parse("DB_MIN_CONNECTIONS", 2)?,
            authz_pdp_url: env_or("AUTHZ_PDP_URL", "http://localhost:8080"),
            authz_service_token_file: PathBuf::from(
                std::env::var("AUTHZ_SERVICE_TOKEN_FILE")
                    .context("AUTHZ_SERVICE_TOKEN_FILE is required")?,
            ),
            authz_timeout_ms: env_parse("AUTHZ_TIMEOUT_MS", 500)?,
            authz_cache_capacity: env_parse("AUTHZ_CACHE_CAPACITY", 100_000)?,
            authz_cache_ttl_secs: env_parse("AUTHZ_CACHE_TTL_SECS", 30)?,
            jwt_jwks_url: std::env::var("JWT_JWKS_URL").context("JWT_JWKS_URL is required")?,
            jwt_audience: std::env::var("JWT_AUDIENCE").context("JWT_AUDIENCE is required")?,
            branch_scope_workload_keys_file: std::env::var("BRANCH_SCOPE_WORKLOAD_KEYS_FILE")
                .ok()
                .map(PathBuf::from),
            kafka_brokers: env_or("KAFKA_BROKERS", "localhost:9092"),
            outbox_poll_interval_ms: env_parse("OUTBOX_POLL_INTERVAL_MS", 1000)?,
            outbox_batch_size: env_parse("OUTBOX_BATCH_SIZE", 100)?,
            otlp_endpoint: std::env::var("OTLP_ENDPOINT").ok(),
            service_name: env_or("SERVICE_NAME", "authz-app"),
        };
        config.validate_https()?;
        Ok(config)
    }

    pub fn socket_addr(&self) -> Result<SocketAddr> {
        format!("{}:{}", self.host, self.port)
            .parse()
            .context("invalid host/port")
    }

    pub fn https_socket_addr(&self) -> Result<Option<SocketAddr>> {
        self.https_port
            .map(|port| {
                format!("{}:{port}", self.host)
                    .parse()
                    .context("invalid HTTPS host/port")
            })
            .transpose()
    }

    pub fn validate_https(&self) -> Result<()> {
        let configured = [
            self.https_port.is_some(),
            self.https_certificate_path.is_some(),
            self.https_private_key_path.is_some(),
            self.https_client_ca_path.is_some(),
        ];
        if configured.iter().any(|value| *value) && configured.iter().any(|value| !*value) {
            anyhow::bail!("HTTPS_PORT and certificate, private key, and client CA paths must be configured together");
        }
        if self
            .https_port
            .is_some_and(|port| port == 0 || port == self.port)
        {
            anyhow::bail!("HTTPS_PORT must be non-zero and distinct from PORT");
        }
        if self.branch_scope_workload_keys_file.is_some() && self.https_port.is_none() {
            anyhow::bail!("branch scope workload verification requires the mTLS HTTPS listener");
        }
        Ok(())
    }

    pub fn authz_timeout(&self) -> Duration {
        Duration::from_millis(self.authz_timeout_ms)
    }
    pub fn authz_cache_ttl(&self) -> Duration {
        Duration::from_secs(self.authz_cache_ttl_secs)
    }
}

fn env_or(key: &str, fallback: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| fallback.to_owned())
}

fn env_parse<T: std::str::FromStr>(key: &str, fallback: T) -> Result<T>
where
    T::Err: std::fmt::Display,
{
    match std::env::var(key) {
        Ok(v) => v
            .parse::<T>()
            .map_err(|e| anyhow::anyhow!("invalid {key}: {e}")),
        Err(_) => Ok(fallback),
    }
}

fn env_required_parse<T: std::str::FromStr>(key: &str) -> Result<T>
where
    T::Err: std::fmt::Display,
{
    std::env::var(key)
        .with_context(|| format!("{key} is required"))?
        .parse::<T>()
        .map_err(|error| anyhow::anyhow!("invalid {key}: {error}"))
}

fn env_optional_parse<T: std::str::FromStr>(key: &str) -> Result<Option<T>>
where
    T::Err: std::fmt::Display,
{
    std::env::var(key)
        .ok()
        .map(|value| {
            value
                .parse::<T>()
                .map_err(|error| anyhow::anyhow!("invalid {key}: {error}"))
        })
        .transpose()
}
