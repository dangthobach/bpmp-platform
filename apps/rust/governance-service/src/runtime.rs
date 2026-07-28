use anyhow::{Context, Result};
use bpmp_contracts::configuration::v1 as configurationv1;
use prost::Message;
use rdkafka::ClientConfig;
use rdkafka::Message as _;
use rdkafka::consumer::{CommitMode, Consumer, StreamConsumer};
use std::path::PathBuf;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::signal;
use tonic::transport::{Certificate, Identity, Server, ServerTlsConfig};
use tracing::{error, info, warn};

use crate::config::{KafkaConfig, RuntimeConfig};
use crate::engine::EngineCluster;
use crate::key_lifecycle::KeyLifecycleWorker;
use crate::policy::PolicyCache;
use crate::service::GovernanceGrpcService;
use crate::store::GovernanceStore;

pub async fn run(path: PathBuf) -> Result<()> {
    init_tracing();
    let config = RuntimeConfig::load(&path)?;
    let store = GovernanceStore::connect(&config.postgres).await?;
    sqlx::query("SELECT 1 FROM governance_approval_requests LIMIT 1")
        .execute(store.pool())
        .await
        .context("governance schema is not initialized")?;
    let policies = PolicyCache::connect(config.configuration_resolver.clone(), &config.tls).await?;
    let engines = EngineCluster::connect(&config.engines, &config.tls).await?;
    let grpc = GovernanceGrpcService::new(store.clone(), engines, policies.clone()).into_server(
        config.grpc.max_decoding_bytes,
        config.grpc.max_encoding_bytes,
    );
    let certificate = tokio::fs::read(&config.tls.server_certificate).await?;
    let private_key = tokio::fs::read(&config.tls.server_private_key).await?;
    let client_ca = tokio::fs::read(&config.tls.client_ca).await?;
    let tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(certificate, private_key))
        .client_ca_root(Certificate::from_pem(client_ca));
    let configuration_worker = tokio::spawn(run_configuration_reloader(
        kafka_consumer(&config.kafka)?,
        config.kafka.configuration_topic.clone(),
        policies.clone(),
    ));
    let lifecycle_worker = tokio::spawn(
        KeyLifecycleWorker::new(
            store.clone(),
            policies,
            config.key_lifecycle.clone(),
            config.worker.clone(),
        )
        .run(),
    );
    let health_worker = tokio::spawn(run_health_server(config.health_addr, store));
    info!(listen_addr = %config.listen_addr, "starting governance-service");
    let reflection = config
        .grpc
        .reflection_enabled
        .then(|| {
            tonic_reflection::server::Builder::configure()
                .register_encoded_file_descriptor_set(bpmp_contracts::PUBLIC_FILE_DESCRIPTOR_SET)
                .with_service_name("bpmp.governance.v1.GovernanceApprovalService")
                .build_v1()
        })
        .transpose()
        .context("build governance gRPC reflection service")?;
    let server = Server::builder()
        .tls_config(tls)?
        .add_service(grpc)
        .add_optional_service(reflection)
        .serve_with_shutdown(config.listen_addr, shutdown());
    tokio::pin!(server);
    let result = tokio::select! {
        value = &mut server => value.context("serve governance gRPC"),
        value = configuration_worker => join_result(value, "configuration reloader"),
        value = lifecycle_worker => join_result(value, "key lifecycle worker"),
        value = health_worker => join_result(value, "health server"),
    };
    result
}

fn join_result(
    value: Result<Result<()>, tokio::task::JoinError>,
    worker: &'static str,
) -> Result<()> {
    match value {
        Ok(Ok(())) => anyhow::bail!("{worker} stopped unexpectedly"),
        Ok(Err(error)) => Err(error).with_context(|| format!("{worker} failed")),
        Err(error) => Err(anyhow::Error::new(error)).with_context(|| format!("{worker} panicked")),
    }
}

fn kafka_consumer(config: &KafkaConfig) -> Result<StreamConsumer> {
    let consumer = ClientConfig::new()
        .set("bootstrap.servers", config.brokers.join(","))
        .set("group.id", &config.consumer_group)
        .set("client.id", &config.client_id)
        .set("enable.auto.commit", "false")
        .set("enable.auto.offset.store", "false")
        .set("auto.offset.reset", "earliest")
        .set("session.timeout.ms", config.session_timeout_ms.to_string())
        .set(
            "fetch.message.max.bytes",
            config.max_message_bytes.to_string(),
        )
        .create()?;
    Ok(consumer)
}

async fn run_configuration_reloader(
    consumer: StreamConsumer,
    topic: String,
    policies: PolicyCache,
) -> Result<()> {
    consumer.subscribe(&[&topic])?;
    loop {
        let message = consumer.recv().await?;
        let payload = message
            .payload()
            .context("configuration publication has no payload")?;
        let event = configurationv1::ConfigurationPublicationEvent::decode(payload)
            .context("decode configuration publication")?;
        if configurationv1::ConfigurationOwner::try_from(event.owner)?
            == configurationv1::ConfigurationOwner::Governance
        {
            for scope in policies
                .scopes()
                .await
                .into_iter()
                .filter(|scope| scope.tenant_id == event.tenant_id)
            {
                policies.refresh(&scope).await?;
            }
        }
        consumer.commit_message(&message, CommitMode::Sync)?;
    }
}

async fn run_health_server(address: std::net::SocketAddr, store: GovernanceStore) -> Result<()> {
    let listener = TcpListener::bind(address).await?;
    loop {
        let (stream, _) = listener.accept().await?;
        let store = store.clone();
        tokio::spawn(async move {
            if let Err(error) = respond_health(stream, store).await {
                warn!(%error, "governance health request failed");
            }
        });
    }
}

async fn respond_health(mut stream: TcpStream, store: GovernanceStore) -> Result<()> {
    let mut request = [0_u8; 1024];
    let length = stream.read(&mut request).await?;
    let first_line = std::str::from_utf8(&request[..length])
        .unwrap_or_default()
        .lines()
        .next()
        .unwrap_or_default();
    let (status, body) = if first_line.starts_with("GET /livez ") {
        ("200 OK", "ok")
    } else if first_line.starts_with("GET /readyz ") {
        match sqlx::query_scalar::<_, i32>("SELECT 1")
            .fetch_one(store.pool())
            .await
        {
            Ok(1) => ("200 OK", "ready"),
            Ok(_) | Err(_) => ("503 Service Unavailable", "not ready"),
        }
    } else {
        ("404 Not Found", "not found")
    };
    let response = format!(
        "HTTP/1.1 {status}\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    stream.write_all(response.as_bytes()).await?;
    stream.shutdown().await?;
    Ok(())
}

async fn shutdown() {
    if let Err(error) = signal::ctrl_c().await {
        error!(%error, "install shutdown signal");
    }
}

fn init_tracing() {
    let subscriber = tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .finish();
    if tracing::subscriber::set_global_default(subscriber).is_err() {
        warn!("tracing subscriber was already installed");
    }
}
