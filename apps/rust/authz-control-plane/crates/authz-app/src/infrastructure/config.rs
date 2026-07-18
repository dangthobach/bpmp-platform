//! Application configuration loaded from environment variables.
//!
//! All required vars are validated at startup — fail-fast.

use anyhow::{Context, Result};
use std::net::SocketAddr;
use std::time::Duration;

#[derive(Debug, Clone)]
pub struct AppConfig {
    pub host: String,
    pub port: u16,
    pub database_url: String,
    pub db_max_connections: u32,
    pub db_min_connections: u32,

    /// AuthZ PDP base URL (e.g. http://authz-server:8080).
    pub authz_pdp_url: String,
    pub authz_timeout_ms: u64,
    pub authz_cache_capacity: u64,
    pub authz_cache_ttl_secs: u64,

    /// JWT verification (Keycloak JWKS).
    pub jwt_jwks_url: String,
    pub jwt_audience: String,

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
        Ok(Self {
            host: env_or("HOST", "0.0.0.0"),
            port: env_parse("PORT", 9090)?,
            database_url: std::env::var("DATABASE_URL").context("DATABASE_URL is required")?,
            db_max_connections: env_parse("DB_MAX_CONNECTIONS", 20)?,
            db_min_connections: env_parse("DB_MIN_CONNECTIONS", 2)?,
            authz_pdp_url: env_or("AUTHZ_PDP_URL", "http://localhost:8080"),
            authz_timeout_ms: env_parse("AUTHZ_TIMEOUT_MS", 500)?,
            authz_cache_capacity: env_parse("AUTHZ_CACHE_CAPACITY", 100_000)?,
            authz_cache_ttl_secs: env_parse("AUTHZ_CACHE_TTL_SECS", 30)?,
            jwt_jwks_url: std::env::var("JWT_JWKS_URL").context("JWT_JWKS_URL is required")?,
            jwt_audience: std::env::var("JWT_AUDIENCE").context("JWT_AUDIENCE is required")?,
            kafka_brokers: env_or("KAFKA_BROKERS", "localhost:9092"),
            outbox_poll_interval_ms: env_parse("OUTBOX_POLL_INTERVAL_MS", 1000)?,
            outbox_batch_size: env_parse("OUTBOX_BATCH_SIZE", 100)?,
            otlp_endpoint: std::env::var("OTLP_ENDPOINT").ok(),
            service_name: env_or("SERVICE_NAME", "authz-app"),
        })
    }

    pub fn socket_addr(&self) -> Result<SocketAddr> {
        format!("{}:{}", self.host, self.port)
            .parse()
            .context("invalid host/port")
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
