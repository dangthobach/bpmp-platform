//! Axum application builder and server startup.

use std::sync::Arc;

#[cfg(target_os = "linux")]
use std::future::IntoFuture;

use anyhow::{Context, Result};
use authz_http_middleware::{apply as apply_transport, Config as TransportConfig};
use axum::{
    middleware,
    routing::{get, post},
    Router,
};

use authz_core::models::tenant::FailMode;
use authz_core::AuthzError;
use authz_db::{create_pool, run_migrations, DbPoolConfig};
use authz_engine::{
    algorithms::{bitmap::PermissionBitmapEngine, cuckoo::PermissionCuckooFilter},
    cache::{BundleLoader, EmergencyRevokeCache, LoadedEngines, PolicyBundleCache},
    evaluator::{
        abac::JitAttributeFetcher,
        pipeline::AuthzEvaluationPipeline,
        rebac::{ReBacConfig, ReBacEngine},
    },
    filter::{
        elasticsearch::EsFilterTranslator, mongodb::MongoFilterTranslator,
        sql::SqlFilterTranslator, translator::FilterTranslatorRegistry,
    },
    shadow::ShadowEngine,
};
use serde_json::Value as JsonValue;
use sqlx::PgPool;

use crate::{
    config::ServerConfig, handlers::health::health_handler,
    middleware::service_auth::require_service_jwt, state::AppState,
};

// ─── No-op JIT Fetcher (MVP) ──────────────────────────────────────────────────
// Replace this with a real HTTP fetcher in production.

struct NoopJitFetcher;

#[async_trait::async_trait]
impl JitAttributeFetcher for NoopJitFetcher {
    async fn fetch(
        &self,
        _source: &str,
        _user_id: &str,
        _key: &str,
        _tenant_id: &str,
    ) -> Result<JsonValue, AuthzError> {
        Ok(JsonValue::Null)
    }
}

/// Builds the Axum router.
pub fn build_router(state: AppState, transport: TransportConfig) -> Result<Router> {
    let protected_routes = Router::new()
        .route("/authz/v1/check", post(crate::handlers::check_handler))
        .route("/authz/v1/filter", post(crate::handlers::filter_handler))
        .route("/authz/v1/explain", post(crate::handlers::explain_handler))
        .nest("/admin/v1", crate::handlers::admin::admin_routes())
        .layer(middleware::from_fn_with_state(
            state.clone(),
            require_service_jwt,
        ));

    let router = Router::new()
        // Health check
        .route("/health", get(health_handler))
        .merge(protected_routes)
        .with_state(state);
    apply_transport(router, transport).map_err(anyhow::Error::msg)
}

/// Starts the server: connects to DB, runs migrations, wires dependencies, listens.
pub async fn run(config: ServerConfig) -> Result<()> {
    // ── Database ──────────────────────────────────────────────────────────────
    let pool = connect_and_migrate(&config).await?;

    // ── Policy engine wiring ───────────────────────────────────────────────────
    let rebac_config = ReBacConfig {
        max_depth: config.rebac_max_depth,
        traversal_timeout_ms: config.rebac_timeout_ms,
        ..Default::default()
    };

    let rebac_engine = Arc::new(ReBacEngine::new(pool.clone(), rebac_config));

    let filter_registry = Arc::new(FilterTranslatorRegistry::new(
        Box::new(SqlFilterTranslator),
        Box::new(EsFilterTranslator::new(rebac_engine.clone())),
        Box::new(MongoFilterTranslator::new(rebac_engine.clone())),
    ));

    let emergency_revoke = Arc::new(EmergencyRevokeCache::default());

    let fail_mode = match config.fail_mode.as_str() {
        "OPEN" => FailMode::Open,
        _ => FailMode::Deny,
    };

    // Warm up high-performance fast paths from the database.
    // On failure (e.g. fresh DB), fall back to empty engines so the server
    // still starts; a control-plane refresh will populate them later.
    let LoadedEngines {
        bitmap_engine,
        cuckoo_filter,
        policy_bundle_cache,
    } = match BundleLoader::new(pool.clone()).load_initial().await {
        Ok(engines) => engines,
        Err(e) => {
            tracing::warn!(error = %e, "Warm-up failed — starting with empty fast-path engines");
            LoadedEngines {
                bitmap_engine: Arc::new(PermissionBitmapEngine::default()),
                cuckoo_filter: Arc::new(PermissionCuckooFilter::new()),
                policy_bundle_cache: Arc::new(PolicyBundleCache::new()),
            }
        }
    };

    let shadow_engine = Arc::new(ShadowEngine::new(pool.clone()));

    let pipeline = Arc::new(AuthzEvaluationPipeline::new(
        pool.clone(),
        emergency_revoke.clone(),
        rebac_engine.clone(),
        filter_registry,
        Arc::new(NoopJitFetcher),
        fail_mode,
        cuckoo_filter,
        bitmap_engine,
        policy_bundle_cache,
        shadow_engine,
    ));

    let state = AppState {
        pool: pool.clone(),
        pipeline: pipeline.clone(),
        emergency_revoke,
        rebac_engine,
        jwt_jwks_url: config.jwt_jwks_url.clone(),
        jwt_audience: config.jwt_audience.clone(),
    };

    let job_pool = pool.clone();
    let job_interval = config.inactive_job_interval_secs;
    let job_threshold = config.inactive_job_threshold_days;
    let job_batch = config.inactive_job_batch_size;

    // Spawn Background Job
    tokio::spawn(async move {
        crate::job::inactive_user::start_inactive_user_job(
            job_pool,
            job_interval,
            job_threshold,
            job_batch,
        )
        .await;
    });

    let transport = TransportConfig {
        service: config.service_name.clone(),
        request_timeout: std::time::Duration::from_millis(config.server_request_timeout_ms),
        max_body_bytes: config.server_max_body_bytes,
        rate_limit_requests_per_second: config.rate_limit_requests_per_second,
        rate_limit_burst: config.rate_limit_burst,
    };

    // ── Start server ──────────────────────────────────────────────────────────
    let addr = config.socket_addr()?;
    let listener = tokio::net::TcpListener::bind(addr).await?;

    tracing::info!(addr = %addr, "AuthZ REST server listening");

    #[cfg(target_os = "linux")]
    {
        let lifecycle_publisher = tokio::spawn(crate::tenant_lifecycle::run_publisher(
            pool.clone(),
            config.clone(),
        ));
        let readiness_consumer = tokio::spawn(crate::tenant_lifecycle::run_readiness_consumer(
            pool, config,
        ));
        let server = axum::serve(listener, build_router(state, transport)?).into_future();
        tokio::pin!(server);
        tokio::select! {
            result = &mut server => result.context("serve AuthZ HTTP"),
            result = lifecycle_publisher => match result {
                Ok(Ok(())) => anyhow::bail!("tenant lifecycle publisher stopped unexpectedly"),
                Ok(Err(error)) => Err(error).context("tenant lifecycle publisher failed"),
                Err(error) => Err(anyhow::Error::from(error))
                    .context("tenant lifecycle publisher panicked"),
            },
            result = readiness_consumer => match result {
                Ok(Ok(())) => anyhow::bail!("tenant readiness consumer stopped unexpectedly"),
                Ok(Err(error)) => Err(error).context("tenant readiness consumer failed"),
                Err(error) => Err(anyhow::Error::from(error))
                    .context("tenant readiness consumer panicked"),
            },
        }
    }
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (pool, config);
        axum::serve(listener, build_router(state, transport)?)
            .await
            .context("serve AuthZ HTTP")
    }
}

/// Applies the embedded AuthZ schema without starting listeners or workers.
pub async fn migrate(config: &ServerConfig) -> Result<()> {
    let _ = connect_and_migrate(config).await?;
    Ok(())
}

async fn connect_and_migrate(config: &ServerConfig) -> Result<PgPool> {
    let pool = create_pool(&DbPoolConfig {
        database_url: config.database_url.clone(),
        max_connections: config.db_max_connections,
        min_connections: config.db_min_connections,
        ..Default::default()
    })
    .await?;

    run_migrations(&pool).await?;
    Ok(pool)
}
