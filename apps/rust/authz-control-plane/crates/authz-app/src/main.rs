//! Binary entry point for `authz-app`.

use std::sync::Arc;
use std::time::Duration;
use std::{fs::File, io::BufReader};

use anyhow::{Context, Result};
use authz_db::{create_pool, DbPoolConfig};

use authz_app::bootstrap::AppContainer;
use authz_app::infrastructure::config::AppConfig;
use authz_app::infrastructure::messaging::{LoggingSink, OutboxWorker};
use authz_app::infrastructure::persistence::run_app_migrations;
use authz_app::presentation::router::{build_public_router, build_router};
use authz_app::telemetry;
use authz_http_middleware::Config as TransportConfig;
use axum::Router;
use hyper::{body::Incoming, service::service_fn, Request};
use hyper_util::rt::TokioIo;
use tokio_rustls::rustls::{self, Certificate, PrivateKey};
use tower::ServiceExt;

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

    let transport = TransportConfig {
        service: cfg.service_name.clone(),
        request_timeout: Duration::from_millis(cfg.server_request_timeout_ms),
        max_body_bytes: cfg.server_max_body_bytes,
        rate_limit_requests_per_second: cfg.rate_limit_requests_per_second,
        rate_limit_burst: cfg.rate_limit_burst,
    };
    let public_router = build_public_router(container.clone(), transport.clone())?;
    let internal_router = build_router(container, transport)?;
    let addr = cfg.socket_addr()?;
    let listener = tokio::net::TcpListener::bind(addr).await?;
    tracing::info!(%addr, "authz-app HTTP listener started");
    if let Some(https_addr) = cfg.https_socket_addr()? {
        let tls_config = load_https_server_config(&cfg)?;
        let https_listener = tokio::net::TcpListener::bind(https_addr).await?;
        tracing::info!(%https_addr, "authz-app mTLS listener started");
        tokio::select! {
            result = axum::serve(listener, public_router) => result?,
            result = serve_https(https_listener, internal_router, tls_config) => result?,
        }
    } else {
        axum::serve(listener, public_router).await?;
    }
    Ok(())
}

fn load_https_server_config(cfg: &AppConfig) -> anyhow::Result<Arc<rustls::ServerConfig>> {
    use rustls::server::AllowAnyAuthenticatedClient;

    let certificate_path = cfg
        .https_certificate_path
        .as_deref()
        .context("HTTPS certificate path is missing")?;
    let key_path = cfg
        .https_private_key_path
        .as_deref()
        .context("HTTPS private key path is missing")?;
    let ca_path = cfg
        .https_client_ca_path
        .as_deref()
        .context("HTTPS client CA path is missing")?;
    let certificates = rustls_pemfile::certs(&mut BufReader::new(File::open(certificate_path)?))?
        .into_iter()
        .map(Certificate)
        .collect::<Vec<_>>();
    anyhow::ensure!(
        !certificates.is_empty(),
        "HTTPS certificate file contains no certificates"
    );
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
    anyhow::ensure!(
        !ca_certificates.is_empty(),
        "HTTPS client CA file contains no certificates"
    );
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
        .context("build authz-app mutual TLS configuration")?;
    server.alpn_protocols = vec![b"http/1.1".to_vec()];
    Ok(Arc::new(server))
}

async fn serve_https(
    listener: tokio::net::TcpListener,
    router: Router,
    config: Arc<rustls::ServerConfig>,
) -> anyhow::Result<()> {
    let acceptor = tokio_rustls::TlsAcceptor::from(config);
    let connection_slots = Arc::new(tokio::sync::Semaphore::new(1024));
    loop {
        let slot = connection_slots
            .clone()
            .acquire_owned()
            .await
            .map_err(|_| anyhow::anyhow!("authz-app HTTPS connection limiter closed"))?;
        let (stream, _) = listener.accept().await?;
        let acceptor = acceptor.clone();
        let router = router.clone();
        tokio::spawn(async move {
            let _slot = slot;
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
                tracing::debug!(%error, "authz-app HTTPS connection ended");
            }
        });
    }
}
