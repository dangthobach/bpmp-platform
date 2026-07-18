//! Binary entry point for `authz-app`.

use std::sync::Arc;
use std::time::Duration;

use anyhow::Result;
use authz_db::{create_pool, DbPoolConfig};

use authz_app::bootstrap::AppContainer;
use authz_app::infrastructure::config::AppConfig;
use authz_app::infrastructure::messaging::{LoggingSink, OutboxWorker};
use authz_app::infrastructure::persistence::run_app_migrations;
use authz_app::presentation::router::build_router;
use authz_app::telemetry;

#[tokio::main]
async fn main() -> Result<()> {
    let cfg = AppConfig::from_env()?;
    let _telemetry = telemetry::init(&cfg.service_name, cfg.otlp_endpoint.as_deref())?;

    let pool = create_pool(&DbPoolConfig {
        database_url: cfg.database_url.clone(),
        max_connections: cfg.db_max_connections,
        min_connections: cfg.db_min_connections,
        ..Default::default()
    })
    .await?;
    run_app_migrations(&pool).await?;

    let container = AppContainer::build(&cfg, pool.clone())?;

    // Background: outbox publisher
    let worker = OutboxWorker::new(
        pool.clone(),
        Arc::new(LoggingSink),
        Duration::from_millis(cfg.outbox_poll_interval_ms),
        cfg.outbox_batch_size,
    );
    tokio::spawn(worker.run());

    let addr = cfg.socket_addr()?;
    let listener = tokio::net::TcpListener::bind(addr).await?;
    tracing::info!(%addr, "authz-app listening");

    let router = build_router(container);
    axum::serve(listener, router).await?;
    Ok(())
}
