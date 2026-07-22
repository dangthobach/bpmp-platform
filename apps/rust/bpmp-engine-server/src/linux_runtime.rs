use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result};
use base64::Engine as _;
use bpmp_adapter_identity_jwt::{JwtIdentityVerifier, JwtVerificationConfig};
use bpmp_adapter_policy_bundle::VerifiedAuthorizationStore;
use bpmp_adapter_rocksdb::{RocksDbConfig, RocksDbWorkflowStore};
use bpmp_adapter_wasmtime::{WasmExecutionLimits, WasmWorkerConfig, WasmtimeWorker};
use bpmp_authz_contracts::authorization::v1::{SignedActorContext, SignedWorkloadContext};
use bpmp_authz_contracts::{
    AUTHORIZATION_PROOF_SCHEMA_VERSION, ActorProofCodec, AuthorizationArtifactLimits,
    AuthorizationKeyring, AuthorizationProofLimits, Ed25519Signer as AuthorizationSigner,
    WorkloadProofCodec,
};
use bpmp_contracts::Ed25519Verifier;
use bpmp_domain_core::{
    BoundaryRuntimePolicy, Command, CommandId, ConfigId, ConfigVersion, ConfigurationScope,
    CorrelationId, EnginePolicy, IdempotencyKey, InstanceId, KeyScope, LocalWasmPolicy,
    PolicyVersion, ResolvedConfigSnapshot, RetryPolicy, ScopeKind, TenantId, WorkflowType,
    WorkflowVersion,
};
use bpmp_engine::{
    ActorProofKind, AuthoritativeCommandHandler, AuthorizedCommand, BoundaryDispatchCredentials,
    BoundaryDispatchCredentialsPort, BoundaryDispatchRequest, BoundaryRuntime,
    BoundaryRuntimeError, ConfigurationLookup, ConfigurationProviderPort,
    EmbeddedAuthorizationProvider, Engine, EngineBoundaryCommandDispatcher,
    GrpcEngineCommandService, GrpcTransportConfig, LocalTaskActivation,
    LocalTaskCompletionDispatcherPort, LocalTaskExecutionOutcome, LocalTaskExecutorPort,
    LocalTaskKind, LocalTaskRuntime, LocalTaskRuntimeError, OutboxBoundaryEventSource, OutboxError,
    OutboxPublisher, OutboxPublisherConfig, OutboxRecord, OutboxStorePort, PublishAcknowledgement,
    RetryDelayPort, RuntimeRegistry, SystemClock, WirLoader, WorkflowDefinitionProviderPort,
};
use bpmp_payload_crypto::{AesGcmPayloadCrypto, CryptoError, DataKeyResolverPort, ResolvedDataKey};
use jsonwebtoken::Algorithm;
use rdkafka::ClientConfig;
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::util::Timeout;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use tokio::signal;
use tonic::transport::{Certificate, Identity, Server, ServerTlsConfig};
use tracing::{error, info, warn};
use zeroize::Zeroizing;

use crate::config::{
    BoundaryWorkerConfig, KafkaConfig, PayloadKeyConfig, RuntimeConfig, VerificationKeyConfig,
    WasmModuleConfig,
};

pub async fn run(path: PathBuf) -> Result<()> {
    init_tracing();
    let config = RuntimeConfig::load(&path)?;
    let registry = Arc::new(load_runtime_registry(&config)?);
    let authorization = Arc::new(load_authorization(&config)?);
    let crypto = AesGcmPayloadCrypto::new(FileDataKeyResolver::load(&config.payload_keys)?);
    let rocks = &config.rocksdb;
    let store = Arc::new(RocksDbWorkflowStore::open(
        RocksDbConfig {
            path: config.data_path.clone(),
            max_open_files: rocks.max_open_files,
            write_buffer_size_bytes: rocks.write_buffer_size_bytes,
            max_background_jobs: rocks.max_background_jobs,
            max_replay_events: rocks.max_replay_events,
        },
        crypto,
    )?);

    let command_engine = Engine::new(registry.clone(), store.clone(), authorization.clone());
    let handler = AuthoritativeCommandHandler::new(command_engine, registry.clone());
    let grpc = GrpcEngineCommandService::new(handler).into_server(GrpcTransportConfig::new(
        config.grpc.max_decoding_bytes,
        config.grpc.max_encoding_bytes,
    )?);

    let dispatch_credentials = DispatchCredentialProvider::load(&config, registry.clone())?;
    let scheduler_engine = Engine::new(registry.clone(), store.clone(), authorization.clone());
    let dispatcher = EngineBoundaryCommandDispatcher::new(
        scheduler_engine,
        registry.clone(),
        dispatch_credentials,
    );
    let boundary = Arc::new(BoundaryRuntime::new(
        OutboxBoundaryEventSource::new(store.clone()),
        store.clone(),
        dispatcher,
        SystemClock,
        boundary_policy(&config.workers.boundary),
    ));
    let local_task_credentials = DispatchCredentialProvider::load(&config, registry.clone())?;
    let local_task_engine = Engine::new(registry.clone(), store.clone(), authorization.clone());
    let local_tasks = Arc::new(LocalTaskRuntime::new(
        store.clone(),
        store.clone(),
        ConfiguredWasmExecutor::load(registry.clone(), &config.wasm_modules)?,
        LocalTaskCompletionDispatcher {
            engine: local_task_engine,
            definitions: registry.clone(),
            credentials: local_task_credentials,
        },
        config.workers.local_task_batch_size,
    )?);
    let outbox = Arc::new(OutboxPublisher::new(
        store.clone(),
        KafkaPublisher::new(&config.kafka)?,
        ThreadDelay,
        OutboxPublisherConfig::new(
            config.workers.outbox_batch_size,
            config.workers.outbox_max_attempts,
            config.workers.outbox_initial_retry_ms,
            config.workers.outbox_max_retry_ms,
            config.workers.outbox_retry_multiplier_millis,
        )?,
    ));

    let interval = config.poll_interval();
    let outbox_store = store;
    let outbox_worker = tokio::spawn(async move {
        loop {
            let checkpoint = match outbox_store.publisher_checkpoint() {
                Ok(value) => value,
                Err(error) => {
                    error!(%error, "read outbox checkpoint");
                    tokio::time::sleep(interval).await;
                    continue;
                }
            };
            let publisher = outbox.clone();
            match tokio::task::spawn_blocking(move || publisher.run_once(checkpoint)).await {
                Ok(Ok(outcome)) if outcome.published > 0 => {
                    info!(
                        published = outcome.published,
                        checkpoint = outcome.checkpoint,
                        "published engine outbox batch"
                    );
                }
                Ok(Ok(_)) => {}
                Ok(Err(error)) => error!(%error, "publish engine outbox batch"),
                Err(error) => error!(%error, "outbox worker join failure"),
            }
            tokio::time::sleep(interval).await;
        }
    });

    let boundary_worker = tokio::spawn(async move {
        loop {
            let runtime = boundary.clone();
            match tokio::task::spawn_blocking(move || {
                runtime.project_once()?;
                runtime.dispatch_due_timers_once()?;
                runtime.dispatch_correlations_once()
            })
            .await
            {
                Ok(Ok(_)) => {}
                Ok(Err(error)) => error!(%error, "run boundary scheduler batch"),
                Err(error) => error!(%error, "boundary worker join failure"),
            }
            tokio::time::sleep(interval).await;
        }
    });

    let local_task_worker = tokio::spawn(async move {
        loop {
            let runtime = local_tasks.clone();
            match tokio::task::spawn_blocking(move || runtime.run_once()).await {
                Ok(Ok(outcome)) if outcome.executed > 0 => info!(
                    executed = outcome.executed,
                    checkpoint = outcome.checkpoint,
                    "executed local task batch"
                ),
                Ok(Ok(_)) => {}
                Ok(Err(error)) => error!(%error, "execute local task batch"),
                Err(error) => error!(%error, "local task worker join failure"),
            }
            tokio::time::sleep(interval).await;
        }
    });

    info!(listen_addr = %config.listen_addr, "starting bpmp-engine");
    let tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(
            fs::read(&config.tls.server_certificate)?,
            fs::read(&config.tls.server_private_key)?,
        ))
        .client_ca_root(Certificate::from_pem(fs::read(&config.tls.client_ca)?));
    let result = Server::builder()
        .tls_config(tls)?
        .add_service(grpc)
        .serve_with_shutdown(config.listen_addr, shutdown())
        .await;
    outbox_worker.abort();
    boundary_worker.abort();
    local_task_worker.abort();
    result.context("serve engine gRPC")
}

struct ConfiguredWasmExecutor {
    registry: Arc<RuntimeRegistry>,
    modules: BTreeMap<(String, String), Vec<u8>>,
    service_bindings: BTreeMap<String, (String, String)>,
}

impl ConfiguredWasmExecutor {
    fn load(registry: Arc<RuntimeRegistry>, configured: &[WasmModuleConfig]) -> Result<Self> {
        let mut modules = BTreeMap::new();
        let mut service_bindings = BTreeMap::new();
        for module in configured {
            let key = (
                module.implementation_ref.clone(),
                module.implementation_version.clone(),
            );
            let bytes = fs::read(&module.path)?;
            verify_module_digest(&bytes, &module.implementation_version)?;
            if modules.insert(key.clone(), bytes).is_some() {
                anyhow::bail!("duplicate WASM module registry entry");
            }
            for task_type in &module.service_task_types {
                if service_bindings
                    .insert(task_type.clone(), key.clone())
                    .is_some()
                {
                    anyhow::bail!("duplicate local service task binding for {task_type}");
                }
            }
        }
        Ok(Self {
            registry,
            modules,
            service_bindings,
        })
    }
}

fn verify_module_digest(bytes: &[u8], version: &str) -> Result<()> {
    let expected = version
        .strip_prefix("sha256:")
        .context("WASM implementation version must use sha256:<digest>")?;
    let actual = Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    if actual != expected.to_ascii_lowercase() {
        anyhow::bail!("WASM module digest does not match implementation_version");
    }
    Ok(())
}

impl LocalTaskExecutorPort for ConfiguredWasmExecutor {
    fn execute(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<LocalTaskExecutionOutcome, LocalTaskRuntimeError> {
        let module_key = match activation.kind {
            LocalTaskKind::Service => match self.service_bindings.get(&activation.task_type) {
                Some(key) => key.clone(),
                None => return Ok(LocalTaskExecutionOutcome::NotHandled),
            },
            LocalTaskKind::Script => (
                activation.implementation_ref.clone(),
                activation.implementation_version.clone(),
            ),
        };
        let module = self.modules.get(&module_key).ok_or_else(|| {
            LocalTaskRuntimeError::Execution("pinned WASM module is not installed".into())
        })?;
        let configuration = ConfigurationProviderPort::resolve(
            &*self.registry,
            &ConfigurationLookup {
                tenant_id: activation.tenant_id.clone(),
                workflow_type: activation.workflow_type.clone(),
                workflow_version: activation.workflow_version.clone(),
            },
        )
        .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        let worker_config = WasmWorkerConfig::try_from(&configuration.engine.local_wasm)
            .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        let limits = WasmExecutionLimits::try_from(&configuration.engine.local_wasm)
            .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        let worker = WasmtimeWorker::new(&worker_config)
            .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        let compiled = worker
            .compile(module, &limits)
            .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        let input = serde_json::to_vec(&serde_json::json!({
            "tenant_id": activation.tenant_id.as_str(),
            "instance_id": activation.instance_id,
            "node_id": activation.node_id.as_str(),
            "activation_event_id": activation.event_id,
        }))
        .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        let _ = worker
            .execute(&compiled, &input, &limits)
            .map_err(|error| LocalTaskRuntimeError::Execution(error.to_string()))?;
        Ok(LocalTaskExecutionOutcome::Completed)
    }
}

struct LocalTaskCompletionDispatcher<C, S, A> {
    engine: Engine<C, S, A>,
    definitions: Arc<RuntimeRegistry>,
    credentials: DispatchCredentialProvider,
}

impl<C, S, A> LocalTaskCompletionDispatcherPort for LocalTaskCompletionDispatcher<C, S, A>
where
    C: ConfigurationProviderPort,
    S: bpmp_engine::WorkflowStorePort,
    A: bpmp_engine::AuthorizationProviderPort,
{
    fn dispatch_completion(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<(), LocalTaskRuntimeError> {
        let definition = WorkflowDefinitionProviderPort::resolve(
            &*self.definitions,
            &activation.tenant_id,
            &activation.workflow_type,
            &activation.workflow_version,
        )
        .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?;
        let identity = format!("local-task:{}", activation.event_id);
        let command_id = CommandId::new(identity.clone())
            .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?;
        let request = BoundaryDispatchRequest {
            tenant_id: activation.tenant_id.clone(),
            instance_id: InstanceId::new(activation.instance_id.clone())
                .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?,
            command_id: command_id.clone(),
            idempotency_key: IdempotencyKey::new(identity)
                .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?,
            correlation_id: CorrelationId::new(format!("local-task:{}", activation.event_id))
                .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?,
            command: match activation.kind {
                LocalTaskKind::Service => Command::CompleteServiceTask {
                    node_id: activation.node_id.clone(),
                    occurred_at_epoch_ms: activation.occurred_at_epoch_ms,
                },
                LocalTaskKind::Script => Command::CompleteScriptTask {
                    node_id: activation.node_id.clone(),
                    occurred_at_epoch_ms: activation.occurred_at_epoch_ms,
                },
            },
            source: bpmp_engine::BoundaryDispatchSource::Timer,
            occurred_at_epoch_ms: activation.occurred_at_epoch_ms,
            workflow_type: activation.workflow_type.clone(),
            workflow_version: activation.workflow_version.clone(),
            authorization_context_ref: None,
        };
        let credentials = self
            .credentials
            .resolve(&request)
            .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?;
        self.engine
            .handle(
                &definition,
                AuthorizedCommand {
                    tenant_id: activation.tenant_id.clone(),
                    instance_id: request.instance_id,
                    command_id,
                    idempotency_key: request.idempotency_key,
                    correlation_id: request.correlation_id,
                    evaluated_at_epoch_ms: activation.occurred_at_epoch_ms,
                    actor_proof: credentials.actor_proof,
                    actor_proof_kind: ActorProofKind::SignedInternalContext,
                    workload_proof: credentials.workload_proof,
                    encryption_key_scope: credentials.encryption_key_scope,
                    variables: BTreeMap::new(),
                    command: request.command,
                },
            )
            .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?;
        Ok(())
    }
}

async fn shutdown() {
    if let Err(error) = signal::ctrl_c().await {
        warn!(%error, "install shutdown signal");
    }
}

fn init_tracing() {
    let subscriber = tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .finish();
    let _ = tracing::subscriber::set_global_default(subscriber);
}

fn load_runtime_registry(config: &RuntimeConfig) -> Result<RuntimeRegistry> {
    let verifier = Ed25519Verifier::from_bytes(&read_exact_32(&config.wir.verification_key)?)?;
    let mut definitions = BTreeMap::new();
    for path in &config.wir.artifacts {
        let bytes =
            fs::read(path).with_context(|| format!("read WIR artifact {}", path.display()))?;
        let definition = WirLoader::load(&bytes, &verifier)
            .with_context(|| format!("load WIR artifact {}", path.display()))?;
        definitions.insert(
            (
                definition.tenant_id.clone(),
                definition.workflow_type.clone(),
                definition.workflow_version.clone(),
            ),
            definition,
        );
    }
    let registry = RuntimeRegistry::default();
    for path in &config.wir.configurations {
        let published: PublishedConfiguration = read_json(path)?;
        let scope = (
            TenantId::new(published.tenant_id)?,
            WorkflowType::new(published.workflow_type)?,
            WorkflowVersion::new(published.workflow_version)?,
        );
        let definition = definitions
            .remove(&scope)
            .with_context(|| format!("configuration {} has no matching WIR", path.display()))?;
        registry.install(definition, published.snapshot.into_domain()?)?;
    }
    if !definitions.is_empty() {
        anyhow::bail!("one or more WIR artifacts have no matching published configuration");
    }
    Ok(registry)
}

fn load_authorization(config: &RuntimeConfig) -> Result<EmbeddedAuthorizationProvider> {
    let actor_keys = load_keyring(&config.authorization.actor_keys)?;
    let workload_keys = load_keyring(&config.authorization.workload_keys)?;
    let policy_keys = load_keyring(&config.authorization.policy_keys)?;
    let proof_limits = AuthorizationProofLimits::new(
        config.authorization.max_proof_bytes,
        config.authorization.max_roles,
        config.authorization.max_capabilities,
    )?;
    let policy_limits = AuthorizationArtifactLimits::new(
        config.authorization.max_policy_bytes,
        config.authorization.max_policy_grants,
    )?;
    let policies = VerifiedAuthorizationStore::new(policy_keys, policy_limits);
    for path in &config.authorization.policy_bundles {
        policies
            .install_signed_bundle(&fs::read(path)?)
            .with_context(|| format!("install policy bundle {}", path.display()))?;
    }
    let algorithms = config
        .authorization
        .jwt_algorithms
        .iter()
        .map(|value| match value.as_str() {
            "RS256" => Ok(Algorithm::RS256),
            "EdDSA" => Ok(Algorithm::EdDSA),
            _ => anyhow::bail!("unsupported JWT algorithm {value}"),
        })
        .collect::<Result<Vec<_>>>()?;
    let jwt = JwtIdentityVerifier::new(
        JwtVerificationConfig {
            issuers: config
                .authorization
                .jwt_issuers
                .iter()
                .cloned()
                .collect::<BTreeSet<_>>(),
            audiences: config
                .authorization
                .jwt_audiences
                .iter()
                .cloned()
                .collect::<BTreeSet<_>>(),
            allowed_algorithms: algorithms,
            max_token_bytes: config.authorization.max_proof_bytes,
            max_jwks_keys: config.authorization.max_jwks_keys,
            max_roles: config.authorization.max_roles,
            max_capabilities: config.authorization.max_capabilities,
            clock_skew_seconds: config.authorization.clock_skew_seconds,
        },
        &fs::read(&config.authorization.jwks)?,
    )?;
    Ok(
        EmbeddedAuthorizationProvider::new(actor_keys, workload_keys, proof_limits, policies)
            .with_jwt_verifier(jwt),
    )
}

fn load_keyring(keys: &[VerificationKeyConfig]) -> Result<AuthorizationKeyring> {
    let mut keyring = AuthorizationKeyring::new();
    for key in keys {
        keyring.insert(key.key_id.clone(), &read_exact_32(&key.path)?)?;
    }
    Ok(keyring)
}

fn read_exact_32(path: &Path) -> Result<[u8; 32]> {
    fs::read(path)?
        .try_into()
        .map_err(|_| anyhow::anyhow!("{} must contain exactly 32 bytes", path.display()))
}

fn read_json<T: for<'de> Deserialize<'de>>(path: &Path) -> Result<T> {
    serde_json::from_slice(&fs::read(path)?).with_context(|| format!("decode {}", path.display()))
}

#[derive(Clone)]
struct FileDataKeyResolver {
    current: BTreeMap<KeyScope, (String, u64)>,
    keys: BTreeMap<(KeyScope, String, u64), [u8; 32]>,
}

impl FileDataKeyResolver {
    fn load(entries: &[PayloadKeyConfig]) -> Result<Self> {
        let mut current = BTreeMap::new();
        let mut keys = BTreeMap::new();
        for entry in entries {
            let scope = KeyScope::new(entry.key_scope.clone())?;
            let key = read_exact_32(&entry.path)?;
            if keys
                .insert(
                    (scope.clone(), entry.key_version.clone(), entry.key_epoch),
                    key,
                )
                .is_some()
            {
                anyhow::bail!("duplicate payload key version for {}", scope.as_str());
            }
            match current.get(&scope) {
                Some((_, epoch)) if *epoch >= entry.key_epoch => {}
                _ => {
                    current.insert(scope, (entry.key_version.clone(), entry.key_epoch));
                }
            }
        }
        Ok(Self { current, keys })
    }
}

impl DataKeyResolverPort for FileDataKeyResolver {
    fn resolve_for_encrypt(&self, scope: &KeyScope) -> Result<ResolvedDataKey, CryptoError> {
        let (version, epoch) = self.current.get(scope).ok_or(CryptoError::KeyUnavailable)?;
        self.resolve_for_decrypt(scope, version, *epoch)
    }

    fn resolve_for_decrypt(
        &self,
        scope: &KeyScope,
        version: &str,
        epoch: u64,
    ) -> Result<ResolvedDataKey, CryptoError> {
        let bytes = self
            .keys
            .get(&(scope.clone(), version.to_owned(), epoch))
            .ok_or(CryptoError::KeyUnavailable)?;
        Ok(ResolvedDataKey {
            key_scope: scope.clone(),
            key_version: version.to_owned(),
            key_epoch: epoch,
            key_bytes: Zeroizing::new(*bytes),
        })
    }
}

struct DispatchCredentialProvider {
    registry: Arc<RuntimeRegistry>,
    actor_signer: AuthorizationSigner,
    workload_signer: AuthorizationSigner,
    actor_key_id: String,
    workload_key_id: String,
    actor_id: String,
    workload_id: String,
    roles: Vec<String>,
    capabilities: Vec<String>,
    proof_ttl_ms: u64,
    limits: AuthorizationProofLimits,
}

impl DispatchCredentialProvider {
    fn load(config: &RuntimeConfig, registry: Arc<RuntimeRegistry>) -> Result<Self> {
        let dispatch = &config.authorization.internal_dispatch;
        Ok(Self {
            registry,
            actor_signer: AuthorizationSigner::from_bytes(&read_exact_32(
                &dispatch.actor_signing_key,
            )?),
            workload_signer: AuthorizationSigner::from_bytes(&read_exact_32(
                &dispatch.workload_signing_key,
            )?),
            actor_key_id: dispatch.actor_signing_key_id.clone(),
            workload_key_id: dispatch.workload_signing_key_id.clone(),
            actor_id: dispatch.actor_id.clone(),
            workload_id: dispatch.workload_id.clone(),
            roles: dispatch.roles.clone(),
            capabilities: dispatch.capabilities.clone(),
            proof_ttl_ms: dispatch.proof_ttl_ms,
            limits: AuthorizationProofLimits::new(
                config.authorization.max_proof_bytes,
                config.authorization.max_roles,
                config.authorization.max_capabilities,
            )?,
        })
    }
}

impl BoundaryDispatchCredentialsPort for DispatchCredentialProvider {
    fn resolve(
        &self,
        request: &BoundaryDispatchRequest,
    ) -> Result<BoundaryDispatchCredentials, BoundaryRuntimeError> {
        let expires = request
            .occurred_at_epoch_ms
            .checked_add(self.proof_ttl_ms)
            .ok_or(BoundaryRuntimeError::ClockOverflow)?;
        let actor = ActorProofCodec::seal(
            SignedActorContext {
                schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
                tenant_id: request.tenant_id.as_str().to_owned(),
                actor_id: self.actor_id.clone(),
                roles: self.roles.clone(),
                capabilities: self.capabilities.clone(),
                revoke_epoch: 0,
                issued_at_epoch_ms: request.occurred_at_epoch_ms,
                expires_at_epoch_ms: expires,
                audience_workload_id: self.workload_id.clone(),
                command_id: request.command_id.as_str().to_owned(),
                signing_key_id: String::new(),
                content_hash: Vec::new(),
                signature: Vec::new(),
            },
            &self.actor_key_id,
            &self.actor_signer,
            self.limits,
        )
        .map_err(|error| BoundaryRuntimeError::Dispatch(error.to_string()))?;
        let workload = WorkloadProofCodec::seal(
            SignedWorkloadContext {
                schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
                tenant_id: request.tenant_id.as_str().to_owned(),
                workload_id: self.workload_id.clone(),
                command_id: request.command_id.as_str().to_owned(),
                issued_at_epoch_ms: request.occurred_at_epoch_ms,
                expires_at_epoch_ms: expires,
                signing_key_id: String::new(),
                content_hash: Vec::new(),
                signature: Vec::new(),
            },
            &self.workload_key_id,
            &self.workload_signer,
            self.limits,
        )
        .map_err(|error| BoundaryRuntimeError::Dispatch(error.to_string()))?;
        Ok(BoundaryDispatchCredentials {
            actor_proof: actor,
            workload_proof: workload,
            encryption_key_scope: ConfigurationProviderPort::resolve(
                &*self.registry,
                &ConfigurationLookup {
                    tenant_id: request.tenant_id.clone(),
                    workflow_type: request.workflow_type.clone(),
                    workflow_version: request.workflow_version.clone(),
                },
            )
            .map_err(|error| BoundaryRuntimeError::DefinitionUnavailable(error.to_string()))?
            .engine
            .event_payload_key_scope,
        })
    }
}

struct KafkaPublisher {
    producer: FutureProducer,
    topic: String,
    timeout: Duration,
}

impl KafkaPublisher {
    fn new(config: &KafkaConfig) -> Result<Self> {
        let producer = ClientConfig::new()
            .set("bootstrap.servers", config.brokers.join(","))
            .set("client.id", &config.client_id)
            .set("enable.idempotence", "true")
            .set("acks", "all")
            .set("message.timeout.ms", config.message_timeout_ms.to_string())
            .create()?;
        Ok(Self {
            producer,
            topic: config.topic.clone(),
            timeout: Duration::from_millis(config.message_timeout_ms),
        })
    }
}

impl bpmp_engine::IntegrationEventPublisherPort for KafkaPublisher {
    fn publish(&self, record: &OutboxRecord) -> Result<PublishAcknowledgement, OutboxError> {
        let delivery = self.producer.send(
            FutureRecord::to(&self.topic)
                .key(&record.instance_id)
                .payload(&record.payload)
                .headers(
                    rdkafka::message::OwnedHeaders::new()
                        .insert(rdkafka::message::Header {
                            key: "bpmp-event-id",
                            value: Some(record.event_id.as_bytes()),
                        })
                        .insert(rdkafka::message::Header {
                            key: "bpmp-tenant-id",
                            value: Some(record.tenant_id.as_bytes()),
                        }),
                ),
            Timeout::After(self.timeout),
        );
        match futures::executor::block_on(delivery) {
            Ok(_) => Ok(PublishAcknowledgement {
                event_id: record.event_id.clone(),
            }),
            Err((error, _)) => Err(OutboxError::BrokerUnavailable(error.to_string())),
        }
    }
}

#[derive(Clone, Copy)]
struct ThreadDelay;

impl RetryDelayPort for ThreadDelay {
    fn wait(&self, delay_ms: u64) {
        std::thread::sleep(Duration::from_millis(delay_ms));
    }
}

fn boundary_policy(config: &BoundaryWorkerConfig) -> BoundaryRuntimePolicy {
    BoundaryRuntimePolicy {
        projection_batch_size: config.projection_batch_size,
        dispatch_batch_size: config.dispatch_batch_size,
        max_dispatch_attempts: config.max_dispatch_attempts,
        retry_delay_ms: config.retry_delay_ms,
        lease_duration_ms: config.lease_duration_ms,
        max_timer_horizon_ms: config.max_timer_horizon_ms,
        max_expression_bytes: config.max_expression_bytes,
        worker_id: config.worker_id.clone(),
        max_signal_id_bytes: config.max_signal_id_bytes,
        max_reference_bytes: config.max_reference_bytes,
        max_subscriptions_per_instance: config.max_subscriptions_per_instance,
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PublishedConfiguration {
    tenant_id: String,
    workflow_type: String,
    workflow_version: String,
    snapshot: ConfigurationSnapshot,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfigurationSnapshot {
    config_id: String,
    config_version: String,
    policy_version: String,
    schema_version: u32,
    content_hash_base64: String,
    scopes: Vec<ConfigurationScopeDto>,
    engine: EnginePolicyDto,
}

impl ConfigurationSnapshot {
    fn into_domain(self) -> Result<ResolvedConfigSnapshot> {
        let hash: [u8; 32] = base64::engine::general_purpose::STANDARD
            .decode(self.content_hash_base64)?
            .try_into()
            .map_err(|_| anyhow::anyhow!("configuration content hash must contain 32 bytes"))?;
        ResolvedConfigSnapshot::new(
            ConfigId::new(self.config_id)?,
            ConfigVersion::new(self.config_version)?,
            PolicyVersion::new(self.policy_version)?,
            self.schema_version,
            self.scopes
                .into_iter()
                .map(ConfigurationScopeDto::into_domain)
                .collect::<Result<Vec<_>>>()?,
            hash,
            self.engine.into_domain()?,
        )
        .map_err(Into::into)
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfigurationScopeDto {
    kind: String,
    reference: String,
}

impl ConfigurationScopeDto {
    fn into_domain(self) -> Result<ConfigurationScope> {
        let kind = match self.kind.as_str() {
            "PLATFORM" => ScopeKind::Platform,
            "ENVIRONMENT" => ScopeKind::Environment,
            "TENANT" => ScopeKind::Tenant,
            "WORKFLOW_TYPE" => ScopeKind::WorkflowType,
            "WORKFLOW_VERSION" => ScopeKind::WorkflowVersion,
            "APPROVED_INSTANCE_OVERRIDE" => ScopeKind::ApprovedInstanceOverride,
            _ => anyhow::bail!("unknown configuration scope {}", self.kind),
        };
        ConfigurationScope::new(kind, self.reference).map_err(Into::into)
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EnginePolicyDto {
    snapshot_interval_events: u32,
    max_events_per_decision: u32,
    max_multi_instance_cardinality: u32,
    default_multi_instance_parallelism: u32,
    command_timeout_ms: u64,
    optimistic_conflict_retry: RetryPolicyDto,
    local_wasm: LocalWasmPolicyDto,
    event_payload_key_scope: String,
    authorization_audit_key_scope: String,
    boundary_runtime: BoundaryWorkerConfig,
}

impl EnginePolicyDto {
    fn into_domain(self) -> Result<EnginePolicy> {
        Ok(EnginePolicy {
            snapshot_interval_events: self.snapshot_interval_events,
            max_events_per_decision: self.max_events_per_decision,
            max_multi_instance_cardinality: self.max_multi_instance_cardinality,
            default_multi_instance_parallelism: self.default_multi_instance_parallelism,
            boundary_runtime: boundary_policy(&self.boundary_runtime),
            command_timeout_ms: self.command_timeout_ms,
            optimistic_conflict_retry: self.optimistic_conflict_retry.into_domain(),
            local_wasm: self.local_wasm.into_domain(),
            event_payload_key_scope: KeyScope::new(self.event_payload_key_scope)?,
            authorization_audit_key_scope: KeyScope::new(self.authorization_audit_key_scope)?,
        })
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RetryPolicyDto {
    max_attempts: u32,
    initial_backoff_ms: u64,
    max_backoff_ms: u64,
    multiplier_millis: u32,
}

impl RetryPolicyDto {
    const fn into_domain(self) -> RetryPolicy {
        RetryPolicy {
            max_attempts: self.max_attempts,
            initial_backoff_ms: self.initial_backoff_ms,
            max_backoff_ms: self.max_backoff_ms,
            multiplier_millis: self.multiplier_millis,
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct LocalWasmPolicyDto {
    max_module_bytes: u64,
    max_input_bytes: u64,
    max_output_bytes: u64,
    max_memory_bytes: u64,
    max_wasm_stack_bytes: u64,
    max_table_elements: u32,
    max_instances: u32,
    max_tables: u32,
    max_memories: u32,
    fuel: u64,
}

impl LocalWasmPolicyDto {
    const fn into_domain(self) -> LocalWasmPolicy {
        LocalWasmPolicy {
            max_module_bytes: self.max_module_bytes,
            max_input_bytes: self.max_input_bytes,
            max_output_bytes: self.max_output_bytes,
            max_memory_bytes: self.max_memory_bytes,
            max_wasm_stack_bytes: self.max_wasm_stack_bytes,
            max_table_elements: self.max_table_elements,
            max_instances: self.max_instances,
            max_tables: self.max_tables,
            max_memories: self.max_memories,
            fuel: self.fuel,
        }
    }
}
