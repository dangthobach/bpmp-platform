//! `authz-server` — Axum HTTP server for the AuthZ platform.
//!
//! Routes:
//! This deployable exposes policy administration only. Authoritative workflow
//! transition decisions run inside `bpmp-engine`.
//! - `POST /authz/v1/filter` — Row filter generation
//! - `POST /authz/v1/explain` — Decision trace (Explain API, G7)
//! - `POST /authz/v1/relations` — Insert a relation tuple
//! - `POST /authz/v1/policies/versions` — Policy version management
//! - `GET  /health` — Health check

pub mod app;
pub mod config;
pub mod error;
pub mod grpc;
pub mod handlers;
pub mod job;
pub mod middleware;
pub mod state;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    // Initialize structured JSON logging
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(
            tracing_subscriber::EnvFilter::from_default_env()
                .add_directive("authz_server=debug".parse()?)
                .add_directive("authz_engine=debug".parse()?)
                .add_directive("authz_db=info".parse()?),
        )
        .init();

    let config = config::ServerConfig::from_env()?;

    tracing::info!(
        host = %config.host,
        port = config.port,
        "Starting AuthZ server"
    );

    app::run(config).await
}
