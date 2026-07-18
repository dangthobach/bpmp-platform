//! PostgreSQL connection pool configuration.
//!
//! Uses sqlx PgPool with tunable parameters loaded from environment config.
//! All queries go through this shared pool — never create ad-hoc connections.

use sqlx::{postgres::PgPoolOptions, PgPool};
use std::time::Duration;

/// Type alias for the shared PostgreSQL connection pool.
pub type DbPool = PgPool;

/// Configuration parameters for the connection pool.
#[derive(Debug, Clone)]
pub struct DbPoolConfig {
    pub database_url: String,
    pub max_connections: u32,
    pub min_connections: u32,
    pub acquire_timeout_secs: u64,
    pub idle_timeout_secs: u64,
}

impl Default for DbPoolConfig {
    fn default() -> Self {
        Self {
            database_url: String::new(),
            max_connections: 20,
            min_connections: 2,
            acquire_timeout_secs: 10,
            idle_timeout_secs: 300,
        }
    }
}

/// Creates and validates the connection pool.
///
/// Returns an error if the pool cannot be established or if a test query fails.
/// Called once at server startup — fail fast is correct here.
pub async fn create_pool(config: &DbPoolConfig) -> Result<DbPool, sqlx::Error> {
    let pool = PgPoolOptions::new()
        .max_connections(config.max_connections)
        .min_connections(config.min_connections)
        .acquire_timeout(Duration::from_secs(config.acquire_timeout_secs))
        .idle_timeout(Duration::from_secs(config.idle_timeout_secs))
        .connect(&config.database_url)
        .await?;

    // Validate connectivity before returning
    sqlx::query("SELECT 1").execute(&pool).await?;

    tracing::info!(
        max_connections = config.max_connections,
        min_connections = config.min_connections,
        "PostgreSQL connection pool established"
    );

    Ok(pool)
}

/// Runs all pending sqlx migrations.
///
/// Called at server startup. Safe to call multiple times (idempotent).
pub async fn run_migrations(pool: &DbPool) -> Result<(), sqlx::migrate::MigrateError> {
    tracing::info!("Running database migrations...");
    sqlx::migrate!("./migrations").run(pool).await?;
    tracing::info!("Database migrations completed");
    Ok(())
}
