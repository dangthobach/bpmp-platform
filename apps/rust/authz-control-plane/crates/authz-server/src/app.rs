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
use hyper::{body::Incoming, service::service_fn, Request};
use hyper_util::rt::TokioIo;

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
use bpmp_authz_contracts::Ed25519Signer;
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use tokio_rustls::rustls::{self, Certificate, PrivateKey};
use tower::ServiceExt;

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
        Err(AuthzError::InvalidRequest {
            reason: "External attribute provider is not configured".into(),
        })
    }
}

/// Builds the Axum router.
pub fn build_router(state: AppState, transport: TransportConfig) -> Result<Router> {
    let protected_routes = Router::new()
        .route("/authz/v1/check", post(crate::handlers::check_handler))
        .route("/authz/v1/filter", post(crate::handlers::filter_handler))
        .route("/authz/v1/query", post(crate::handlers::query::query))
        .route(
            "/authz/v1/query-revoke-feed",
            post(crate::handlers::query_revoke::feed),
        )
        .route(
            "/admin/v1/query-revoke",
            post(crate::handlers::query_revoke::change),
        )
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

fn load_policy_bundle_signing(
    config: &ServerConfig,
) -> Result<Option<crate::state::PolicyBundleSigning>> {
    let (Some(key_id), Some(path)) = (
        config.policy_signing_key_id.as_ref(),
        config.policy_signing_key_path.as_ref(),
    ) else {
        return Ok(None);
    };
    let bytes = std::fs::read(path)
        .with_context(|| format!("read authorization policy signing key {path}"))?;
    let seed: [u8; 32] = bytes.as_slice().try_into().map_err(|_| {
        anyhow::anyhow!("authorization policy signing seed must contain exactly 32 bytes")
    })?;
    Ok(Some(crate::state::PolicyBundleSigning {
        key_id: key_id.clone(),
        signer: Arc::new(Ed25519Signer::from_bytes(&seed)),
    }))
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
        owner_manifest_key_ids: config.owner_manifest_key_ids.clone(),
        policy_bundle_signing: load_policy_bundle_signing(&config)?,
        policy_bundle_max_bytes: config
            .kafka_max_message_bytes
            .saturating_sub(1024)
            .min(config.server_max_body_bytes.saturating_sub(1024))
            .min(4 * 1024 * 1024),
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
    let mut https_server = if let Some(https_addr) = config.https_socket_addr()? {
        let https_listener = tokio::net::TcpListener::bind(https_addr).await?;
        let tls = load_https_server_config(&config)?;
        let https_router = build_router(state.clone(), transport.clone())?;
        Some(tokio::spawn(serve_https(https_listener, https_router, tls)))
    } else {
        None
    };

    tracing::info!(addr = %addr, "AuthZ REST server listening");

    #[cfg(target_os = "linux")]
    {
        let lifecycle_publisher = tokio::spawn(crate::tenant_lifecycle::run_publisher(
            pool.clone(),
            config.clone(),
        ));
        let readiness_consumer = tokio::spawn(crate::tenant_lifecycle::run_readiness_consumer(
            pool.clone(),
            config.clone(),
        ));
        let revoke_publisher = tokio::spawn(crate::query_revoke_publisher::run(
            pool.clone(),
            config.clone(),
        ));
        let bundle_publisher = tokio::spawn(crate::policy_bundle_publisher::run(
            state.pool.clone(),
            config.clone(),
            state.policy_bundle_signing.clone(),
            state.policy_bundle_max_bytes,
        ));
        let bundle_acknowledgement_consumer = tokio::spawn(
            crate::policy_bundle_acknowledgement_consumer::run(pool.clone(), config.clone()),
        );
        let server = axum::serve(listener, build_router(state, transport)?).into_future();
        tokio::pin!(server);
        let result = tokio::select! {
            result=revoke_publisher=>match result {Ok(Ok(()))=>anyhow::bail!("query revoke publisher stopped"),Ok(Err(error))=>Err(error).context("query revoke publisher failed"),Err(error)=>Err(anyhow::Error::from(error)).context("query revoke publisher panicked")},
            result=bundle_publisher=>match result {Ok(Ok(()))=>anyhow::bail!("authorization policy bundle publisher stopped"),Ok(Err(error))=>Err(error).context("authorization policy bundle publisher failed"),Err(error)=>Err(anyhow::Error::from(error)).context("authorization policy bundle publisher panicked")},
            result=bundle_acknowledgement_consumer=>match result {Ok(Ok(()))=>anyhow::bail!("authorization policy bundle acknowledgement consumer stopped"),Ok(Err(error))=>Err(error).context("authorization policy bundle acknowledgement consumer failed"),Err(error)=>Err(anyhow::Error::from(error)).context("authorization policy bundle acknowledgement consumer panicked")},
            result = &mut server => result.context("serve AuthZ HTTP"),
            result = async { match https_server.as_mut() { Some(server) => server.await.map_err(anyhow::Error::from)?.context("serve AuthZ HTTPS"), None => std::future::pending().await } } => result,
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
        };
        if let Some(server) = https_server {
            server.abort();
        }
        result
    }
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (pool, config);
        if let Some(server) = https_server {
            let http_server = axum::serve(listener, build_router(state, transport)?);
            tokio::select! {
                result = http_server => result.context("serve AuthZ HTTP"),
                result = server => result.map_err(anyhow::Error::from)?.context("serve AuthZ HTTPS"),
            }
        } else {
            axum::serve(listener, build_router(state, transport)?)
                .await
                .context("serve AuthZ HTTP")
        }
    }
}

fn load_https_server_config(config: &ServerConfig) -> Result<Arc<rustls::ServerConfig>> {
    use rustls::server::AllowAnyAuthenticatedClient;
    use std::{fs::File, io::BufReader};
    let certificate_path = config
        .https_certificate_path
        .as_deref()
        .context("HTTPS certificate path is missing")?;
    let key_path = config
        .https_private_key_path
        .as_deref()
        .context("HTTPS private key path is missing")?;
    let ca_path = config
        .https_client_ca_path
        .as_deref()
        .context("HTTPS client CA path is missing")?;
    let certificates = rustls_pemfile::certs(&mut BufReader::new(File::open(certificate_path)?))?
        .into_iter()
        .map(Certificate)
        .collect::<Vec<_>>();
    if certificates.is_empty() {
        anyhow::bail!("HTTPS certificate file contains no certificates");
    }
    let mut key_reader = BufReader::new(File::open(key_path)?);
    let private_key = rustls_pemfile::read_all(&mut key_reader)?
        .into_iter()
        .find_map(|item| match item {
            rustls_pemfile::Item::PKCS8Key(key)
            | rustls_pemfile::Item::RSAKey(key)
            | rustls_pemfile::Item::ECKey(key) => Some(PrivateKey(key)),
            _ => None,
        })
        .context("HTTPS private key file contains no supported key")?;
    let mut roots = rustls::RootCertStore::empty();
    let ca_certificates = rustls_pemfile::certs(&mut BufReader::new(File::open(ca_path)?))?;
    if ca_certificates.is_empty() {
        anyhow::bail!("HTTPS client CA file contains no certificates");
    }
    for certificate in ca_certificates {
        roots
            .add(&Certificate(certificate))
            .context("add HTTPS client CA certificate")?;
    }
    let verifier = AllowAnyAuthenticatedClient::new(roots);
    let mut server = rustls::ServerConfig::builder()
        .with_safe_defaults()
        .with_client_cert_verifier(Arc::new(verifier))
        .with_single_cert(certificates, private_key)
        .context("build mutual-TLS Authz server configuration")?;
    server.alpn_protocols = vec![b"http/1.1".to_vec()];
    Ok(Arc::new(server))
}

async fn serve_https(
    listener: tokio::net::TcpListener,
    router: Router,
    config: Arc<rustls::ServerConfig>,
) -> Result<()> {
    let acceptor = tokio_rustls::TlsAcceptor::from(config);
    loop {
        let (stream, _) = listener.accept().await?;
        let acceptor = acceptor.clone();
        let router = router.clone();
        tokio::spawn(async move {
            let Ok(stream) = acceptor.accept(stream).await else {
                return;
            };
            let service = service_fn(move |request: Request<Incoming>| {
                let router = router.clone();
                async move { router.oneshot(request.map(axum::body::Body::new)).await }
            });
            if let Err(error) = hyper::server::conn::http1::Builder::new()
                .serve_connection(TokioIo::new(stream), service)
                .await
            {
                tracing::debug!(%error, "Authz HTTPS connection ended");
            }
        });
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
