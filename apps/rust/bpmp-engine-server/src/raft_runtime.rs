use std::collections::{BTreeMap, BTreeSet};
use std::io;
use std::sync::Arc;
use std::time::Duration;

use bpmp_adapter_rocksdb::{RocksDbWorkflowStore, WorkflowCommitPreparation};
use bpmp_contracts::engine::v1::{CommandEnvelope, CommandReceipt};
use bpmp_contracts::raft::v1::raft_peer_service_client::RaftPeerServiceClient;
use bpmp_contracts::raft::v1::raft_peer_service_server::{RaftPeerService, RaftPeerServiceServer};
use bpmp_contracts::raft::v1::{
    AddLearnerRequest, AddLearnerResponse, AppendEntriesRequest as WireAppendEntriesRequest,
    AppendEntriesResponse as WireAppendEntriesResponse, ChangeMembershipRequest,
    ChangeMembershipResponse, ForwardCommandRequest, ForwardCommandResponse,
    InstallSnapshotRequest as WireInstallSnapshotRequest,
    InstallSnapshotResponse as WireInstallSnapshotResponse, VoteRequest as WireVoteRequest,
    VoteResponse as WireVoteResponse,
};
use bpmp_domain_core::{ActorId, CommandId, IdempotencyKey, InstanceId, TenantId};
use bpmp_engine::{
    CommitOutcome, CommitRequest, CommittedResult, EngineCommandHandlerPort, EngineError,
    LoadedInstance, LocalTaskActivation, RemoteTask, RemoteTaskClaim, RemoteTaskClaimRequest,
    RemoteTaskEnqueueOutcome, RemoteTaskError, RemoteTaskFailureOutcome, RemoteTaskIngressPort,
    RemoteTaskStorePort, StoreError, TransportError, WorkflowStorePort,
};
use bpmp_payload_crypto::PayloadCryptoPort;
use bpmp_raft_state_machine::{ApplyOutcome, ApplyResponse, PreparedAtomicBatch, TypeConfig};
use bpmp_transport_observability::inject_tonic_metadata;
use openraft::error::{NetworkError, RPCError, RaftError, RemoteError};
use openraft::network::RPCOption;
use openraft::raft::{
    AppendEntriesRequest, AppendEntriesResponse, InstallSnapshotRequest, InstallSnapshotResponse,
    VoteRequest, VoteResponse,
};
use openraft::{
    BasicNode, Raft, RaftNetwork, RaftNetworkFactory, error::Infallible as RaftInfallible,
};
use serde::Serialize;
use serde::de::DeserializeOwned;
use tokio::runtime::Handle;
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity};
use tonic::{Request, Response, Status};

/// Routes every authoritative workflow mutation through the local `OpenRaft` node.
///
/// Reads remain local because `OpenRaft` only acknowledges a client write after
/// the state machine has durably applied the committed log entry on this node.
pub struct RaftWorkflowStore<C> {
    local: Arc<RocksDbWorkflowStore<C>>,
    raft: Raft<TypeConfig>,
    runtime: Handle,
    proposal_admission: Arc<tokio::sync::Semaphore>,
    max_prepare_attempts: u32,
    proposal_timeout: Duration,
}

const RAFT_RPC_SCHEMA_VERSION: u32 = 1;
const MAX_FORWARD_HOPS: u32 = 1;

type RaftRpcError<E = RaftInfallible> = RPCError<u64, BasicNode, RaftError<u64, E>>;

#[derive(Debug, Clone)]
pub struct RaftPeer {
    pub node_id: u64,
    pub address: String,
    pub tls_domain: String,
}

#[derive(Clone)]
pub struct PeerDirectory {
    peers: Arc<BTreeMap<u64, RaftPeer>>,
    tls: Arc<PeerClientTls>,
    rpc_timeout: Duration,
    max_message_bytes: usize,
    #[cfg(test)]
    insecure_test_transport: bool,
}

#[derive(Clone)]
struct PeerClientTls {
    ca: Vec<u8>,
    certificate: Vec<u8>,
    private_key: Vec<u8>,
}

impl PeerDirectory {
    /// Creates a bounded, immutable peer endpoint directory.
    ///
    /// # Errors
    ///
    /// Rejects duplicate/empty peers, missing TLS material, and zero timeout.
    pub fn new(
        peers: impl IntoIterator<Item = RaftPeer>,
        ca: Vec<u8>,
        certificate: Vec<u8>,
        private_key: Vec<u8>,
        rpc_timeout: Duration,
        max_message_bytes: usize,
    ) -> Result<Self, String> {
        if ca.is_empty()
            || certificate.is_empty()
            || private_key.is_empty()
            || rpc_timeout.is_zero()
            || max_message_bytes == 0
        {
            return Err("Raft peer TLS material and timeout must be configured".into());
        }
        let mut indexed = BTreeMap::new();
        for peer in peers {
            if peer.node_id == 0
                || peer.address.trim().is_empty()
                || peer.tls_domain.trim().is_empty()
                || indexed.insert(peer.node_id, peer).is_some()
            {
                return Err("Raft peer identity is invalid or duplicated".into());
            }
        }
        if indexed.is_empty() {
            return Err("Raft peer directory must not be empty".into());
        }
        Ok(Self {
            peers: Arc::new(indexed),
            tls: Arc::new(PeerClientTls {
                ca,
                certificate,
                private_key,
            }),
            rpc_timeout,
            max_message_bytes,
            #[cfg(test)]
            insecure_test_transport: false,
        })
    }

    pub fn membership(&self) -> BTreeMap<u64, BasicNode> {
        self.peers
            .iter()
            .map(|(id, peer)| (*id, BasicNode::new(peer.address.clone())))
            .collect()
    }

    fn contains(&self, node_id: u64) -> bool {
        self.peers.contains_key(&node_id)
    }

    async fn client(&self, node_id: u64) -> Result<RaftPeerServiceClient<Channel>, io::Error> {
        let peer = self.peers.get(&node_id).ok_or_else(|| {
            io::Error::new(io::ErrorKind::NotFound, "Raft peer is not configured")
        })?;
        #[cfg(test)]
        if self.insecure_test_transport {
            return Endpoint::from_shared(format!("http://{}", peer.address))
                .map_err(io_other)?
                .connect_timeout(self.rpc_timeout)
                .timeout(self.rpc_timeout)
                .connect()
                .await
                .map(|channel| self.configured_client(channel))
                .map_err(io_other);
        }
        let endpoint = Endpoint::from_shared(format!("https://{}", peer.address))
            .map_err(io_other)?
            .connect_timeout(self.rpc_timeout)
            .timeout(self.rpc_timeout)
            .tls_config(
                ClientTlsConfig::new()
                    .domain_name(peer.tls_domain.clone())
                    .ca_certificate(Certificate::from_pem(self.tls.ca.clone()))
                    .identity(Identity::from_pem(
                        self.tls.certificate.clone(),
                        self.tls.private_key.clone(),
                    )),
            )
            .map_err(io_other)?;
        endpoint
            .connect()
            .await
            .map(|channel| self.configured_client(channel))
            .map_err(io_other)
    }

    fn configured_client(&self, channel: Channel) -> RaftPeerServiceClient<Channel> {
        RaftPeerServiceClient::new(channel)
            .max_decoding_message_size(self.max_message_bytes)
            .max_encoding_message_size(self.max_message_bytes)
    }

    async fn forward_command(
        &self,
        leader_id: u64,
        command: CommandEnvelope,
    ) -> Result<CommandReceipt, TransportError> {
        let mut client = self
            .client(leader_id)
            .await
            .map_err(|error| unavailable_transport(error.to_string()))?;
        let mut outbound = tonic::Request::new(ForwardCommandRequest {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            hop_count: MAX_FORWARD_HOPS,
            command: Some(command),
        });
        inject_tonic_metadata(&mut outbound);
        let response = client
            .forward_command(outbound)
            .await
            .map_err(|error| unavailable_transport(error.to_string()))?
            .into_inner();
        response
            .receipt
            .ok_or_else(|| unavailable_transport("leader returned no command receipt".into()))
    }
}

#[derive(Clone)]
pub struct TonicRaftNetworkFactory {
    source_node_id: u64,
    peers: PeerDirectory,
}

impl TonicRaftNetworkFactory {
    pub const fn new(source_node_id: u64, peers: PeerDirectory) -> Self {
        Self {
            source_node_id,
            peers,
        }
    }
}

pub struct TonicRaftConnection {
    source_node_id: u64,
    target_node_id: u64,
    peers: PeerDirectory,
}

impl RaftNetworkFactory<TypeConfig> for TonicRaftNetworkFactory {
    type Network = TonicRaftConnection;

    async fn new_client(&mut self, target: u64, _node: &BasicNode) -> Self::Network {
        TonicRaftConnection {
            source_node_id: self.source_node_id,
            target_node_id: target,
            peers: self.peers.clone(),
        }
    }
}

impl RaftNetwork<TypeConfig> for TonicRaftConnection {
    async fn append_entries(
        &mut self,
        request: AppendEntriesRequest<TypeConfig>,
        option: RPCOption,
    ) -> Result<AppendEntriesResponse<u64>, RaftRpcError> {
        let payload_json = encode_request(&request)?;
        let mut client = self.client().await?;
        let mut outbound = tonic::Request::new(WireAppendEntriesRequest {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            source_node_id: self.source_node_id,
            payload_json,
        });
        inject_tonic_metadata(&mut outbound);
        outbound.set_timeout(option.hard_ttl().min(self.peers.rpc_timeout));
        let response = client
            .append_entries(outbound)
            .await
            .map_err(|error| network_status(&error))?
            .into_inner();
        decode_remote_result(self.target_node_id, &response.payload_json)
    }

    async fn install_snapshot(
        &mut self,
        request: InstallSnapshotRequest<TypeConfig>,
        option: RPCOption,
    ) -> Result<InstallSnapshotResponse<u64>, RaftRpcError<openraft::error::InstallSnapshotError>>
    {
        let payload_json = encode_request(&request)?;
        let mut client = self.client().await?;
        let mut outbound = tonic::Request::new(WireInstallSnapshotRequest {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            source_node_id: self.source_node_id,
            payload_json,
        });
        inject_tonic_metadata(&mut outbound);
        outbound.set_timeout(option.hard_ttl().min(self.peers.rpc_timeout));
        let response = client
            .install_snapshot(outbound)
            .await
            .map_err(|error| network_status(&error))?
            .into_inner();
        decode_remote_result(self.target_node_id, &response.payload_json)
    }

    async fn vote(
        &mut self,
        request: VoteRequest<u64>,
        option: RPCOption,
    ) -> Result<VoteResponse<u64>, RaftRpcError> {
        let payload_json = encode_request(&request)?;
        let mut client = self.client().await?;
        let mut outbound = tonic::Request::new(WireVoteRequest {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            source_node_id: self.source_node_id,
            payload_json,
        });
        inject_tonic_metadata(&mut outbound);
        outbound.set_timeout(option.hard_ttl().min(self.peers.rpc_timeout));
        let response = client
            .vote(outbound)
            .await
            .map_err(|error| network_status(&error))?
            .into_inner();
        decode_remote_result(self.target_node_id, &response.payload_json)
    }
}

impl TonicRaftConnection {
    async fn client<E>(&self) -> Result<RaftPeerServiceClient<Channel>, RaftRpcError<E>>
    where
        E: std::error::Error,
    {
        self.peers
            .client(self.target_node_id)
            .await
            .map_err(|error| RPCError::Network(NetworkError::new(&error)))
    }
}

#[derive(Clone)]
pub struct ForwardingCommandHandler<H> {
    local: Arc<H>,
    peers: PeerDirectory,
    runtime: Handle,
}

impl<H> ForwardingCommandHandler<H> {
    pub fn new(local: Arc<H>, peers: PeerDirectory, runtime: Handle) -> Self {
        Self {
            local,
            peers,
            runtime,
        }
    }
}

impl<H> EngineCommandHandlerPort for ForwardingCommandHandler<H>
where
    H: EngineCommandHandlerPort,
{
    fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError> {
        match self.local.handle(envelope.clone()) {
            Err(TransportError::Engine(EngineError::Store(StoreError::NotLeader {
                leader_id: Some(leader_id),
                ..
            }))) => self
                .runtime
                .block_on(self.peers.forward_command(leader_id, envelope)),
            result => result,
        }
    }
}

#[derive(Clone)]
pub struct TonicRaftPeerService<H> {
    raft: Raft<TypeConfig>,
    local_handler: Arc<H>,
    peers: PeerDirectory,
}

impl<H> TonicRaftPeerService<H> {
    pub fn new(raft: Raft<TypeConfig>, local_handler: Arc<H>, peers: PeerDirectory) -> Self {
        Self {
            raft,
            local_handler,
            peers,
        }
    }

    pub fn into_server(
        self,
        max_decoding_bytes: usize,
        max_encoding_bytes: usize,
    ) -> RaftPeerServiceServer<Self>
    where
        H: EngineCommandHandlerPort,
    {
        RaftPeerServiceServer::new(self)
            .max_decoding_message_size(max_decoding_bytes)
            .max_encoding_message_size(max_encoding_bytes)
    }

    fn validate_rpc(&self, schema_version: u32, source_node_id: u64) -> Result<(), Status> {
        if schema_version != RAFT_RPC_SCHEMA_VERSION {
            return Err(Status::failed_precondition(
                "unsupported Raft RPC schema version",
            ));
        }
        if !self.peers.contains(source_node_id) {
            return Err(Status::permission_denied(
                "Raft RPC source node is not configured",
            ));
        }
        Ok(())
    }
}

#[tonic::async_trait]
impl<H> RaftPeerService for TonicRaftPeerService<H>
where
    H: EngineCommandHandlerPort,
{
    async fn append_entries(
        &self,
        request: Request<WireAppendEntriesRequest>,
    ) -> Result<Response<WireAppendEntriesResponse>, Status> {
        let request = request.into_inner();
        self.validate_rpc(request.schema_version, request.source_node_id)?;
        let command: AppendEntriesRequest<TypeConfig> = decode_request(&request.payload_json)?;
        let result = self.raft.append_entries(command).await;
        Ok(Response::new(WireAppendEntriesResponse {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            payload_json: encode_response(&result)?,
        }))
    }

    async fn vote(
        &self,
        request: Request<WireVoteRequest>,
    ) -> Result<Response<WireVoteResponse>, Status> {
        let request = request.into_inner();
        self.validate_rpc(request.schema_version, request.source_node_id)?;
        let command: VoteRequest<u64> = decode_request(&request.payload_json)?;
        let result = self.raft.vote(command).await;
        Ok(Response::new(WireVoteResponse {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            payload_json: encode_response(&result)?,
        }))
    }

    async fn install_snapshot(
        &self,
        request: Request<WireInstallSnapshotRequest>,
    ) -> Result<Response<WireInstallSnapshotResponse>, Status> {
        let request = request.into_inner();
        self.validate_rpc(request.schema_version, request.source_node_id)?;
        let command: InstallSnapshotRequest<TypeConfig> = decode_request(&request.payload_json)?;
        let result = self.raft.install_snapshot(command).await;
        Ok(Response::new(WireInstallSnapshotResponse {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            payload_json: encode_response(&result)?,
        }))
    }

    async fn forward_command(
        &self,
        request: Request<ForwardCommandRequest>,
    ) -> Result<Response<ForwardCommandResponse>, Status> {
        let request = request.into_inner();
        if request.schema_version != RAFT_RPC_SCHEMA_VERSION
            || request.hop_count != MAX_FORWARD_HOPS
        {
            return Err(Status::failed_precondition(
                "forward schema or hop count is invalid",
            ));
        }
        let command = request
            .command
            .ok_or_else(|| Status::invalid_argument("forwarded command is missing"))?;
        let handler = Arc::clone(&self.local_handler);
        let receipt = tokio::task::spawn_blocking(move || handler.handle(command))
            .await
            .map_err(|error| Status::internal(format!("leader command worker failed: {error}")))?
            .map_err(Status::from)?;
        Ok(Response::new(ForwardCommandResponse {
            receipt: Some(receipt),
        }))
    }

    async fn add_learner(
        &self,
        request: Request<AddLearnerRequest>,
    ) -> Result<Response<AddLearnerResponse>, Status> {
        let request = request.into_inner();
        if request.schema_version != RAFT_RPC_SCHEMA_VERSION
            || request.node_id == 0
            || request.raft_address.trim().is_empty()
        {
            return Err(Status::invalid_argument("learner request is invalid"));
        }
        let response = self
            .raft
            .add_learner(
                request.node_id,
                BasicNode::new(request.raft_address),
                request.blocking,
            )
            .await
            .map_err(|error| Status::failed_precondition(error.to_string()))?;
        Ok(Response::new(AddLearnerResponse {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            log_index: response.log_id.index,
            leader_id: self.raft.metrics().borrow().current_leader,
            leader_address: leader_address(&self.raft),
        }))
    }

    async fn change_membership(
        &self,
        request: Request<ChangeMembershipRequest>,
    ) -> Result<Response<ChangeMembershipResponse>, Status> {
        let request = request.into_inner();
        let voters = request.voter_node_ids.into_iter().collect::<BTreeSet<_>>();
        if request.schema_version != RAFT_RPC_SCHEMA_VERSION
            || voters.is_empty()
            || voters.iter().any(|node_id| !self.peers.contains(*node_id))
        {
            return Err(Status::invalid_argument("membership request is invalid"));
        }
        let response = self
            .raft
            .change_membership(voters, request.retain_removed_as_learners)
            .await
            .map_err(|error| Status::failed_precondition(error.to_string()))?;
        Ok(Response::new(ChangeMembershipResponse {
            schema_version: RAFT_RPC_SCHEMA_VERSION,
            log_index: response.log_id.index,
            leader_id: self.raft.metrics().borrow().current_leader,
            leader_address: leader_address(&self.raft),
        }))
    }
}

fn leader_address(raft: &Raft<TypeConfig>) -> Option<String> {
    raft.metrics()
        .borrow()
        .current_leader
        .and_then(|leader_id| {
            raft.metrics()
                .borrow()
                .membership_config
                .membership()
                .get_node(&leader_id)
                .map(|node| node.addr.clone())
        })
}

#[allow(clippy::result_large_err)]
fn encode_request<T: Serialize, E: std::error::Error>(
    value: &T,
) -> Result<Vec<u8>, RaftRpcError<E>> {
    serde_json::to_vec(value).map_err(|error| json_network_error(&error))
}

fn decode_request<T: DeserializeOwned>(payload: &[u8]) -> Result<T, Status> {
    serde_json::from_slice(payload)
        .map_err(|error| Status::invalid_argument(format!("invalid Raft RPC payload: {error}")))
}

fn encode_response<T: Serialize>(value: &T) -> Result<Vec<u8>, Status> {
    serde_json::to_vec(value)
        .map_err(|error| Status::internal(format!("encode Raft RPC response: {error}")))
}

#[allow(clippy::result_large_err)]
fn decode_remote_result<T, E>(target: u64, payload: &[u8]) -> Result<T, RaftRpcError<E>>
where
    T: DeserializeOwned,
    E: std::error::Error + DeserializeOwned,
{
    let decoded: Result<T, RaftError<u64, E>> =
        serde_json::from_slice(payload).map_err(|error| json_network_error(&error))?;
    decoded.map_err(|error| RPCError::RemoteError(RemoteError::new(target, error)))
}

fn network_status<E>(error: &tonic::Status) -> RaftRpcError<E>
where
    E: std::error::Error,
{
    RPCError::Network(NetworkError::new(&io::Error::other(error.to_string())))
}

fn json_network_error<E>(error: &serde_json::Error) -> RaftRpcError<E>
where
    E: std::error::Error,
{
    RPCError::Network(NetworkError::new(&io::Error::new(
        io::ErrorKind::InvalidData,
        error.to_string(),
    )))
}

fn io_other(error: impl std::fmt::Display) -> io::Error {
    io::Error::other(error.to_string())
}

fn unavailable_transport(message: String) -> TransportError {
    TransportError::Engine(EngineError::Store(StoreError::Unavailable(message)))
}

impl<C> RaftWorkflowStore<C> {
    pub fn new(
        local: Arc<RocksDbWorkflowStore<C>>,
        raft: Raft<TypeConfig>,
        runtime: Handle,
        max_inflight_proposals: usize,
        max_prepare_attempts: u32,
        proposal_timeout: Duration,
    ) -> Result<Self, StoreError> {
        if max_inflight_proposals == 0
            || max_inflight_proposals > 4096
            || !(1..=20).contains(&max_prepare_attempts)
            || proposal_timeout.is_zero()
        {
            return Err(StoreError::Unavailable(
                "invalid bounded Raft proposal configuration".into(),
            ));
        }
        Ok(Self {
            local,
            raft,
            runtime,
            proposal_admission: Arc::new(tokio::sync::Semaphore::new(max_inflight_proposals)),
            max_prepare_attempts,
            proposal_timeout,
        })
    }

    /// Proposes a pre-materialized authoritative batch through the local Raft
    /// node and waits for quorum commit plus local state-machine apply.
    ///
    /// # Errors
    ///
    /// Returns a leader hint or an unavailable error without bypassing Raft.
    pub fn propose_prepared(
        &self,
        batch: PreparedAtomicBatch,
    ) -> Result<ApplyResponse, StoreError> {
        self.submit(batch, tokio::time::Instant::now() + self.proposal_timeout)
    }

    fn submit(
        &self,
        batch: PreparedAtomicBatch,
        deadline: tokio::time::Instant,
    ) -> Result<ApplyResponse, StoreError> {
        let permit = self.admit()?;
        self.submit_admitted(batch, deadline, permit)
    }

    fn admit(&self) -> Result<tokio::sync::OwnedSemaphorePermit, StoreError> {
        self.proposal_admission
            .clone()
            .try_acquire_owned()
            .map_err(|_| StoreError::Unavailable("Raft proposal admission exhausted".into()))
    }

    fn submit_admitted(
        &self,
        batch: PreparedAtomicBatch,
        deadline: tokio::time::Instant,
        permit: tokio::sync::OwnedSemaphorePermit,
    ) -> Result<ApplyResponse, StoreError> {
        if tokio::time::Instant::now() >= deadline {
            return Err(StoreError::Unavailable(
                "Raft proposal deadline exceeded before submission".into(),
            ));
        }
        let raft = self.raft.clone();
        // A caller deadline is not a rollback. Retain admission until consensus
        // completes, including while the caller retries an indeterminate result.
        let pending = self.runtime.spawn(async move {
            let _permit = permit;
            raft.client_write(batch).await
        });
        self.runtime
            .block_on(async { tokio::time::timeout_at(deadline, pending).await })
            .map_err(|_| {
                StoreError::Unavailable(
                    "Raft proposal timed out; outcome unknown, retry with the same idempotency key"
                        .into(),
                )
            })?
            .map_err(|error| {
                StoreError::Unavailable(format!("Raft proposal task failed: {error}"))
            })?
            .map(|response| response.data)
            .map_err(|error| {
                if let Some(forward) = error.forward_to_leader() {
                    StoreError::NotLeader {
                        leader_id: forward.leader_id,
                        leader_address: forward.leader_node.as_ref().map(|node| node.addr.clone()),
                    }
                } else {
                    StoreError::Unavailable(format!("Raft client write failed: {error}"))
                }
            })
    }

    fn propose_remote_batch(
        &self,
        batch: PreparedAtomicBatch,
    ) -> Result<ApplyResponse, RemoteTaskError> {
        self.propose_prepared(batch).map_err(remote_store_error)
    }
}

impl<C> WorkflowStorePort for RaftWorkflowStore<C>
where
    C: PayloadCryptoPort + 'static,
{
    fn lookup_idempotency(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError> {
        self.local
            .lookup_idempotency(tenant_id, actor_id, idempotency_key, command_id)
    }

    fn load(
        &self,
        tenant_id: &TenantId,
        instance_id: &InstanceId,
    ) -> Result<LoadedInstance, StoreError> {
        self.local.load(tenant_id, instance_id)
    }

    fn commit(&self, request: CommitRequest) -> Result<CommitOutcome, StoreError> {
        let deadline = tokio::time::Instant::now() + self.proposal_timeout;
        for attempt in 0..self.max_prepare_attempts {
            let permit = self.admit()?;
            let batch = match self.local.prepare_workflow_batch(&request)? {
                WorkflowCommitPreparation::AlreadyCommitted(result) => {
                    return Ok(CommitOutcome::Duplicate(result));
                }
                WorkflowCommitPreparation::Proposal(batch) => batch,
            };
            let outbox_condition = batch.preconditions.iter().position(|condition| {
                condition.storage_key.column_family == "outbox_meta"
                    && condition.storage_key.key == b"tail"
            });
            let response = self.submit_admitted(batch, deadline, permit)?;
            // Only a globally allocated outbox cursor can be transparently rebased.
            // Stream version, event identity, audit and idempotency conflicts are final.
            if matches!(response.outcome, ApplyOutcome::PreconditionFailed {condition_index}
            if outbox_condition == Some(condition_index as usize))
                && attempt + 1 < self.max_prepare_attempts
            {
                continue;
            }
            return self.local.resolve_workflow_apply(&request, &response);
        }
        Err(StoreError::Unavailable(
            "Raft workflow prepare budget exhausted".into(),
        ))
    }
}

impl<C> RemoteTaskIngressPort for RaftWorkflowStore<C>
where
    C: PayloadCryptoPort + 'static,
{
    fn enqueue_remote_task(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<RemoteTaskEnqueueOutcome, RemoteTaskError> {
        let task = RemoteTask::try_from(activation)?;
        let Some(batch) = self.local.prepare_remote_task_enqueue(&task)? else {
            return Ok(RemoteTaskEnqueueOutcome::Duplicate);
        };
        match self.propose_remote_batch(batch)?.outcome {
            ApplyOutcome::Applied => Ok(RemoteTaskEnqueueOutcome::Enqueued),
            ApplyOutcome::Duplicate => Ok(RemoteTaskEnqueueOutcome::Duplicate),
            ApplyOutcome::PreconditionFailed { .. } => Err(RemoteTaskError::Conflict),
            ApplyOutcome::Rejected { reason } => Err(RemoteTaskError::Store(reason)),
        }
    }
}

impl<C> RemoteTaskStorePort for RaftWorkflowStore<C>
where
    C: PayloadCryptoPort + 'static,
{
    fn ready_tasks(
        &self,
        now_epoch_ms: u64,
        limit: usize,
    ) -> Result<Vec<RemoteTask>, RemoteTaskError> {
        self.local.ready_remote_tasks(now_epoch_ms, limit)
    }

    fn claim_remote_task(
        &self,
        request: &RemoteTaskClaimRequest,
    ) -> Result<Option<RemoteTaskClaim>, RemoteTaskError> {
        let Some(batch) = self.local.prepare_remote_task_claim(request)? else {
            return Ok(None);
        };
        match self.propose_remote_batch(batch)?.outcome {
            ApplyOutcome::Applied | ApplyOutcome::Duplicate => self
                .local
                .load_remote_task_claim(&request.tenant_id, &request.task_id)
                .and_then(|claim| {
                    claim
                        .filter(|claim| claim.assignment_id == request.assignment_id)
                        .ok_or(RemoteTaskError::Conflict)
                        .map(Some)
                }),
            ApplyOutcome::PreconditionFailed { .. } => Ok(None),
            ApplyOutcome::Rejected { reason } => Err(RemoteTaskError::Store(reason)),
        }
    }

    fn complete_remote_task(&self, claim: &RemoteTaskClaim) -> Result<(), RemoteTaskError> {
        let response =
            self.propose_remote_batch(self.local.prepare_remote_task_complete(claim)?)?;
        remote_mutation_applied(response)
    }

    fn fail_remote_task(
        &self,
        claim: &RemoteTaskClaim,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<RemoteTaskFailureOutcome, RemoteTaskError> {
        let response = self.propose_remote_batch(self.local.prepare_remote_task_failure(
            claim,
            retry_at_epoch_ms,
            dead_letter,
        )?)?;
        remote_mutation_applied(response)?;
        Ok(if dead_letter {
            RemoteTaskFailureOutcome::DeadLettered
        } else {
            RemoteTaskFailureOutcome::RetryScheduled
        })
    }

    fn expired_claims(
        &self,
        now_epoch_ms: u64,
        limit: usize,
    ) -> Result<Vec<RemoteTaskClaim>, RemoteTaskError> {
        self.local.expired_remote_task_claims(now_epoch_ms, limit)
    }
}

fn remote_mutation_applied(response: ApplyResponse) -> Result<(), RemoteTaskError> {
    match response.outcome {
        ApplyOutcome::Applied | ApplyOutcome::Duplicate => Ok(()),
        ApplyOutcome::PreconditionFailed { .. } => Err(RemoteTaskError::Conflict),
        ApplyOutcome::Rejected { reason } => Err(RemoteTaskError::Store(reason)),
    }
}

fn remote_store_error(error: StoreError) -> RemoteTaskError {
    match error {
        StoreError::NotLeader {
            leader_id,
            leader_address,
        } => RemoteTaskError::NotLeader {
            leader_id,
            leader_address,
        },
        other => RemoteTaskError::Store(other.to_string()),
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;
    use std::sync::Arc;
    use std::time::Duration;

    use bpmp_adapter_rocksdb::{RocksDbConfig, RocksDbWorkflowStore};
    use bpmp_contracts::engine::v1::{CommandEnvelope, CommandReceipt};
    use bpmp_engine::{
        CommitOutcome, EngineCommandHandlerPort, EngineError, OutboxStorePort, StoreError,
        TransportError, WorkflowStorePort,
    };
    use bpmp_payload_crypto::{
        CryptoError, EncryptedPayload, EncryptionContext, PayloadCryptoPort,
    };
    use bpmp_raft_state_machine::{
        ApplyOutcome, AtomicStateStorage, AuthoritativeStateMachine, Mutation, PreparedAtomicBatch,
        StateMachineLimits, StorageKey,
    };
    use openraft::Raft;
    use proptest::prelude::*;
    use tokio::net::TcpListener;
    use tokio_stream::wrappers::TcpListenerStream;
    use tonic::transport::Server;

    use super::{
        ForwardingCommandHandler, PeerClientTls, PeerDirectory, RaftPeer, RaftWorkflowStore,
        TonicRaftNetworkFactory, TonicRaftPeerService, TypeConfig,
    };
    use bpmp_transport_observability::RequestMetadataLayer;

    struct TestCrypto;

    fn workflow_request(index: usize) -> bpmp_engine::CommitRequest {
        use bpmp_domain_core::{
            ActorId, CommandId, ConfigVersion, CorrelationId, DomainEvent, IdempotencyKey,
            InstanceId, KeyScope, NodeId, PolicyVersion, TenantId, WorkflowType, WorkflowVersion,
        };
        use bpmp_engine::{
            AuthorizationAudit, CommitRequest, CommittedResult, EventEnvelope, EventMetadata,
        };
        let tenant = TenantId::new("tenant-pipeline").unwrap();
        let instance = InstanceId::new(format!("instance-{index}")).unwrap();
        let actor = ActorId::new("actor-pipeline").unwrap();
        let command = CommandId::new(format!("start-{index}")).unwrap();
        let event_id = format!("start-{index}:1");
        let config = ConfigVersion::new("config-pipeline").unwrap();
        let policy = PolicyVersion::new("policy-pipeline").unwrap();
        let workflow = WorkflowType::new("approval").unwrap();
        let version = WorkflowVersion::new("1").unwrap();
        let scope = KeyScope::new("tenant-pipeline/operational").unwrap();
        let correlation = CorrelationId::new(format!("correlation-{index}")).unwrap();
        CommitRequest {
            tenant_id: tenant.clone(),
            instance_id: instance.clone(),
            actor_id: actor.clone(),
            idempotency_key: IdempotencyKey::new(format!("idem-{index}")).unwrap(),
            command_id: command.clone(),
            expected_version: 0,
            events: vec![EventEnvelope {
                metadata: EventMetadata {
                    event_id: event_id.clone(),
                    tenant_id: tenant.clone(),
                    instance_id: instance.clone(),
                    sequence: 1,
                    schema_version: 1,
                    correlation_id: correlation.clone(),
                    causation_command_id: command.clone(),
                    occurred_at_epoch_ms: 42,
                    config_version: config.clone(),
                    policy_version: policy.clone(),
                    actor_id: actor.clone(),
                    encryption_key_scope: scope.clone(),
                    workflow_type: workflow.clone(),
                    workflow_version: version.clone(),
                },
                event: DomainEvent::WorkflowStarted {
                    tenant_id: tenant.clone(),
                    workflow_type: workflow.clone(),
                    workflow_version: version.clone(),
                    start_node_id: NodeId::new("start").unwrap(),
                    occurred_at_epoch_ms: 42,
                },
            }],
            snapshot: None,
            authorization_audit: AuthorizationAudit {
                decision_id: format!("allow-{index}"),
                tenant_id: tenant,
                actor_id: actor,
                workload_id: "api-gateway".into(),
                roles: vec!["operator".into()],
                action: "START".into(),
                workflow_type: workflow,
                workflow_version: version,
                instance_id: instance,
                active_node_id: "start".into(),
                policy_version: policy.clone(),
                config_version: config.clone(),
                bundle_sequence: 1,
                revoke_epoch: 1,
                occurred_at_epoch_ms: 42,
                command_id: command,
                correlation_id: correlation,
                matched_grant_ids: vec!["allow-start".into()],
                encryption_key_scope: scope,
            },
            result: CommittedResult {
                version: 1,
                event_ids: vec![event_id],
                config_version: config,
                policy_version: policy,
            },
        }
    }

    impl PayloadCryptoPort for TestCrypto {
        fn encrypt(
            &self,
            context: EncryptionContext<'_>,
            plaintext: &[u8],
        ) -> Result<EncryptedPayload, CryptoError> {
            Ok(EncryptedPayload {
                key_scope: context.key_scope.clone(),
                key_version: "test-v1".into(),
                key_epoch: 1,
                algorithm: "test".into(),
                nonce: vec![1],
                ciphertext: plaintext.to_vec(),
            })
        }

        fn decrypt(
            &self,
            _associated_data: &[u8],
            payload: &EncryptedPayload,
        ) -> Result<Vec<u8>, CryptoError> {
            Ok(payload.ciphertext.clone())
        }
    }

    struct ReceiptHandler;

    impl EngineCommandHandlerPort for ReceiptHandler {
        fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError> {
            Ok(CommandReceipt {
                command_id: envelope.command_id,
                committed_sequence: 1,
                duplicate: false,
            })
        }
    }

    struct FollowerHandler;

    impl EngineCommandHandlerPort for FollowerHandler {
        fn handle(&self, _envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError> {
            Err(TransportError::Engine(EngineError::Store(
                StoreError::NotLeader {
                    leader_id: Some(1),
                    leader_address: Some("leader".into()),
                },
            )))
        }
    }

    fn insecure_directory(peers: Vec<RaftPeer>) -> PeerDirectory {
        PeerDirectory {
            peers: Arc::new(peers.into_iter().map(|peer| (peer.node_id, peer)).collect()),
            tls: Arc::new(PeerClientTls {
                ca: Vec::new(),
                certificate: Vec::new(),
                private_key: Vec::new(),
            }),
            rpc_timeout: Duration::from_secs(2),
            max_message_bytes: 32 * 1024 * 1024,
            insecure_test_transport: true,
        }
    }

    fn limits() -> StateMachineLimits {
        StateMachineLimits {
            max_snapshot_bytes: 64 * 1024 * 1024,
            snapshot_directory: std::env::temp_dir(),
            max_conditions: 16,
            max_mutations: 16,
            max_batch_bytes: 64 * 1024,
            append_only_column_families: BTreeSet::new(),
        }
    }

    fn batch() -> PreparedAtomicBatch {
        PreparedAtomicBatch::new(
            "peer-rpc-command".into(),
            b"peer-rpc-scope".to_vec(),
            Vec::new(),
            vec![Mutation::Put {
                storage_key: StorageKey {
                    column_family: "stream_meta".into(),
                    key: b"cluster-e2e".to_vec(),
                },
                value: b"quorum".to_vec(),
            }],
            b"committed".to_vec(),
        )
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 6)]
    #[allow(clippy::too_many_lines)]
    async fn three_node_peer_rpc_replicates_membership_and_quorum_write() {
        let mut listeners = Vec::new();
        let mut peers = Vec::new();
        for node_id in 1..=3 {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            peers.push(RaftPeer {
                node_id,
                address: listener.local_addr().unwrap().to_string(),
                tls_domain: "localhost".into(),
            });
            listeners.push(listener);
        }
        let directory = insecure_directory(peers);
        let raft_config = Arc::new(
            openraft::Config {
                heartbeat_interval: 40,
                election_timeout_min: 120,
                election_timeout_max: 240,
                ..Default::default()
            }
            .validate()
            .unwrap(),
        );
        let mut nodes = Vec::new();
        let mut workflow_stores = Vec::new();
        let mut state_stores = Vec::new();
        let mut data_directories = Vec::new();
        for node_id in 1..=3 {
            let data_directory = tempfile::tempdir().unwrap();
            let workflow_store = Arc::new(
                RocksDbWorkflowStore::open(
                    RocksDbConfig {
                        path: data_directory.path().to_path_buf(),
                        max_open_files: 64,
                        write_buffer_size_bytes: 1024 * 1024,
                        max_background_jobs: 2,
                        max_replay_events: 1_000,
                    },
                    TestCrypto,
                )
                .unwrap(),
            );
            let state_store = workflow_store
                .authoritative_state_storage(1024 * 1024)
                .unwrap();
            let raft = Raft::<TypeConfig>::new(
                node_id,
                Arc::clone(&raft_config),
                TonicRaftNetworkFactory::new(node_id, directory.clone()),
                workflow_store.raft_log_storage(),
                AuthoritativeStateMachine::new(state_store.clone(), limits()).unwrap(),
            )
            .await
            .unwrap();
            nodes.push(raft);
            workflow_stores.push(workflow_store);
            state_stores.push(state_store);
            data_directories.push(data_directory);
        }
        let mut servers = Vec::new();
        for (listener, raft) in listeners.into_iter().zip(nodes.iter().cloned()) {
            let service =
                TonicRaftPeerService::new(raft, Arc::new(ReceiptHandler), directory.clone())
                    .into_server(64 * 1024, 64 * 1024);
            servers.push(tokio::spawn(async move {
                Server::builder()
                    .layer(RequestMetadataLayer::new("bpmp-engine-raft-test"))
                    .add_service(service)
                    .serve_with_incoming(TcpListenerStream::new(listener))
                    .await
            }));
        }
        nodes[0].initialize(directory.membership()).await.unwrap();
        let leader_id = tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                if let Some(leader_id) = nodes[0].metrics().borrow().current_leader {
                    break leader_id;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        let response = nodes[usize::try_from(leader_id - 1).unwrap()]
            .client_write(batch())
            .await
            .unwrap();
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                if nodes.iter().all(|node| {
                    node.metrics()
                        .borrow()
                        .last_applied
                        .is_some_and(|log_id| log_id.index >= response.log_id.index)
                }) {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        let leader_index = usize::try_from(leader_id - 1).unwrap();
        let pipeline = Arc::new(
            RaftWorkflowStore::new(
                workflow_stores[leader_index].clone(),
                nodes[leader_index].clone(),
                tokio::runtime::Handle::current(),
                4,
                4,
                Duration::from_secs(5),
            )
            .unwrap(),
        );
        let permits = pipeline.proposal_admission.try_acquire_many(4).unwrap();
        assert!(pipeline.propose_prepared(batch()).is_err());
        drop(permits);
        let mut proposals = Vec::new();
        for index in 0..4 {
            let pipeline = pipeline.clone();
            proposals.push(tokio::task::spawn_blocking(move || {
                let command = format!("concurrent-{index}");
                let prepared = PreparedAtomicBatch::new(
                    command.clone(),
                    command.as_bytes().to_vec(),
                    Vec::new(),
                    vec![Mutation::Put {
                        storage_key: StorageKey {
                            column_family: "stream_meta".into(),
                            key: command.as_bytes().to_vec(),
                        },
                        value: command.as_bytes().to_vec(),
                    }],
                    Vec::new(),
                );
                pipeline.propose_prepared(prepared).unwrap()
            }));
        }
        for proposal in proposals {
            assert_eq!(proposal.await.unwrap().outcome, ApplyOutcome::Applied);
        }
        let mut commands = Vec::new();
        for index in 0..4 {
            let pipeline = pipeline.clone();
            commands.push(tokio::task::spawn_blocking(move || {
                pipeline.commit(workflow_request(index)).unwrap()
            }));
        }
        for command in commands {
            assert!(matches!(
                command.await.unwrap(),
                CommitOutcome::Committed(_)
            ));
        }
        let records = workflow_stores[leader_index].read_after(0, 10).unwrap();
        assert_eq!(records.len(), 4);
        for index in 0..4 {
            let pipeline = pipeline.clone();
            assert!(matches!(
                tokio::task::spawn_blocking(move || pipeline
                    .commit(workflow_request(index))
                    .unwrap())
                .await
                .unwrap(),
                CommitOutcome::Duplicate(_)
            ));
        }
        assert_eq!(
            workflow_stores[leader_index]
                .read_after(0, 10)
                .unwrap()
                .len(),
            4
        );
        let mut contenders = Vec::new();
        for index in 10..12 {
            let pipeline = pipeline.clone();
            let mut request = workflow_request(index);
            request.instance_id = bpmp_domain_core::InstanceId::new("contended-instance").unwrap();
            request.events[0].metadata.instance_id = request.instance_id.clone();
            request.authorization_audit.instance_id = request.instance_id.clone();
            contenders.push(tokio::task::spawn_blocking(move || {
                pipeline.commit(request)
            }));
        }
        let left = contenders.remove(0).await.unwrap();
        let right = contenders.remove(0).await.unwrap();
        assert_eq!(
            [&left, &right]
                .iter()
                .filter(|result| matches!(result, Ok(CommitOutcome::Committed(_))))
                .count(),
            1
        );
        assert_eq!(
            [&left, &right]
                .iter()
                .filter(|result| matches!(result, Err(StoreError::VersionConflict { .. })))
                .count(),
            1
        );
        assert_eq!(
            workflow_stores[leader_index]
                .read_after(0, 10)
                .unwrap()
                .len(),
            5
        );
        for state_store in state_stores {
            let snapshot = state_store.export_snapshot().unwrap();
            assert!(contains_bytes(
                &serde_json::from_slice(&snapshot).unwrap(),
                b"quorum"
            ));
        }
        let deadline_pipeline = Arc::new(
            RaftWorkflowStore::new(
                workflow_stores[leader_index].clone(),
                nodes[leader_index].clone(),
                tokio::runtime::Handle::current(),
                1,
                1,
                Duration::from_millis(50),
            )
            .unwrap(),
        );
        let before = workflow_stores[leader_index]
            .read_after(0, 10)
            .unwrap()
            .len();
        let expired = deadline_pipeline.submit(
            batch(),
            tokio::time::Instant::now() - Duration::from_millis(1),
        );
        assert!(
            matches!(expired, Err(StoreError::Unavailable(message)) if message.contains("before submission"))
        );
        assert_eq!(
            workflow_stores[leader_index]
                .read_after(0, 10)
                .unwrap()
                .len(),
            before
        );
        for (index, node) in nodes.iter().enumerate() {
            if index != leader_index {
                node.shutdown().await.unwrap();
            }
        }
        let caller = deadline_pipeline.clone();
        let outcome = tokio::task::spawn_blocking(move || caller.propose_prepared(batch()))
            .await
            .unwrap();
        assert!(
            matches!(outcome, Err(StoreError::Unavailable(message)) if message.contains("outcome unknown"))
        );
        assert_eq!(deadline_pipeline.proposal_admission.available_permits(), 0);
        assert!(matches!(deadline_pipeline.propose_prepared(batch()),
            Err(StoreError::Unavailable(message)) if message.contains("admission exhausted")));
        nodes[leader_index].shutdown().await.unwrap();
        tokio::time::timeout(Duration::from_secs(2), async {
            while deadline_pipeline.proposal_admission.available_permits() == 0 {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        for server in servers {
            server.abort();
        }
        drop(data_directories);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    #[allow(clippy::too_many_lines)]
    async fn large_snapshot_crosses_bounded_peer_rpc_chunks_into_rocksdb() {
        use bpmp_raft_state_machine::StateMachineMetadata;
        use openraft::{RaftNetwork as _, RaftNetworkFactory as _, RaftSnapshotBuilder as _};
        use tokio::io::AsyncReadExt as _;
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let mut directory = insecure_directory(vec![
            RaftPeer {
                node_id: 1,
                address: "127.0.0.1:1".into(),
                tls_domain: "localhost".into(),
            },
            RaftPeer {
                node_id: 2,
                address: listener.local_addr().unwrap().to_string(),
                tls_domain: "localhost".into(),
            },
        ]);
        directory.max_message_bytes = 512 * 1024;
        let data = tempfile::tempdir().unwrap();
        let store = Arc::new(
            RocksDbWorkflowStore::open(
                RocksDbConfig {
                    path: data.path().to_owned(),
                    max_open_files: 64,
                    write_buffer_size_bytes: 1024 * 1024,
                    max_background_jobs: 2,
                    max_replay_events: 100,
                },
                TestCrypto,
            )
            .unwrap(),
        );
        let state_store = store.authoritative_state_storage(16 * 1024 * 1024).unwrap();
        let raft = Raft::<TypeConfig>::new(
            2,
            Arc::new(openraft::Config::default().validate().unwrap()),
            TonicRaftNetworkFactory::new(2, directory.clone()),
            store.raft_log_storage(),
            AuthoritativeStateMachine::new(state_store.clone(), limits()).unwrap(),
        )
        .await
        .unwrap();
        let service =
            TonicRaftPeerService::new(raft.clone(), Arc::new(ReceiptHandler), directory.clone())
                .into_server(directory.max_message_bytes, directory.max_message_bytes);
        let server = tokio::spawn(async move {
            Server::builder()
                .layer(RequestMetadataLayer::new("bpmp-snapshot-test"))
                .add_service(service)
                .serve_with_incoming(TcpListenerStream::new(listener))
                .await
        });
        let source_directory = tempfile::tempdir().unwrap();
        let source_store = RocksDbWorkflowStore::open(
            RocksDbConfig {
                path: source_directory.path().to_owned(),
                max_open_files: 64,
                write_buffer_size_bytes: 1024 * 1024,
                max_background_jobs: 2,
                max_replay_events: 100,
            },
            TestCrypto,
        )
        .unwrap();
        let source = source_store
            .authoritative_state_storage(16 * 1024 * 1024)
            .unwrap();
        let log_id = openraft::LogId::new(openraft::CommittedLeaderId::new(1, 1), 1);
        let metadata = StateMachineMetadata {
            last_applied: Some(log_id),
            membership: openraft::StoredMembership::new(
                Some(log_id),
                openraft::Membership::new(vec![BTreeSet::from([1, 2])], directory.membership()),
            ),
        };
        let mut bounds = limits();
        bounds.max_batch_bytes = 2 * 1024 * 1024;
        let prepared = PreparedAtomicBatch::new(
            "snapshot-large".into(),
            b"snapshot-large".to_vec(),
            Vec::new(),
            vec![Mutation::Put {
                storage_key: StorageKey {
                    column_family: "stream_meta".into(),
                    key: b"large".to_vec(),
                },
                value: vec![255; 400_000],
            }],
            Vec::new(),
        );
        source.apply(&prepared, &bounds, &metadata).unwrap();
        let mut machine = AuthoritativeStateMachine::new(source, bounds).unwrap();
        let mut snapshot = machine.build_snapshot().await.unwrap();
        let mut bytes = Vec::new();
        snapshot.snapshot.read_to_end(&mut bytes).await.unwrap();
        assert!(bytes.len() > directory.max_message_bytes);
        let mut factory = TonicRaftNetworkFactory::new(1, directory);
        let mut network = factory
            .new_client(2, &openraft::BasicNode::new("unused"))
            .await;
        let mut offset = 0;
        for chunk in bytes.chunks(64 * 1024) {
            let end = offset + chunk.len();
            network
                .install_snapshot(
                    openraft::raft::InstallSnapshotRequest {
                        vote: openraft::Vote::new_committed(1, 1),
                        meta: snapshot.meta.clone(),
                        offset: offset as u64,
                        data: chunk.to_vec(),
                        done: end == bytes.len(),
                    },
                    openraft::network::RPCOption::new(Duration::from_secs(5)),
                )
                .await
                .unwrap();
            offset = end;
        }
        assert_eq!(state_store.load_metadata().unwrap(), metadata);
        let restored: serde_json::Value =
            serde_json::from_slice(&state_store.export_snapshot().unwrap()).unwrap();
        assert!(contains_bytes(&restored, &vec![255; 400_000]));
        raft.shutdown().await.unwrap();
        server.abort();
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn follower_forwards_original_command_once_to_leader_peer_service() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let directory = insecure_directory(vec![RaftPeer {
            node_id: 1,
            address: listener.local_addr().unwrap().to_string(),
            tls_domain: "localhost".into(),
        }]);
        let data_directory = tempfile::tempdir().unwrap();
        let workflow_store = Arc::new(
            RocksDbWorkflowStore::open(
                RocksDbConfig {
                    path: data_directory.path().to_path_buf(),
                    max_open_files: 64,
                    write_buffer_size_bytes: 1024 * 1024,
                    max_background_jobs: 2,
                    max_replay_events: 100,
                },
                TestCrypto,
            )
            .unwrap(),
        );
        let raft = Raft::<TypeConfig>::new(
            1,
            Arc::new(openraft::Config::default().validate().unwrap()),
            TonicRaftNetworkFactory::new(1, directory.clone()),
            workflow_store.raft_log_storage(),
            AuthoritativeStateMachine::new(
                workflow_store
                    .authoritative_state_storage(1024 * 1024)
                    .unwrap(),
                limits(),
            )
            .unwrap(),
        )
        .await
        .unwrap();
        let service = TonicRaftPeerService::new(raft, Arc::new(ReceiptHandler), directory.clone())
            .into_server(64 * 1024, 64 * 1024);
        let server = tokio::spawn(async move {
            Server::builder()
                .layer(RequestMetadataLayer::new("bpmp-engine-raft-test"))
                .add_service(service)
                .serve_with_incoming(TcpListenerStream::new(listener))
                .await
        });
        let handler = Arc::new(ForwardingCommandHandler::new(
            Arc::new(FollowerHandler),
            directory,
            tokio::runtime::Handle::current(),
        ));
        let receipt = tokio::task::spawn_blocking(move || {
            handler.handle(CommandEnvelope {
                command_id: "forwarded-command".into(),
                ..Default::default()
            })
        })
        .await
        .unwrap()
        .unwrap();
        assert_eq!(receipt.command_id, "forwarded-command");
        server.abort();
    }

    fn contains_bytes(value: &serde_json::Value, expected: &[u8]) -> bool {
        match value {
            serde_json::Value::Array(values) => {
                let bytes = values
                    .iter()
                    .map(serde_json::Value::as_u64)
                    .collect::<Option<Vec<_>>>()
                    .and_then(|values| {
                        values
                            .into_iter()
                            .map(u8::try_from)
                            .collect::<Result<Vec<_>, _>>()
                            .ok()
                    });
                bytes.as_deref() == Some(expected)
                    || values.iter().any(|value| contains_bytes(value, expected))
            }
            serde_json::Value::Object(values) => {
                values.values().any(|value| contains_bytes(value, expected))
            }
            _ => false,
        }
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(128))]

        // Feature: rust-bpm-platform, Property 23: quorum before acknowledgement and partition safety
        #[test]
        fn quorum_model_never_acknowledges_a_minority(
            voter_count in 1_usize..16,
            acknowledgements in proptest::collection::vec(any::<bool>(), 1..16),
        ) {
            let acknowledgements = acknowledgements
                .into_iter()
                .take(voter_count)
                .filter(|acknowledged| *acknowledged)
                .count();
            let quorum = voter_count / 2 + 1;
            let may_acknowledge = acknowledgements >= quorum;

            prop_assert_eq!(may_acknowledge, acknowledgements.saturating_mul(2) > voter_count);
        }
    }
}
