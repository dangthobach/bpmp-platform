use std::collections::{BTreeMap, BTreeSet};
use std::fmt::Write as _;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, RwLock};
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
use bpmp_contracts::configuration::v1 as configurationv1;
use bpmp_domain_core::{
    BoundaryRuntimePolicy, Command, CommandId, ConfigId, ConfigVersion, ConfigurationScope,
    CorrelationId, EnginePolicy, EngineWorkerPolicy, IdempotencyKey, InstanceId, KeyScope,
    LocalWasmPolicy, PolicyVersion, ResolvedConfigSnapshot, RetryPolicy, ScopeKind, TenantId,
    WorkflowType, WorkflowVersion,
};
use bpmp_engine::{
    ActorProofKind, AuthoritativeCommandHandler, AuthorizedCommand, BoundaryDispatchCredentials,
    BoundaryDispatchCredentialsPort, BoundaryDispatchRequest, BoundaryPolicyHandle,
    BoundaryRuntime, BoundaryRuntimeError, ConfigurationLookup, ConfigurationProviderPort,
    EmbeddedAuthorizationProvider, Engine, EngineBoundaryCommandDispatcher,
    GrpcEngineCommandService, GrpcEngineGovernanceService, GrpcTransportConfig,
    LocalTaskActivation, LocalTaskCompletionDispatcherPort, LocalTaskExecutionOutcome,
    LocalTaskExecutorPort, LocalTaskKind, LocalTaskRetryPolicy, LocalTaskRetryPolicyHandle,
    LocalTaskRuntime, LocalTaskRuntimeError, OutboxBoundaryEventSource, OutboxError,
    OutboxPublisher, OutboxPublisherConfig, OutboxRecord, OutboxStorePort, PublishAcknowledgement,
    RetryDelayPort, RetryingLocalTaskExecutor, RuntimeConfigurationUpdate,
    RuntimeGovernancePolicyUpdate, RuntimeRegistry, RuntimeSafePointGate, SafePointCommandHandler,
    SystemClock, WirLoader, WorkflowDefinitionProviderPort,
    configuration_publication_matches_scope,
};
use bpmp_governance_domain::GovernancePolicy;
use bpmp_payload_crypto::{AesGcmPayloadCrypto, CryptoError, DataKeyResolverPort, ResolvedDataKey};
use bpmp_raft_state_machine::{AuthoritativeStateMachine, StateMachineLimits, TypeConfig};
use jsonwebtoken::Algorithm;
use openraft::Raft;
use prost::Message as ProstMessage;
use rdkafka::ClientConfig;
use rdkafka::Message as KafkaMessage;
use rdkafka::consumer::{CommitMode, Consumer, StreamConsumer};
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::util::Timeout;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use tokio::signal;
use tonic::transport::{
    Certificate, Channel, ClientTlsConfig, Endpoint, Identity, Server, ServerTlsConfig,
};
use tracing::{error, info, warn};
use zeroize::Zeroizing;

use crate::config::{
    BoundaryWorkerConfig, ConfigurationResolverConfig, KafkaConfig, PayloadKeyConfig,
    RuntimeConfig, VerificationKeyConfig, WasmModuleConfig,
};
use crate::governance_runtime::AuthoritativeGovernanceHandler;
use crate::raft_runtime::{
    ForwardingCommandHandler, PeerDirectory, RaftPeer, RaftWorkflowStore, TonicRaftNetworkFactory,
    TonicRaftPeerService,
};

#[allow(clippy::too_many_lines)]
pub async fn run(path: PathBuf) -> Result<()> {
    init_tracing();
    let config = RuntimeConfig::load(&path)?;
    let registry = Arc::new(load_runtime_registry(&config).await?);
    let (initial_boundary_policy, initial_worker_policy) = common_runtime_worker_policy(&registry)?;
    let worker_policy = EngineWorkerPolicyHandle::new(initial_worker_policy)?;
    let boundary_policy = BoundaryPolicyHandle::new(initial_boundary_policy);
    let local_task_retry_policy =
        LocalTaskRetryPolicyHandle::new(local_task_retry_policy(&worker_policy.snapshot()?))?;
    let safe_point_gate = RuntimeSafePointGate::default();
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
    let server_certificate = fs::read(&config.tls.server_certificate)?;
    let server_private_key = fs::read(&config.tls.server_private_key)?;
    let client_ca = fs::read(&config.tls.client_ca)?;
    let peer_directory = PeerDirectory::new(
        config.raft.peers.iter().map(|peer| RaftPeer {
            node_id: peer.node_id,
            address: peer.raft_address.clone(),
            tls_domain: peer.tls_domain.clone(),
        }),
        client_ca.clone(),
        server_certificate.clone(),
        server_private_key.clone(),
        Duration::from_millis(config.raft.rpc_timeout_ms),
    )
    .map_err(anyhow::Error::msg)?;
    let raft_config = Arc::new(
        openraft::Config {
            cluster_name: config.raft.cluster_name.clone(),
            heartbeat_interval: config.raft.heartbeat_interval_ms,
            election_timeout_min: config.raft.election_timeout_min_ms,
            election_timeout_max: config.raft.election_timeout_max_ms,
            ..Default::default()
        }
        .validate()?,
    );
    let raft = Raft::<TypeConfig>::new(
        config.raft.node_id,
        raft_config,
        TonicRaftNetworkFactory::new(config.raft.node_id, peer_directory.clone()),
        store.raft_log_storage(),
        AuthoritativeStateMachine::new(
            store.authoritative_state_storage(config.raft.max_snapshot_bytes)?,
            StateMachineLimits {
                max_conditions: config.raft.max_conditions,
                max_mutations: config.raft.max_mutations,
                max_batch_bytes: config.raft.max_batch_bytes,
                append_only_column_families: config.raft.append_only_column_families.clone(),
            },
        )?,
    )
    .await?;
    let raft_store = Arc::new(RaftWorkflowStore::new(
        store.clone(),
        raft.clone(),
        tokio::runtime::Handle::current(),
    ));

    let command_engine = Engine::new(registry.clone(), raft_store.clone(), authorization.clone());
    let local_handler = Arc::new(SafePointCommandHandler::new(
        AuthoritativeCommandHandler::new(command_engine, registry.clone()),
        safe_point_gate.clone(),
    ));
    let grpc = GrpcEngineCommandService::new(ForwardingCommandHandler::new(
        local_handler.clone(),
        peer_directory.clone(),
        tokio::runtime::Handle::current(),
    ))
    .into_server(GrpcTransportConfig::new(
        config.grpc.max_decoding_bytes,
        config.grpc.max_encoding_bytes,
    )?);
    let governance_grpc = GrpcEngineGovernanceService::new(AuthoritativeGovernanceHandler::new(
        store.clone(),
        raft_store.clone(),
        registry.clone(),
    ))
    .into_server(
        config.grpc.max_decoding_bytes,
        config.grpc.max_encoding_bytes,
    );
    let peer_grpc = TonicRaftPeerService::new(raft.clone(), local_handler, peer_directory.clone())
        .into_server(
            config.grpc.max_decoding_bytes,
            config.grpc.max_encoding_bytes,
        );
    let peer_tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(
            server_certificate.clone(),
            server_private_key.clone(),
        ))
        .client_ca_root(Certificate::from_pem(client_ca.clone()));
    let peer_listen_addr = config.raft.peer_listen_addr;
    let peer_server = tokio::spawn(async move {
        Server::builder()
            .tls_config(peer_tls)?
            .add_service(peer_grpc)
            .serve(peer_listen_addr)
            .await
            .context("serve Raft peer gRPC")
    });
    if config.raft.bootstrap && !raft.is_initialized().await? {
        raft.initialize(peer_directory.membership()).await?;
    }

    let dispatch_credentials = DispatchCredentialProvider::load(&config, registry.clone())?;
    let scheduler_engine = Engine::new(registry.clone(), raft_store.clone(), authorization.clone());
    let dispatcher = EngineBoundaryCommandDispatcher::new(
        scheduler_engine,
        registry.clone(),
        dispatch_credentials,
    );
    let boundary = Arc::new(BoundaryRuntime::new_with_handle(
        OutboxBoundaryEventSource::new(store.clone()),
        store.clone(),
        dispatcher,
        SystemClock,
        boundary_policy.clone(),
    ));
    let local_task_credentials = DispatchCredentialProvider::load(&config, registry.clone())?;
    let local_task_engine = Engine::new(registry.clone(), raft_store, authorization.clone());
    let local_tasks = Arc::new(LocalTaskRuntime::new(
        store.clone(),
        store.clone(),
        RetryingLocalTaskExecutor::new_with_handle(
            ConfiguredWasmExecutor::load(registry.clone(), &config.wasm_modules)?,
            ThreadDelay,
            local_task_retry_policy.clone(),
        )?,
        LocalTaskCompletionDispatcher {
            engine: local_task_engine,
            definitions: registry.clone(),
            credentials: local_task_credentials,
        },
        usize::try_from(worker_policy.snapshot()?.local_task_batch_size)
            .context("local task batch size exceeds usize")?,
    )?);
    let initial_outbox_config = outbox_publisher_config(&worker_policy.snapshot()?)?;
    let outbox = Arc::new(OutboxPublisher::new(
        store.clone(),
        KafkaPublisher::new(&config.kafka)?,
        ThreadDelay,
        initial_outbox_config,
    ));

    let local_node_id = config.raft.node_id;
    let outbox_store = store;
    let outbox_raft = raft.clone();
    let outbox_worker_policy = worker_policy.clone();
    let outbox_worker = tokio::spawn(async move {
        loop {
            let policy = match outbox_worker_policy.snapshot() {
                Ok(value) => value,
                Err(error) => {
                    error!(%error, "read outbox worker policy");
                    break;
                }
            };
            let interval = Duration::from_millis(policy.poll_interval_ms);
            if !is_current_leader(&outbox_raft, local_node_id) {
                tokio::time::sleep(interval).await;
                continue;
            }
            let checkpoint = match outbox_store.publisher_checkpoint() {
                Ok(value) => value,
                Err(error) => {
                    error!(%error, "read outbox checkpoint");
                    tokio::time::sleep(interval).await;
                    continue;
                }
            };
            let batch_config = match outbox_publisher_config(&policy) {
                Ok(value) => value,
                Err(error) => {
                    error!(%error, "validate outbox worker policy");
                    break;
                }
            };
            let publisher = outbox.clone();
            match tokio::task::spawn_blocking(move || {
                publisher.run_once_with_config(checkpoint, batch_config)
            })
            .await
            {
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

    let boundary_raft = raft.clone();
    let boundary_safe_point_gate = safe_point_gate.clone();
    let boundary_worker_policy = worker_policy.clone();
    let boundary_worker = tokio::spawn(async move {
        loop {
            let interval = match boundary_worker_policy.snapshot() {
                Ok(policy) => Duration::from_millis(policy.poll_interval_ms),
                Err(error) => {
                    error!(%error, "read boundary worker policy");
                    break;
                }
            };
            if !is_current_leader(&boundary_raft, local_node_id) {
                tokio::time::sleep(interval).await;
                continue;
            }
            let runtime = boundary.clone();
            let gate = boundary_safe_point_gate.clone();
            match tokio::task::spawn_blocking(move || {
                let _permit = gate.enter_work()?;
                runtime.project_once()?;
                runtime.dispatch_due_timers_once()?;
                runtime
                    .dispatch_correlations_once()
                    .map_err(anyhow::Error::from)
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

    let local_task_raft = raft.clone();
    let local_task_safe_point_gate = safe_point_gate.clone();
    let local_task_worker_policy = worker_policy.clone();
    let local_task_worker = tokio::spawn(async move {
        loop {
            let policy = match local_task_worker_policy.snapshot() {
                Ok(value) => value,
                Err(error) => {
                    error!(%error, "read local task worker policy");
                    break;
                }
            };
            let interval = Duration::from_millis(policy.poll_interval_ms);
            let batch_size = match usize::try_from(policy.local_task_batch_size) {
                Ok(value) => value,
                Err(error) => {
                    error!(%error, "local task batch size exceeds usize");
                    break;
                }
            };
            if !is_current_leader(&local_task_raft, local_node_id) {
                tokio::time::sleep(interval).await;
                continue;
            }
            let runtime = local_tasks.clone();
            let gate = local_task_safe_point_gate.clone();
            match tokio::task::spawn_blocking(move || {
                let _permit = gate.enter_work()?;
                runtime
                    .run_once_with_batch_size(batch_size)
                    .map_err(anyhow::Error::from)
            })
            .await
            {
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

    let mut configuration_worker = if let Some(resolver) = config.configuration_resolver.clone() {
        let consumer = configuration_consumer(&config.kafka)?;
        consumer.subscribe(&[&config.kafka.topics.configuration_publications])?;
        let client = connect_configuration_resolver(&config, &resolver).await?;
        let registry = registry.clone();
        let gate = safe_point_gate.clone();
        let handles = RuntimeWorkerPolicyHandles {
            workers: worker_policy,
            boundary: boundary_policy,
            local_task_retry: local_task_retry_policy,
        };
        Some(tokio::spawn(async move {
            run_configuration_reloader(consumer, client, resolver, registry, gate, handles).await
        }))
    } else {
        None
    };

    info!(listen_addr = %config.listen_addr, "starting bpmp-engine");
    let tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(server_certificate, server_private_key))
        .client_ca_root(Certificate::from_pem(client_ca));
    let reflection = config
        .grpc
        .reflection_enabled
        .then(|| {
            tonic_reflection::server::Builder::configure()
                .register_encoded_file_descriptor_set(bpmp_contracts::PUBLIC_FILE_DESCRIPTOR_SET)
                .with_service_name("bpmp.engine.v1.EngineCommandService")
                .with_service_name("bpmp.governance.v1.EngineGovernanceService")
                .build_v1()
        })
        .transpose()
        .context("build engine gRPC reflection service")?;
    let server = Server::builder()
        .tls_config(tls)?
        .add_service(grpc)
        .add_service(governance_grpc)
        .add_optional_service(reflection)
        .serve_with_shutdown(config.listen_addr, shutdown());
    tokio::pin!(server);
    let result = if let Some(worker) = configuration_worker.as_mut() {
        tokio::select! {
            server_result = &mut server => server_result.context("serve engine gRPC"),
            worker_result = worker => match worker_result {
                Ok(Ok(())) => Err(anyhow::anyhow!(
                    "configuration reloader stopped unexpectedly"
                )),
                Ok(Err(error)) => Err(error.context("configuration reloader failed")),
                Err(error) => Err(anyhow::Error::from(error)
                    .context("configuration reloader join failed")),
            },
        }
    } else {
        server.await.context("serve engine gRPC")
    };
    outbox_worker.abort();
    boundary_worker.abort();
    local_task_worker.abort();
    if let Some(worker) = configuration_worker {
        worker.abort();
    }
    peer_server.abort();
    result
}

fn is_current_leader(raft: &Raft<TypeConfig>, local_node_id: u64) -> bool {
    raft.metrics().borrow().current_leader == Some(local_node_id)
}

#[derive(Clone)]
struct EngineWorkerPolicyHandle {
    value: Arc<RwLock<EngineWorkerPolicy>>,
}

impl EngineWorkerPolicyHandle {
    fn new(policy: EngineWorkerPolicy) -> Result<Self> {
        validate_worker_policy(&policy)?;
        Ok(Self {
            value: Arc::new(RwLock::new(policy)),
        })
    }

    fn replace(&self, policy: EngineWorkerPolicy) -> Result<()> {
        validate_worker_policy(&policy)?;
        *self
            .value
            .write()
            .map_err(|_| anyhow::anyhow!("engine worker policy lock is poisoned"))? = policy;
        Ok(())
    }

    fn snapshot(&self) -> Result<EngineWorkerPolicy> {
        self.value
            .read()
            .map(|policy| policy.clone())
            .map_err(|_| anyhow::anyhow!("engine worker policy lock is poisoned"))
    }
}

#[derive(Clone)]
struct RuntimeWorkerPolicyHandles {
    workers: EngineWorkerPolicyHandle,
    boundary: BoundaryPolicyHandle,
    local_task_retry: LocalTaskRetryPolicyHandle,
}

impl RuntimeWorkerPolicyHandles {
    fn replace(&self, boundary: BoundaryRuntimePolicy, workers: EngineWorkerPolicy) -> Result<()> {
        let retry = local_task_retry_policy(&workers);
        validate_worker_policy(&workers)?;
        retry.validate().map_err(anyhow::Error::from)?;
        self.boundary
            .replace(boundary)
            .map_err(anyhow::Error::from)?;
        self.local_task_retry
            .replace(retry)
            .map_err(anyhow::Error::from)?;
        self.workers.replace(workers)
    }
}

fn common_runtime_worker_policy(
    registry: &RuntimeRegistry,
) -> Result<(BoundaryRuntimePolicy, EngineWorkerPolicy)> {
    let mut selected: Option<(BoundaryRuntimePolicy, EngineWorkerPolicy)> = None;
    for scope in registry.installed_scopes()? {
        let configuration = ConfigurationProviderPort::resolve(
            registry,
            &ConfigurationLookup {
                tenant_id: scope.tenant_id,
                workflow_type: scope.workflow_type,
                workflow_version: scope.workflow_version,
            },
        )?;
        let candidate = (
            configuration.engine.boundary_runtime,
            configuration.engine.workers,
        );
        if selected
            .as_ref()
            .is_some_and(|current| current != &candidate)
        {
            anyhow::bail!(
                "engine worker and boundary policies must be identical across installed workflow scopes"
            );
        }
        selected = Some(candidate);
    }
    selected.context("engine has no installed workflow configuration")
}

fn common_runtime_worker_policy_after_updates(
    registry: &RuntimeRegistry,
    updates: &[RuntimeConfigurationUpdate],
) -> Result<Option<(BoundaryRuntimePolicy, EngineWorkerPolicy)>> {
    let mut effective = BTreeMap::new();
    for scope in registry.installed_scopes()? {
        let lookup = ConfigurationLookup {
            tenant_id: scope.tenant_id.clone(),
            workflow_type: scope.workflow_type.clone(),
            workflow_version: scope.workflow_version.clone(),
        };
        if let Ok(configuration) = ConfigurationProviderPort::resolve(registry, &lookup) {
            effective.insert(
                (scope.tenant_id, scope.workflow_type, scope.workflow_version),
                (
                    configuration.engine.boundary_runtime,
                    configuration.engine.workers,
                ),
            );
        }
    }
    for update in updates {
        effective.insert(
            (
                update.tenant_id.clone(),
                update.workflow_type.clone(),
                update.workflow_version.clone(),
            ),
            (
                update.configuration.engine.boundary_runtime.clone(),
                update.configuration.engine.workers.clone(),
            ),
        );
    }
    let mut selected: Option<(BoundaryRuntimePolicy, EngineWorkerPolicy)> = None;
    for candidate in effective.into_values() {
        if selected
            .as_ref()
            .is_some_and(|current| current != &candidate)
        {
            anyhow::bail!(
                "hot engine worker and boundary policies differ across resolved workflow scopes"
            );
        }
        selected = Some(candidate);
    }
    Ok(selected)
}

fn validate_worker_policy(policy: &EngineWorkerPolicy) -> Result<()> {
    outbox_publisher_config(policy)?;
    local_task_retry_policy(policy)
        .validate()
        .map_err(anyhow::Error::from)?;
    if policy.poll_interval_ms == 0 || policy.local_task_batch_size == 0 {
        anyhow::bail!("engine worker policy contains a zero bound");
    }
    Ok(())
}

fn outbox_publisher_config(policy: &EngineWorkerPolicy) -> Result<OutboxPublisherConfig> {
    OutboxPublisherConfig::new(
        usize::try_from(policy.outbox_batch_size).context("outbox batch size exceeds usize")?,
        policy.outbox_retry.max_attempts,
        policy.outbox_retry.initial_backoff_ms,
        policy.outbox_retry.max_backoff_ms,
        policy.outbox_retry.multiplier_millis,
    )
    .map_err(Into::into)
}

const fn local_task_retry_policy(policy: &EngineWorkerPolicy) -> LocalTaskRetryPolicy {
    LocalTaskRetryPolicy {
        max_attempts: policy.local_task_retry.max_attempts,
        initial_backoff_ms: policy.local_task_retry.initial_backoff_ms,
        max_backoff_ms: policy.local_task_retry.max_backoff_ms,
        multiplier_millis: policy.local_task_retry.multiplier_millis,
    }
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
    let actual =
        Sha256::digest(bytes)
            .iter()
            .fold(String::with_capacity(64), |mut output, byte| {
                let _ = write!(output, "{byte:02x}");
                output
            });
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

type ConfigurationResolverClient =
    configurationv1::configuration_resolver_service_client::ConfigurationResolverServiceClient<
        Channel,
    >;

async fn connect_configuration_resolver(
    config: &RuntimeConfig,
    resolver: &ConfigurationResolverConfig,
) -> Result<ConfigurationResolverClient> {
    let certificate = fs::read(&config.tls.server_certificate)?;
    let private_key = fs::read(&config.tls.server_private_key)?;
    let ca = fs::read(&config.tls.client_ca)?;
    let endpoint = Endpoint::from_shared(resolver.endpoint.clone())?
        .connect_timeout(Duration::from_millis(resolver.timeout_ms))
        .timeout(Duration::from_millis(resolver.timeout_ms))
        .tls_config(
            ClientTlsConfig::new()
                .domain_name(resolver.tls_domain.clone())
                .ca_certificate(Certificate::from_pem(ca))
                .identity(Identity::from_pem(certificate, private_key)),
        )?;
    let mut last_error = None;
    let mut channel = None;
    for attempt in 1..=resolver.max_attempts {
        match endpoint.clone().connect().await {
            Ok(connected) => {
                channel = Some(connected);
                break;
            }
            Err(error) => {
                last_error = Some(error);
                if attempt < resolver.max_attempts {
                    tokio::time::sleep(Duration::from_millis(resolver.retry_delay_ms)).await;
                }
            }
        }
    }
    let channel = channel.with_context(|| {
        format!(
            "connect configuration resolver after {} attempts: {}",
            resolver.max_attempts,
            last_error.map_or_else(|| "unknown error".to_owned(), |error| error.to_string())
        )
    })?;
    Ok(ConfigurationResolverClient::new(channel)
        .max_decoding_message_size(resolver.max_decoding_bytes)
        .max_encoding_message_size(resolver.max_encoding_bytes))
}

fn configuration_consumer(config: &KafkaConfig) -> Result<StreamConsumer> {
    let mut client = ClientConfig::new();
    client
        .set("bootstrap.servers", config.brokers.join(","))
        .set("group.id", &config.consumer_groups.configuration_reloader)
        .set("client.id", &config.client_id)
        .set("enable.auto.commit", "false")
        .set("enable.auto.offset.store", "false")
        .set("auto.offset.reset", "earliest")
        .set(
            "session.timeout.ms",
            config.consumer_session_timeout_ms.to_string(),
        )
        .set(
            "fetch.message.max.bytes",
            config.max_message_bytes.to_string(),
        );
    apply_kafka_security(&mut client, config);
    Ok(client.create()?)
}

async fn run_configuration_reloader(
    consumer: StreamConsumer,
    mut client: ConfigurationResolverClient,
    resolver: ConfigurationResolverConfig,
    registry: Arc<RuntimeRegistry>,
    gate: RuntimeSafePointGate,
    worker_handles: RuntimeWorkerPolicyHandles,
) -> Result<()> {
    loop {
        let message = consumer.recv().await?;
        let payload = message
            .payload()
            .context("configuration publication has no payload")?;
        let event = configurationv1::ConfigurationPublicationEvent::decode(payload)
            .context("decode configuration publication")?;
        let changed = reconcile_configuration_event(
            &event,
            &mut client,
            &resolver,
            &registry,
            &gate,
            &worker_handles,
        )
        .await?;
        consumer.commit_message(&message, CommitMode::Sync)?;
        info!(
            event_id = event.event_id,
            event_sequence = event.event_sequence,
            changed,
            "applied configuration publication at engine safe point"
        );
    }
}

async fn reconcile_configuration_event(
    event: &configurationv1::ConfigurationPublicationEvent,
    client: &mut ConfigurationResolverClient,
    resolver: &ConfigurationResolverConfig,
    registry: &RuntimeRegistry,
    gate: &RuntimeSafePointGate,
    worker_handles: &RuntimeWorkerPolicyHandles,
) -> Result<usize> {
    validate_configuration_event(event)?;
    let owner = configurationv1::ConfigurationOwner::try_from(event.owner)?;
    if !matches!(
        owner,
        configurationv1::ConfigurationOwner::Engine
            | configurationv1::ConfigurationOwner::Governance
    ) {
        return Ok(0);
    }
    let scope = event
        .scope
        .as_ref()
        .context("configuration publication has no scope")?;
    let scope_kind = configurationv1::ConfigurationScopeType::try_from(scope.r#type)?;
    let domain_scope_kind = match scope_kind {
        configurationv1::ConfigurationScopeType::Platform => ScopeKind::Platform,
        configurationv1::ConfigurationScopeType::Environment => ScopeKind::Environment,
        configurationv1::ConfigurationScopeType::Tenant => ScopeKind::Tenant,
        configurationv1::ConfigurationScopeType::WorkflowType => ScopeKind::WorkflowType,
        configurationv1::ConfigurationScopeType::WorkflowVersion => ScopeKind::WorkflowVersion,
        configurationv1::ConfigurationScopeType::ApprovedInstanceOverride => {
            ScopeKind::ApprovedInstanceOverride
        }
        configurationv1::ConfigurationScopeType::Unspecified => {
            anyhow::bail!("configuration publication scope is unspecified")
        }
    };
    let candidates = registry
        .installed_scopes()?
        .into_iter()
        .filter(|candidate| {
            configuration_publication_matches_scope(
                candidate,
                &event.tenant_id,
                domain_scope_kind,
                &scope.reference,
                &resolver.platform_reference,
                &resolver.environment_reference,
            )
        })
        .collect::<Vec<_>>();
    if configurationv1::ConfigurationPublicationKind::try_from(event.kind)?
        == configurationv1::ConfigurationPublicationKind::Retired
    {
        return gate.with_safe_point(|safe_point| match owner {
            configurationv1::ConfigurationOwner::Engine => registry
                .retire_configurations(candidates, safe_point)
                .map_err(Into::into),
            configurationv1::ConfigurationOwner::Governance => registry
                .retire_governance_policies(candidates, safe_point)
                .map_err(Into::into),
            _ => unreachable!("owner was filtered above"),
        })?;
    }
    let mut engine_updates = Vec::with_capacity(candidates.len());
    let mut governance_updates = Vec::with_capacity(candidates.len());
    for candidate in candidates {
        let response = client
            .resolve_configuration(configurationv1::ResolveConfigurationRequest {
                tenant_id: candidate.tenant_id.as_str().to_owned(),
                workflow_type: candidate.workflow_type.as_str().to_owned(),
                workflow_version: candidate.workflow_version.as_str().to_owned(),
                platform_reference: resolver.platform_reference.clone(),
                environment_reference: resolver.environment_reference.clone(),
                instance_id: String::new(),
                owner: owner as i32,
            })
            .await
            .context("resolve hot configuration snapshot")?
            .into_inner();
        let snapshot = response
            .snapshot
            .context("configuration resolver returned no hot snapshot")?;
        if snapshot.config_id == event.profile_id && snapshot.ordinal < event.ordinal {
            anyhow::bail!("configuration resolver returned a stale publication ordinal");
        }
        match owner {
            configurationv1::ConfigurationOwner::Engine => {
                engine_updates.push(RuntimeConfigurationUpdate {
                    tenant_id: candidate.tenant_id,
                    workflow_type: candidate.workflow_type,
                    workflow_version: candidate.workflow_version,
                    configuration: configuration_snapshot_from_proto(snapshot)?,
                });
            }
            configurationv1::ConfigurationOwner::Governance => {
                let (policy, config_version, policy_version) =
                    governance_policy_from_proto(snapshot)?;
                governance_updates.push(RuntimeGovernancePolicyUpdate {
                    tenant_id: candidate.tenant_id,
                    workflow_type: candidate.workflow_type,
                    workflow_version: candidate.workflow_version,
                    policy,
                    config_version,
                    policy_version,
                });
            }
            _ => unreachable!("owner was filtered above"),
        }
    }
    let runtime_worker_policy =
        common_runtime_worker_policy_after_updates(registry, &engine_updates)?;
    gate.with_safe_point(|safe_point| -> Result<usize> {
        match owner {
            configurationv1::ConfigurationOwner::Engine => {
                let changed = registry.replace_configurations(engine_updates, safe_point)?;
                if let Some((boundary, workers)) = runtime_worker_policy {
                    worker_handles.replace(boundary, workers)?;
                }
                Ok(changed)
            }
            configurationv1::ConfigurationOwner::Governance => registry
                .replace_governance_policies(governance_updates, safe_point)
                .map_err(Into::into),
            _ => unreachable!("owner was filtered above"),
        }
    })?
}

fn validate_configuration_event(
    event: &configurationv1::ConfigurationPublicationEvent,
) -> Result<()> {
    if event.schema_version != 1
        || event.event_id.trim().is_empty()
        || event.event_sequence == 0
        || event.tenant_id.trim().is_empty()
        || event.profile_id.trim().is_empty()
        || event.version_id.trim().is_empty()
        || event.config_version.trim().is_empty()
        || event.policy_version.trim().is_empty()
        || event.ordinal == 0
        || event.content_hash.len() != 32
        || event.occurred_at_epoch_ms == 0
        || configurationv1::ConfigurationPublicationKind::try_from(event.kind)?
            == configurationv1::ConfigurationPublicationKind::Unspecified
    {
        anyhow::bail!("configuration publication metadata is invalid");
    }
    Ok(())
}

#[allow(clippy::too_many_lines)]
async fn load_runtime_registry(config: &RuntimeConfig) -> Result<RuntimeRegistry> {
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
    if let Some(resolver) = &config.configuration_resolver {
        let mut client = connect_configuration_resolver(config, resolver).await?;
        while let Some(((tenant_id, workflow_type, workflow_version), definition)) =
            definitions.pop_first()
        {
            let response = client
                .resolve_configuration(configurationv1::ResolveConfigurationRequest {
                    tenant_id: tenant_id.as_str().to_owned(),
                    workflow_type: workflow_type.as_str().to_owned(),
                    workflow_version: workflow_version.as_str().to_owned(),
                    platform_reference: resolver.platform_reference.clone(),
                    environment_reference: resolver.environment_reference.clone(),
                    instance_id: String::new(),
                    owner: configurationv1::ConfigurationOwner::Engine as i32,
                })
                .await
                .context("resolve published runtime configuration")?
                .into_inner();
            let snapshot = response
                .snapshot
                .context("configuration resolver returned no snapshot")?;
            registry.install(definition, configuration_snapshot_from_proto(snapshot)?)?;
            let response = client
                .resolve_configuration(configurationv1::ResolveConfigurationRequest {
                    tenant_id: tenant_id.as_str().to_owned(),
                    workflow_type: workflow_type.as_str().to_owned(),
                    workflow_version: workflow_version.as_str().to_owned(),
                    platform_reference: resolver.platform_reference.clone(),
                    environment_reference: resolver.environment_reference.clone(),
                    instance_id: String::new(),
                    owner: configurationv1::ConfigurationOwner::Governance as i32,
                })
                .await
                .context("resolve published governance policy")?
                .into_inner();
            let governance = response
                .snapshot
                .context("configuration resolver returned no governance snapshot")?;
            let (policy, config_version, policy_version) =
                governance_policy_from_proto(governance)?;
            registry.install_governance_policy(RuntimeGovernancePolicyUpdate {
                tenant_id,
                workflow_type,
                workflow_version,
                policy,
                config_version,
                policy_version,
            })?;
        }
    } else {
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
    }
    if !definitions.is_empty() {
        anyhow::bail!("one or more WIR artifacts have no matching published configuration");
    }
    Ok(registry)
}

fn configuration_snapshot_from_proto(
    snapshot: configurationv1::ResolvedConfigurationSnapshot,
) -> Result<ResolvedConfigSnapshot> {
    if configurationv1::ConfigurationOwner::try_from(snapshot.owner)?
        != configurationv1::ConfigurationOwner::Engine
        || snapshot.ordinal == 0
    {
        anyhow::bail!("configuration snapshot owner or ordinal is invalid");
    }
    let hash: [u8; 32] = snapshot
        .content_hash
        .try_into()
        .map_err(|_| anyhow::anyhow!("configuration content hash must contain 32 bytes"))?;
    let scopes = snapshot
        .resolved_scopes
        .into_iter()
        .map(|scope| {
            let kind = match configurationv1::ConfigurationScopeType::try_from(scope.r#type)? {
                configurationv1::ConfigurationScopeType::Platform => ScopeKind::Platform,
                configurationv1::ConfigurationScopeType::Environment => ScopeKind::Environment,
                configurationv1::ConfigurationScopeType::Tenant => ScopeKind::Tenant,
                configurationv1::ConfigurationScopeType::WorkflowType => ScopeKind::WorkflowType,
                configurationv1::ConfigurationScopeType::WorkflowVersion => {
                    ScopeKind::WorkflowVersion
                }
                configurationv1::ConfigurationScopeType::ApprovedInstanceOverride => {
                    ScopeKind::ApprovedInstanceOverride
                }
                configurationv1::ConfigurationScopeType::Unspecified => {
                    anyhow::bail!("configuration scope type is unspecified")
                }
            };
            ConfigurationScope::new(kind, scope.reference).map_err(Into::into)
        })
        .collect::<Result<Vec<_>>>()?;
    let engine = snapshot
        .engine
        .context("configuration resolver returned no engine policy")?;
    let retry = engine
        .optimistic_conflict_retry
        .context("configuration resolver returned no retry policy")?;
    let wasm = engine
        .local_wasm
        .context("configuration resolver returned no local WASM policy")?;
    let boundary = engine
        .boundary_runtime
        .context("configuration resolver returned no boundary policy")?;
    let workers = engine
        .workers
        .context("configuration resolver returned no engine worker policy")?;
    let outbox_retry = workers
        .outbox_retry
        .context("configuration resolver returned no outbox retry policy")?;
    let local_task_retry = workers
        .local_task_retry
        .context("configuration resolver returned no local task retry policy")?;
    ResolvedConfigSnapshot::new(
        ConfigId::new(snapshot.config_id)?,
        ConfigVersion::new(snapshot.config_version)?,
        PolicyVersion::new(snapshot.policy_version)?,
        snapshot.schema_version,
        scopes,
        hash,
        EnginePolicy {
            snapshot_interval_events: engine.snapshot_interval_events,
            max_events_per_decision: engine.max_events_per_decision,
            max_multi_instance_cardinality: engine.max_multi_instance_cardinality,
            default_multi_instance_parallelism: engine.default_multi_instance_parallelism,
            command_timeout_ms: engine.command_timeout_ms,
            optimistic_conflict_retry: RetryPolicy {
                max_attempts: retry.max_attempts,
                initial_backoff_ms: retry.initial_backoff_ms,
                max_backoff_ms: retry.max_backoff_ms,
                multiplier_millis: retry.multiplier_millis,
            },
            local_wasm: LocalWasmPolicy {
                max_module_bytes: wasm.max_module_bytes,
                max_input_bytes: wasm.max_input_bytes,
                max_output_bytes: wasm.max_output_bytes,
                max_memory_bytes: wasm.max_memory_bytes,
                max_wasm_stack_bytes: wasm.max_wasm_stack_bytes,
                max_table_elements: wasm.max_table_elements,
                max_instances: wasm.max_instances,
                max_tables: wasm.max_tables,
                max_memories: wasm.max_memories,
                fuel: wasm.fuel,
            },
            event_payload_key_scope: KeyScope::new(engine.event_payload_key_scope)?,
            authorization_audit_key_scope: KeyScope::new(engine.authorization_audit_key_scope)?,
            boundary_runtime: BoundaryRuntimePolicy {
                projection_batch_size: boundary.projection_batch_size,
                dispatch_batch_size: boundary.dispatch_batch_size,
                max_dispatch_attempts: boundary.max_dispatch_attempts,
                retry_delay_ms: boundary.retry_delay_ms,
                lease_duration_ms: boundary.lease_duration_ms,
                max_timer_horizon_ms: boundary.max_timer_horizon_ms,
                max_expression_bytes: boundary.max_expression_bytes,
                worker_id: boundary.worker_id,
                max_signal_id_bytes: boundary.max_signal_id_bytes,
                max_reference_bytes: boundary.max_reference_bytes,
                max_subscriptions_per_instance: boundary.max_subscriptions_per_instance,
            },
            workers: EngineWorkerPolicy {
                poll_interval_ms: workers.poll_interval_ms,
                outbox_batch_size: workers.outbox_batch_size,
                outbox_retry: RetryPolicy {
                    max_attempts: outbox_retry.max_attempts,
                    initial_backoff_ms: outbox_retry.initial_backoff_ms,
                    max_backoff_ms: outbox_retry.max_backoff_ms,
                    multiplier_millis: outbox_retry.multiplier_millis,
                },
                local_task_batch_size: workers.local_task_batch_size,
                local_task_retry: RetryPolicy {
                    max_attempts: local_task_retry.max_attempts,
                    initial_backoff_ms: local_task_retry.initial_backoff_ms,
                    max_backoff_ms: local_task_retry.max_backoff_ms,
                    multiplier_millis: local_task_retry.multiplier_millis,
                },
            },
        },
    )
    .map_err(Into::into)
}

fn governance_policy_from_proto(
    snapshot: configurationv1::ResolvedConfigurationSnapshot,
) -> Result<(GovernancePolicy, ConfigVersion, PolicyVersion)> {
    if configurationv1::ConfigurationOwner::try_from(snapshot.owner)?
        != configurationv1::ConfigurationOwner::Governance
        || snapshot.ordinal == 0
    {
        anyhow::bail!("governance configuration owner or ordinal is invalid");
    }
    let governance = snapshot
        .governance
        .context("configuration resolver returned no governance policy")?;
    let approval_public_keys = governance
        .approval_keys
        .into_iter()
        .filter(|key| key.enabled)
        .map(|key| {
            let bytes = key
                .ed25519_public_key
                .try_into()
                .map_err(|_| anyhow::anyhow!("governance approval key must contain 32 bytes"))?;
            if key.key_id.trim().is_empty() {
                anyhow::bail!("governance approval key id is empty");
            }
            Ok((key.key_id, bytes))
        })
        .collect::<Result<BTreeMap<_, _>>>()?;
    let policy = GovernancePolicy {
        abort_capability: governance.abort_capability,
        accepted_auth_assurance: governance.accepted_auth_assurance.into_iter().collect(),
        approval_public_keys,
        required_approver_count: u16::try_from(governance.required_approver_count)
            .context("governance approver count exceeds u16")?,
        max_proof_age_ms: governance.fresh_authentication_max_age_ms,
        max_approval_ttl_ms: governance.approval_ttl_ms,
        max_pending_ledger_entries: governance.max_pending_compensations,
    };
    policy
        .validate()
        .map_err(|error| anyhow::anyhow!("invalid governance policy: {error}"))?;
    Ok((
        policy,
        ConfigVersion::new(snapshot.config_version)?,
        PolicyVersion::new(snapshot.policy_version)?,
    ))
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
        let mut client = ClientConfig::new();
        client
            .set("bootstrap.servers", config.brokers.join(","))
            .set("client.id", &config.client_id)
            .set("enable.idempotence", "true")
            .set("acks", "all")
            .set(
                "max.in.flight.requests.per.connection",
                config.max_inflight.to_string(),
            )
            .set("message.max.bytes", config.max_message_bytes.to_string())
            .set("message.timeout.ms", config.message_timeout_ms.to_string());
        apply_kafka_security(&mut client, config);
        let producer = client.create()?;
        Ok(Self {
            producer,
            topic: config.topics.committed_events.clone(),
            timeout: Duration::from_millis(config.message_timeout_ms),
        })
    }
}

fn apply_kafka_security(client: &mut ClientConfig, config: &KafkaConfig) {
    client.set(
        "security.protocol",
        if config.security.protocol == "SSL" {
            "ssl"
        } else {
            "plaintext"
        },
    );
    if let Some(path) = &config.security.ca_location {
        client.set("ssl.ca.location", path.to_string_lossy());
    }
    if let Some(path) = &config.security.certificate_location {
        client.set("ssl.certificate.location", path.to_string_lossy());
    }
    if let Some(path) = &config.security.key_location {
        client.set("ssl.key.location", path.to_string_lossy());
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
    workers: EngineWorkerPolicyDto,
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
            workers: self.workers.into_domain(),
        })
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EngineWorkerPolicyDto {
    poll_interval_ms: u64,
    outbox_batch_size: u32,
    outbox_retry: RetryPolicyDto,
    local_task_batch_size: u32,
    local_task_retry: RetryPolicyDto,
}

impl EngineWorkerPolicyDto {
    const fn into_domain(self) -> EngineWorkerPolicy {
        EngineWorkerPolicy {
            poll_interval_ms: self.poll_interval_ms,
            outbox_batch_size: self.outbox_batch_size,
            outbox_retry: self.outbox_retry.into_domain(),
            local_task_batch_size: self.local_task_batch_size,
            local_task_retry: self.local_task_retry.into_domain(),
        }
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
