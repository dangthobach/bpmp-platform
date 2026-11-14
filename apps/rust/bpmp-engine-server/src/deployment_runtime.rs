use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use bpmp_adapter_kms::{AesGcmPayloadCrypto, DataKeyResolverPort};
use bpmp_adapter_rocksdb::RocksDbWorkflowStore;
use bpmp_contracts::Ed25519Verifier;
use bpmp_contracts::configuration::v1 as configurationv1;
use bpmp_contracts::engine::v1::{
    CommitRuntimeDeploymentRequest, CommitRuntimeDeploymentResponse, GetRuntimeDeploymentRequest,
    GetRuntimeDeploymentResponse,
    engine_deployment_service_server::{EngineDeploymentService, EngineDeploymentServiceServer},
};
use bpmp_engine::{
    RuntimeConfigurationUpdate, RuntimeGovernancePolicyUpdate, RuntimeRegistry,
    RuntimeSafePointGate,
};
use bpmp_raft_state_machine::ApplyOutcome;
use bpmp_raft_state_machine::TypeConfig;
use bpmp_transport_observability::WorkloadAuthorization;
use openraft::Raft;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tonic::{Request, Response, Status};

use crate::config::RuntimeConfig;
use crate::linux_runtime::{
    ConfigurationResolverClient, RuntimeWorkerPolicyHandles,
    common_runtime_worker_policy_after_updates, configuration_snapshot_from_proto,
    connect_configuration_resolver, governance_policy_from_proto,
};
use crate::raft_runtime::PeerDirectory;
use crate::raft_runtime::RaftWorkflowStore;

const MAX_DEPLOYMENT_ARTIFACT_BYTES: usize = 128 * 1024;
const DEPLOYMENT_SCAN_LIMIT: usize = 100_000;

type EngineStore = RocksDbWorkflowStore<AesGcmPayloadCrypto<Arc<dyn DataKeyResolverPort>>>;
type EngineRaftStore = RaftWorkflowStore<AesGcmPayloadCrypto<Arc<dyn DataKeyResolverPort>>>;

#[derive(Clone)]
pub struct RuntimeDeploymentService {
    config: Arc<RuntimeConfig>,
    store: Arc<EngineStore>,
    raft_store: Arc<EngineRaftStore>,
    verifier: Arc<Ed25519Verifier>,
    registry: Arc<RuntimeRegistry>,
    safe_point_gate: RuntimeSafePointGate,
    authorization: WorkloadAuthorization,
    peer_directory: PeerDirectory,
    raft: Raft<TypeConfig>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct RuntimeDeploymentIntent {
    deployment_id: String,
    deployment_sequence: u64,
    tenant_id: String,
    workflow_type: String,
    workflow_version: String,
    environment_reference: String,
    engine_config_version: String,
    engine_policy_version: String,
    engine_config_hash: String,
    governance_config_version: String,
    governance_policy_version: String,
    signed_wir_artifact_base64: String,
    artifact_sha256: String,
    rollback_of_deployment_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct RuntimeDeploymentAck {
    deployment_id: String,
    node_id: u64,
    artifact_sha256: String,
}

struct ResolvedDeployment {
    definition: bpmp_domain_core::WorkflowDefinition,
    configuration: bpmp_domain_core::ResolvedConfigSnapshot,
    governance: RuntimeGovernancePolicyUpdate,
}

impl RuntimeDeploymentService {
    pub fn new(
        config: Arc<RuntimeConfig>,
        store: Arc<EngineStore>,
        raft_store: Arc<EngineRaftStore>,
        verifier: Arc<Ed25519Verifier>,
        registry: Arc<RuntimeRegistry>,
        safe_point_gate: RuntimeSafePointGate,
        authorization: WorkloadAuthorization,
        peer_directory: PeerDirectory,
        raft: Raft<TypeConfig>,
    ) -> Self {
        Self {
            config,
            store,
            raft_store,
            verifier,
            registry,
            safe_point_gate,
            authorization,
            peer_directory,
            raft,
        }
    }

    pub fn into_server(
        self,
        max_decoding_bytes: usize,
        max_encoding_bytes: usize,
    ) -> EngineDeploymentServiceServer<Self> {
        EngineDeploymentServiceServer::new(self)
            .max_decoding_message_size(max_decoding_bytes)
            .max_encoding_message_size(max_encoding_bytes)
    }

    pub fn authorization(config: &RuntimeConfig) -> Result<WorkloadAuthorization> {
        WorkloadAuthorization::try_new(
            &config.authorization.deployment_client_certificate_sha256,
            &[
                "/bpmp.engine.v1.EngineDeploymentService/CommitRuntimeDeployment".into(),
                "/bpmp.engine.v1.EngineDeploymentService/GetRuntimeDeployment".into(),
            ],
        )
        .map_err(anyhow::Error::msg)
    }

    fn required_nodes(&self) -> Result<Vec<u64>, Status> {
        let metrics = self.raft.metrics();
        let mut nodes = metrics
            .borrow()
            .membership_config
            .membership()
            .nodes()
            .map(|(node_id, _)| *node_id)
            .collect::<Vec<_>>();
        nodes.sort_unstable();
        nodes.dedup();
        if nodes.is_empty() {
            return Err(Status::failed_precondition(
                "Engine Raft membership is not initialized",
            ));
        }
        Ok(nodes)
    }

    fn deployment_status(
        &self,
        intent: &RuntimeDeploymentIntent,
    ) -> Result<GetRuntimeDeploymentResponse, Status> {
        let ack_prefix = format!("ack/{}/", intent.deployment_id);
        let records = self
            .store
            .read_runtime_deployment_records(ack_prefix.as_bytes(), DEPLOYMENT_SCAN_LIMIT)
            .map_err(unavailable)?;
        let mut applied = records
            .into_iter()
            .filter_map(|(_, raw)| serde_json::from_slice::<RuntimeDeploymentAck>(&raw).ok())
            .filter(|ack| {
                ack.deployment_id == intent.deployment_id
                    && ack.artifact_sha256 == intent.artifact_sha256
            })
            .map(|ack| ack.node_id)
            .collect::<Vec<_>>();
        applied.sort_unstable();
        applied.dedup();
        let applied_set = applied
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        let required = self.required_nodes()?;
        let pending = required
            .into_iter()
            .filter(|node_id| !applied_set.contains(node_id))
            .collect::<Vec<_>>();
        let mut status = "ACTIVATING";
        if pending.is_empty() {
            if !is_latest_deployment(&self.store, intent).map_err(unavailable)? {
                status = "SUPERSEDED";
                return Ok(GetRuntimeDeploymentResponse {
                    deployment_id: intent.deployment_id.clone(),
                    status: status.into(),
                    artifact_id: intent.artifact_sha256.clone(),
                    applied_node_ids: applied,
                    pending_node_ids: pending,
                    failure_code: String::new(),
                    engine_config_version: intent.engine_config_version.clone(),
                    engine_policy_version: intent.engine_policy_version.clone(),
                    engine_config_sha256: intent.engine_config_hash.clone(),
                    governance_config_version: intent.governance_config_version.clone(),
                    governance_policy_version: intent.governance_policy_version.clone(),
                });
            }
            let tenant = bpmp_domain_core::TenantId::new(intent.tenant_id.clone())
                .map_err(|_| Status::data_loss("deployment tenant scope is invalid"))?;
            let workflow_type =
                bpmp_domain_core::WorkflowType::new(intent.workflow_type.clone())
                    .map_err(|_| Status::data_loss("deployment workflow type is invalid"))?;
            let workflow_version =
                bpmp_domain_core::WorkflowVersion::new(intent.workflow_version.clone())
                    .map_err(|_| Status::data_loss("deployment workflow version is invalid"))?;
            self.safe_point_gate
                .with_safe_point(|_| {
                    self.registry
                        .set_active_version(&tenant, &workflow_type, &workflow_version)
                })
                .map_err(|error| {
                    Status::unavailable(format!(
                        "could not activate local runtime version: {error}"
                    ))
                })?
                .map_err(|error| Status::failed_precondition(error.to_string()))?;
            status = "ACTIVE";
        }
        Ok(GetRuntimeDeploymentResponse {
            deployment_id: intent.deployment_id.clone(),
            status: status.into(),
            artifact_id: intent.artifact_sha256.clone(),
            applied_node_ids: applied,
            pending_node_ids: pending,
            failure_code: String::new(),
            engine_config_version: intent.engine_config_version.clone(),
            engine_policy_version: intent.engine_policy_version.clone(),
            engine_config_sha256: intent.engine_config_hash.clone(),
            governance_config_version: intent.governance_config_version.clone(),
            governance_policy_version: intent.governance_policy_version.clone(),
        })
    }
}

#[tonic::async_trait]
impl EngineDeploymentService for RuntimeDeploymentService {
    async fn commit_runtime_deployment(
        &self,
        request: Request<CommitRuntimeDeploymentRequest>,
    ) -> Result<Response<CommitRuntimeDeploymentResponse>, Status> {
        if !self.authorization.is_tonic_request_authorized(
            &request,
            "/bpmp.engine.v1.EngineDeploymentService/CommitRuntimeDeployment",
        ) {
            return Err(Status::permission_denied(
                "deployment workload certificate is not authorized",
            ));
        }
        let request = request.into_inner();
        validate_deployment_id(&request.deployment_id)?;
        if request.tenant_id.trim().is_empty()
            || request.workflow_type.trim().is_empty()
            || request.workflow_version.trim().is_empty()
            || request.environment_reference.trim().is_empty()
            || request.signed_wir_artifact.is_empty()
            || request.deployment_sequence == 0
            || request.signed_wir_artifact.len() > MAX_DEPLOYMENT_ARTIFACT_BYTES
            || (!request.rollback_of_deployment_id.is_empty()
                && validate_deployment_id(&request.rollback_of_deployment_id).is_err())
        {
            return Err(Status::invalid_argument(
                "runtime deployment fields or artifact size are invalid",
            ));
        }
        let resolver = self.config.configuration_resolver.as_ref().ok_or_else(|| {
            Status::failed_precondition("Engine deployment requires Configuration Service")
        })?;
        if request.environment_reference != resolver.environment_reference {
            return Err(Status::permission_denied(
                "deployment environment does not match this Engine cluster",
            ));
        }
        let digest: [u8; 32] = Sha256::digest(&request.signed_wir_artifact).into();
        if request.artifact_sha256.as_slice() != digest {
            return Err(Status::invalid_argument(
                "artifact SHA-256 does not match signed WIR",
            ));
        }
        match self.load_intent(&request.deployment_id) {
            Ok(existing)
                if existing.tenant_id == request.tenant_id
                    && existing.workflow_type == request.workflow_type
                    && existing.workflow_version == request.workflow_version
                    && existing.environment_reference == request.environment_reference
                    && existing.deployment_sequence == request.deployment_sequence
                    && existing.rollback_of_deployment_id == request.rollback_of_deployment_id
                    && existing.artifact_sha256 == digest_hex(&digest) =>
            {
                return self.deployment_status(&existing).map(Response::new);
            }
            Ok(_) => {
                return Err(Status::already_exists(
                    "deployment ID was already committed with a different intent",
                ));
            }
            Err(error) if error.code() == tonic::Code::NotFound => {}
            Err(error) => return Err(error),
        }
        let rollback_target = if !request.rollback_of_deployment_id.is_empty() {
            let previous = self.load_intent(&request.rollback_of_deployment_id)?;
            if previous.tenant_id != request.tenant_id
                || previous.workflow_type != request.workflow_type
                || previous.workflow_version != request.workflow_version
                || previous.environment_reference != request.environment_reference
                || previous.artifact_sha256 != digest_hex(&digest)
            {
                return Err(Status::failed_precondition(
                    "rollback must target the exact signed artifact and scope of the prior deployment",
                ));
            }
            if !self.deployment_has_all_node_acks(&previous)? {
                return Err(Status::failed_precondition(
                    "rollback target has not been installed and acknowledged by every configured Engine node",
                ));
            }
            Some(previous)
        } else {
            None
        };
        let resolved = resolve_deployment(
            &self.config,
            &self.verifier,
            &request.tenant_id,
            &request.workflow_type,
            &request.workflow_version,
            &request.signed_wir_artifact,
            rollback_target.as_ref().map(|previous| {
                (
                    previous.engine_config_version.as_str(),
                    previous.engine_policy_version.as_str(),
                    previous.governance_config_version.as_str(),
                    previous.governance_policy_version.as_str(),
                )
            }),
        )
        .await
        .map_err(|error| Status::failed_precondition(error.to_string()))?;
        let engine_config_version = resolved.configuration.config_version.as_str().to_owned();
        let engine_policy_version = resolved.configuration.policy_version.as_str().to_owned();
        let engine_config_hash = digest_hex(&resolved.configuration.content_hash);
        let governance_config_version = resolved.governance.config_version.as_str().to_owned();
        let governance_policy_version = resolved.governance.policy_version.as_str().to_owned();
        if rollback_target.as_ref().is_some_and(|previous| {
            previous.engine_config_version != engine_config_version
                || previous.engine_policy_version != engine_policy_version
                || previous.engine_config_hash != engine_config_hash
                || previous.governance_config_version != governance_config_version
                || previous.governance_policy_version != governance_policy_version
        }) {
            return Err(Status::failed_precondition(
                "rollback target configuration snapshots are no longer the resolved versions; refusing to activate a mixed runtime",
            ));
        }

        let artifact_sha256 = digest_hex(&digest);
        let intent = RuntimeDeploymentIntent {
            deployment_id: request.deployment_id.clone(),
            deployment_sequence: request.deployment_sequence,
            tenant_id: request.tenant_id,
            workflow_type: request.workflow_type,
            workflow_version: request.workflow_version,
            environment_reference: request.environment_reference,
            engine_config_version,
            engine_policy_version,
            engine_config_hash,
            governance_config_version,
            governance_policy_version,
            signed_wir_artifact_base64: STANDARD.encode(request.signed_wir_artifact),
            artifact_sha256: artifact_sha256.clone(),
            rollback_of_deployment_id: request.rollback_of_deployment_id,
        };
        self.validate_deployment_sequence(&intent)?;
        let value = serde_json::to_vec(&intent)
            .map_err(|_| Status::internal("deployment intent serialization failed"))?;
        if value.len().saturating_add(4096) > self.config.raft.max_batch_bytes as usize {
            return Err(Status::resource_exhausted(
                "signed WIR is larger than the configured Raft deployment record budget",
            ));
        }
        let record_key = format!("intent/{}", intent.deployment_id).into_bytes();
        let command_id = format!("runtime-deployment/{}", intent.deployment_id);
        let response = match self.raft_store.commit_runtime_deployment_record(
            &command_id,
            record_key.clone(),
            value.clone(),
        ) {
            Ok(response) => response,
            Err(bpmp_engine::StoreError::NotLeader {
                leader_id: Some(leader_id),
                ..
            }) => {
                self.peer_directory
                    .forward_deployment_intent(
                        leader_id,
                        self.config.raft.node_id,
                        command_id,
                        record_key,
                        value,
                    )
                    .await
                    .map_err(map_store_error)?;
                None
            }
            Err(error) => return Err(map_store_error(error)),
        };
        if response
            .as_ref()
            .is_some_and(|result| matches!(result.outcome, ApplyOutcome::Rejected { .. }))
        {
            return Err(Status::failed_precondition(
                "Raft rejected deployment intent",
            ));
        }
        let status = self.deployment_status(&intent)?;
        Ok(Response::new(CommitRuntimeDeploymentResponse {
            deployment_id: status.deployment_id,
            status: status.status,
            artifact_id: artifact_sha256,
            applied_node_ids: status.applied_node_ids,
            pending_node_ids: status.pending_node_ids,
            engine_config_version: status.engine_config_version,
            engine_policy_version: status.engine_policy_version,
            engine_config_sha256: status.engine_config_sha256,
            governance_config_version: status.governance_config_version,
            governance_policy_version: status.governance_policy_version,
        }))
    }

    async fn get_runtime_deployment(
        &self,
        request: Request<GetRuntimeDeploymentRequest>,
    ) -> Result<Response<GetRuntimeDeploymentResponse>, Status> {
        if !self.authorization.is_tonic_request_authorized(
            &request,
            "/bpmp.engine.v1.EngineDeploymentService/GetRuntimeDeployment",
        ) {
            return Err(Status::permission_denied(
                "deployment workload certificate is not authorized",
            ));
        }
        let deployment_id = request.into_inner().deployment_id;
        validate_deployment_id(&deployment_id)?;
        let intent = self.load_intent(&deployment_id)?;
        self.deployment_status(&intent).map(Response::new)
    }
}

impl RuntimeDeploymentService {
    fn validate_deployment_sequence(&self, intent: &RuntimeDeploymentIntent) -> Result<(), Status> {
        let records = self
            .store
            .read_runtime_deployment_records(b"intent/", DEPLOYMENT_SCAN_LIMIT)
            .map_err(unavailable)?;
        let mut latest = 0_u64;
        for (_, raw) in records {
            let existing: RuntimeDeploymentIntent = serde_json::from_slice(&raw)
                .map_err(|_| Status::data_loss("runtime deployment intent is corrupt"))?;
            if existing.deployment_id == intent.deployment_id {
                if existing.deployment_sequence == intent.deployment_sequence
                    && existing.artifact_sha256 == intent.artifact_sha256
                    && existing.tenant_id == intent.tenant_id
                    && existing.workflow_type == intent.workflow_type
                    && existing.workflow_version == intent.workflow_version
                    && existing.environment_reference == intent.environment_reference
                    && existing.engine_config_version == intent.engine_config_version
                    && existing.engine_policy_version == intent.engine_policy_version
                    && existing.engine_config_hash == intent.engine_config_hash
                    && existing.governance_config_version == intent.governance_config_version
                    && existing.governance_policy_version == intent.governance_policy_version
                {
                    return Ok(());
                }
                return Err(Status::already_exists(
                    "deployment_id is already bound to a different intent",
                ));
            }
            if existing.tenant_id == intent.tenant_id
                && existing.workflow_type == intent.workflow_type
                && existing.environment_reference == intent.environment_reference
            {
                if existing.deployment_sequence == intent.deployment_sequence {
                    return Err(Status::already_exists(
                        "deployment sequence is already used in this workflow environment",
                    ));
                }
                latest = latest.max(existing.deployment_sequence);
            }
        }
        if intent.deployment_sequence <= latest {
            return Err(Status::failed_precondition(
                "deployment sequence must increase for the workflow environment",
            ));
        }
        Ok(())
    }

    fn load_intent(&self, deployment_id: &str) -> Result<RuntimeDeploymentIntent, Status> {
        let key = format!("intent/{deployment_id}").into_bytes();
        let records = self
            .store
            .read_runtime_deployment_records(&key, 1)
            .map_err(unavailable)?;
        let (_, raw) = records
            .into_iter()
            .find(|(record_key, _)| record_key == &key)
            .ok_or_else(|| Status::not_found("runtime deployment was not found"))?;
        serde_json::from_slice(&raw)
            .map_err(|_| Status::data_loss("runtime deployment intent is corrupt"))
    }

    fn deployment_has_all_node_acks(
        &self,
        intent: &RuntimeDeploymentIntent,
    ) -> Result<bool, Status> {
        let prefix = format!("ack/{}/", intent.deployment_id);
        let records = self
            .store
            .read_runtime_deployment_records(prefix.as_bytes(), DEPLOYMENT_SCAN_LIMIT)
            .map_err(unavailable)?;
        let acknowledged = records
            .into_iter()
            .filter_map(|(_, raw)| serde_json::from_slice::<RuntimeDeploymentAck>(&raw).ok())
            .filter(|ack| {
                ack.deployment_id == intent.deployment_id
                    && ack.artifact_sha256 == intent.artifact_sha256
            })
            .map(|ack| ack.node_id)
            .collect::<std::collections::BTreeSet<_>>();
        Ok(self
            .required_nodes()?
            .into_iter()
            .all(|node_id| acknowledged.contains(&node_id)))
    }
}

pub async fn run_runtime_deployment_worker(
    config: Arc<RuntimeConfig>,
    store: Arc<EngineStore>,
    raft_store: Arc<EngineRaftStore>,
    registry: Arc<RuntimeRegistry>,
    safe_point_gate: RuntimeSafePointGate,
    worker_handles: RuntimeWorkerPolicyHandles,
    verifier: Arc<Ed25519Verifier>,
    peer_directory: PeerDirectory,
    raft: Raft<TypeConfig>,
) -> Result<()> {
    let interval = Duration::from_millis(config.workers.poll_interval_ms.max(100));
    loop {
        let records = store.read_runtime_deployment_records(b"intent/", DEPLOYMENT_SCAN_LIMIT)?;
        for (key, raw) in records {
            let intent: RuntimeDeploymentIntent = match serde_json::from_slice(&raw) {
                Ok(intent) => intent,
                Err(error) => {
                    tracing::error!(%error, "invalid committed runtime deployment intent");
                    continue;
                }
            };
            if key != format!("intent/{}", intent.deployment_id).as_bytes() {
                tracing::error!(
                    deployment_id = intent.deployment_id,
                    "deployment intent key mismatch"
                );
                continue;
            }
            let ack_key = format!("ack/{}/{:020}", intent.deployment_id, config.raft.node_id);
            let already_acknowledged = !store
                .read_runtime_deployment_records(ack_key.as_bytes(), 1)?
                .is_empty();
            if already_acknowledged {
                if all_nodes_acknowledged(&raft, &store, &intent)?
                    && is_latest_deployment(&store, &intent)?
                {
                    activate_local_version(&registry, &safe_point_gate, &intent)?;
                }
                continue;
            }
            let attempt = apply_deployment_intent(
                &config,
                verifier.as_ref(),
                &intent,
                &registry,
                &safe_point_gate,
                &worker_handles,
            )
            .await;
            match attempt {
                Ok(()) => {
                    let ack = RuntimeDeploymentAck {
                        deployment_id: intent.deployment_id.clone(),
                        node_id: config.raft.node_id,
                        artifact_sha256: intent.artifact_sha256.clone(),
                    };
                    let ack_value = serde_json::to_vec(&ack)?;
                    let ack_command = format!(
                        "runtime-deployment-ack/{}/{}",
                        intent.deployment_id, config.raft.node_id
                    );
                    let commit = raft_store.commit_runtime_deployment_record(
                        &ack_command,
                        ack_key.as_bytes().to_vec(),
                        ack_value.clone(),
                    );
                    let commit = match commit {
                        Err(bpmp_engine::StoreError::NotLeader {
                            leader_id: Some(leader_id),
                            ..
                        }) => {
                            peer_directory
                                .forward_deployment_ack(
                                    leader_id,
                                    config.raft.node_id,
                                    ack_command.clone(),
                                    ack_key.into_bytes(),
                                    ack_value,
                                )
                                .await
                        }
                        Err(error) => Err(error),
                        Ok(_) => Ok(()),
                    };
                    if let Err(error) = commit {
                        tracing::warn!(%error, deployment_id = intent.deployment_id, "could not commit Engine deployment ACK");
                    } else if all_nodes_acknowledged(&raft, &store, &intent)?
                        && is_latest_deployment(&store, &intent)?
                    {
                        activate_local_version(&registry, &safe_point_gate, &intent)?;
                        tracing::info!(
                            deployment_id = intent.deployment_id,
                            workflow_type = intent.workflow_type,
                            workflow_version = intent.workflow_version,
                            "Engine cluster deployment is active"
                        );
                    }
                }
                Err(error) => {
                    tracing::warn!(%error, deployment_id = intent.deployment_id, node_id = config.raft.node_id, "runtime deployment apply is pending retry")
                }
            }
        }
        tokio::time::sleep(interval).await;
    }
}

async fn apply_deployment_intent(
    config: &RuntimeConfig,
    verifier: &Ed25519Verifier,
    intent: &RuntimeDeploymentIntent,
    registry: &RuntimeRegistry,
    gate: &RuntimeSafePointGate,
    worker_handles: &RuntimeWorkerPolicyHandles,
) -> Result<()> {
    let bytes = STANDARD
        .decode(&intent.signed_wir_artifact_base64)
        .context("decode signed WIR deployment artifact")?;
    let digest: [u8; 32] = Sha256::digest(&bytes).into();
    if digest_hex(&digest) != intent.artifact_sha256 {
        anyhow::bail!("deployment artifact failed its content-address check");
    }
    let resolved = resolve_deployment(
        config,
        verifier,
        &intent.tenant_id,
        &intent.workflow_type,
        &intent.workflow_version,
        &bytes,
        Some((
            &intent.engine_config_version,
            &intent.engine_policy_version,
            &intent.governance_config_version,
            &intent.governance_policy_version,
        )),
    )
    .await?;
    if resolved.configuration.config_version.as_str() != intent.engine_config_version
        || resolved.configuration.policy_version.as_str() != intent.engine_policy_version
        || digest_hex(&resolved.configuration.content_hash) != intent.engine_config_hash
        || resolved.governance.config_version.as_str() != intent.governance_config_version
        || resolved.governance.policy_version.as_str() != intent.governance_policy_version
    {
        anyhow::bail!(
            "resolved configuration versions differ from the Raft-pinned deployment intent"
        );
    }
    let update = RuntimeConfigurationUpdate {
        tenant_id: resolved.definition.tenant_id.clone(),
        workflow_type: resolved.definition.workflow_type.clone(),
        workflow_version: resolved.definition.workflow_version.clone(),
        configuration: resolved.configuration.clone(),
    };
    let worker_policy =
        common_runtime_worker_policy_after_updates(registry, std::slice::from_ref(&update))?;
    gate.with_safe_point(|safe_point| -> Result<()> {
        registry.migrate_verified(
            resolved.definition,
            resolved.configuration,
            digest,
            safe_point,
        )?;
        registry.install_governance_policy(resolved.governance)?;
        if let Some((boundary_policy, worker_policy)) = worker_policy {
            worker_handles.replace(boundary_policy, worker_policy)?;
        }
        Ok(())
    })??;
    Ok(())
}

async fn resolve_deployment(
    config: &RuntimeConfig,
    verifier: &Ed25519Verifier,
    tenant_id: &str,
    workflow_type: &str,
    workflow_version: &str,
    artifact: &[u8],
    pinned_versions: Option<(&str, &str, &str, &str)>,
) -> Result<ResolvedDeployment> {
    let definition =
        bpmp_engine::WirLoader::load(artifact, verifier).context("verify and decode signed WIR")?;
    if definition.tenant_id.as_str() != tenant_id
        || definition.workflow_type.as_str() != workflow_type
        || definition.workflow_version.as_str() != workflow_version
    {
        anyhow::bail!("signed WIR scope does not match deployment request");
    }
    let resolver = config
        .configuration_resolver
        .as_ref()
        .context("Engine deployment requires Configuration Service")?;
    let mut client = connect_configuration_resolver(config, resolver).await?;
    let (
        engine_config_version,
        engine_policy_version,
        governance_config_version,
        governance_policy_version,
    ) = pinned_versions.unwrap_or_default();
    let engine = client
        .resolve_configuration(configurationv1::ResolveConfigurationRequest {
            tenant_id: tenant_id.into(),
            workflow_type: workflow_type.into(),
            workflow_version: workflow_version.into(),
            platform_reference: resolver.platform_reference.clone(),
            environment_reference: resolver.environment_reference.clone(),
            instance_id: String::new(),
            owner: configurationv1::ConfigurationOwner::Engine as i32,
            config_version: engine_config_version.into(),
            policy_version: engine_policy_version.into(),
        })
        .await?
        .into_inner()
        .snapshot
        .context("Configuration Service returned no Engine snapshot")?;
    let configuration = configuration_snapshot_from_proto(engine)?;
    let governance = client
        .resolve_configuration(configurationv1::ResolveConfigurationRequest {
            tenant_id: tenant_id.into(),
            workflow_type: workflow_type.into(),
            workflow_version: workflow_version.into(),
            platform_reference: resolver.platform_reference.clone(),
            environment_reference: resolver.environment_reference.clone(),
            instance_id: String::new(),
            owner: configurationv1::ConfigurationOwner::Governance as i32,
            config_version: governance_config_version.into(),
            policy_version: governance_policy_version.into(),
        })
        .await?
        .into_inner()
        .snapshot
        .context("Configuration Service returned no Governance snapshot")?;
    let (policy, config_version, policy_version) = governance_policy_from_proto(governance)?;
    Ok(ResolvedDeployment {
        definition,
        configuration,
        governance: RuntimeGovernancePolicyUpdate {
            tenant_id: bpmp_domain_core::TenantId::new(tenant_id.to_owned())?,
            workflow_type: bpmp_domain_core::WorkflowType::new(workflow_type.to_owned())?,
            workflow_version: bpmp_domain_core::WorkflowVersion::new(workflow_version.to_owned())?,
            policy,
            config_version,
            policy_version,
        },
    })
}

fn all_nodes_acknowledged(
    raft: &Raft<TypeConfig>,
    store: &EngineStore,
    intent: &RuntimeDeploymentIntent,
) -> Result<bool> {
    let prefix = format!("ack/{}/", intent.deployment_id);
    let records =
        store.read_runtime_deployment_records(prefix.as_bytes(), DEPLOYMENT_SCAN_LIMIT)?;
    let acknowledged = records
        .into_iter()
        .filter_map(|(_, raw)| serde_json::from_slice::<RuntimeDeploymentAck>(&raw).ok())
        .filter(|ack| {
            ack.deployment_id == intent.deployment_id
                && ack.artifact_sha256 == intent.artifact_sha256
        })
        .map(|ack| ack.node_id)
        .collect::<std::collections::BTreeSet<_>>();
    let metrics = raft.metrics();
    let required = metrics
        .borrow()
        .membership_config
        .membership()
        .nodes()
        .map(|(node_id, _)| *node_id)
        .collect::<std::collections::BTreeSet<_>>();
    Ok(!required.is_empty()
        && required
            .iter()
            .all(|node_id| acknowledged.contains(node_id)))
}

fn is_latest_deployment(store: &EngineStore, intent: &RuntimeDeploymentIntent) -> Result<bool> {
    let records = store.read_runtime_deployment_records(b"intent/", DEPLOYMENT_SCAN_LIMIT)?;
    let mut latest = None;
    for (_, raw) in records {
        let candidate: RuntimeDeploymentIntent =
            serde_json::from_slice(&raw).context("decode committed runtime deployment intent")?;
        if candidate.tenant_id == intent.tenant_id
            && candidate.workflow_type == intent.workflow_type
            && candidate.environment_reference == intent.environment_reference
        {
            latest = Some(latest.unwrap_or(0_u64).max(candidate.deployment_sequence));
        }
    }
    Ok(latest == Some(intent.deployment_sequence))
}

fn activate_local_version(
    registry: &RuntimeRegistry,
    gate: &RuntimeSafePointGate,
    intent: &RuntimeDeploymentIntent,
) -> Result<()> {
    let tenant = bpmp_domain_core::TenantId::new(intent.tenant_id.clone())?;
    let workflow_type = bpmp_domain_core::WorkflowType::new(intent.workflow_type.clone())?;
    let workflow_version = bpmp_domain_core::WorkflowVersion::new(intent.workflow_version.clone())?;
    gate.with_safe_point(|_| {
        registry.set_active_version(&tenant, &workflow_type, &workflow_version)
    })??;
    Ok(())
}

fn digest_hex(digest: &[u8; 32]) -> String {
    let mut encoded = String::with_capacity(64);
    for byte in digest {
        use std::fmt::Write as _;
        let _ = write!(&mut encoded, "{byte:02x}");
    }
    encoded
}

fn validate_deployment_id(value: &str) -> Result<(), Status> {
    let valid = value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        });
    if valid {
        Ok(())
    } else {
        Err(Status::invalid_argument("deployment_id must be a UUID"))
    }
}

fn unavailable(error: impl std::fmt::Display) -> Status {
    Status::unavailable(format!("Engine deployment state unavailable: {error}"))
}

fn map_store_error(error: bpmp_engine::StoreError) -> Status {
    match error {
        bpmp_engine::StoreError::NotLeader { leader_id, .. } => {
            let mut status = Status::unavailable("request must be sent to the Engine Raft leader");
            if let Some(leader_id) = leader_id {
                if let Ok(value) = leader_id.to_string().parse() {
                    status.metadata_mut().insert("bpmp-engine-leader-id", value);
                }
            }
            status
        }
        bpmp_engine::StoreError::CorruptData(message) => Status::data_loss(message),
        other => Status::unavailable(other.to_string()),
    }
}
