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
}

impl ServerConfig {
    /// Loads configuration from environment variables.
    ///
    /// All required vars are validated at startup — fail-fast.
    pub fn from_env() -> Result<Self> {
        Ok(Self {
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
        })
    }

    pub fn socket_addr(&self) -> Result<SocketAddr> {
        format!("{}:{}", self.host, self.port)
            .parse()
            .context("Invalid host/port combination")
    }
}
