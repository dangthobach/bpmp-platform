use std::collections::{BTreeMap, BTreeSet};
use std::fmt::Debug;
use std::io;
use std::ops::RangeBounds;
use std::sync::Arc;
use std::time::Duration;

use bpmp_raft_state_machine::{
    AuthoritativeStateMachine, InMemoryAtomicStateStorage, Mutation, PreparedAtomicBatch,
    StateMachineLimits, StorageKey, TypeConfig,
};
use openraft::error::{NetworkError, RPCError, RaftError, RemoteError};
use openraft::network::RPCOption;
use openraft::raft::{
    AppendEntriesRequest, AppendEntriesResponse, InstallSnapshotRequest, InstallSnapshotResponse,
    VoteRequest, VoteResponse,
};
use openraft::storage::{LogFlushed, RaftLogStorage};
use openraft::{
    BasicNode, LogId, LogState, Raft, RaftLogId, RaftLogReader, RaftNetwork, RaftNetworkFactory,
    RaftTypeConfig, StorageError, Vote,
};
use tokio::sync::{Mutex, RwLock};

type TestRaft = Raft<TypeConfig>;
type TestRpcError<E = openraft::error::Infallible> = RPCError<u64, BasicNode, RaftError<u64, E>>;

#[derive(Clone, Debug, Default)]
struct MemoryLogStore<C: RaftTypeConfig> {
    inner: Arc<Mutex<MemoryLogState<C>>>,
}

#[derive(Debug)]
struct MemoryLogState<C: RaftTypeConfig> {
    last_purged: Option<LogId<C::NodeId>>,
    log: BTreeMap<u64, C::Entry>,
    committed: Option<LogId<C::NodeId>>,
    vote: Option<Vote<C::NodeId>>,
}

impl<C: RaftTypeConfig> Default for MemoryLogState<C> {
    fn default() -> Self {
        Self {
            last_purged: None,
            log: BTreeMap::new(),
            committed: None,
            vote: None,
        }
    }
}

impl<C: RaftTypeConfig> RaftLogReader<C> for MemoryLogStore<C>
where
    C::Entry: Clone,
{
    async fn try_get_log_entries<RB: RangeBounds<u64> + Clone + Debug>(
        &mut self,
        range: RB,
    ) -> Result<Vec<C::Entry>, StorageError<C::NodeId>> {
        let state = self.inner.lock().await;
        Ok(state
            .log
            .range(range)
            .map(|(_, entry)| entry.clone())
            .collect())
    }
}

impl<C: RaftTypeConfig> RaftLogStorage<C> for MemoryLogStore<C>
where
    C::Entry: Clone,
{
    type LogReader = Self;

    async fn get_log_state(&mut self) -> Result<LogState<C>, StorageError<C::NodeId>> {
        let state = self.inner.lock().await;
        let last_log_id = state
            .log
            .last_key_value()
            .map(|(_, entry)| entry.get_log_id().clone())
            .or_else(|| state.last_purged.clone());
        Ok(LogState {
            last_purged_log_id: state.last_purged.clone(),
            last_log_id,
        })
    }

    async fn save_committed(
        &mut self,
        committed: Option<LogId<C::NodeId>>,
    ) -> Result<(), StorageError<C::NodeId>> {
        self.inner.lock().await.committed = committed;
        Ok(())
    }

    async fn read_committed(
        &mut self,
    ) -> Result<Option<LogId<C::NodeId>>, StorageError<C::NodeId>> {
        Ok(self.inner.lock().await.committed.clone())
    }

    async fn save_vote(&mut self, vote: &Vote<C::NodeId>) -> Result<(), StorageError<C::NodeId>> {
        self.inner.lock().await.vote = Some(vote.clone());
        Ok(())
    }

    async fn read_vote(&mut self) -> Result<Option<Vote<C::NodeId>>, StorageError<C::NodeId>> {
        Ok(self.inner.lock().await.vote.clone())
    }

    async fn append<I>(
        &mut self,
        entries: I,
        callback: LogFlushed<C>,
    ) -> Result<(), StorageError<C::NodeId>>
    where
        I: IntoIterator<Item = C::Entry>,
    {
        let mut state = self.inner.lock().await;
        for entry in entries {
            state.log.insert(entry.get_log_id().index, entry);
        }
        callback.log_io_completed(Ok(()));
        Ok(())
    }

    async fn truncate(&mut self, log_id: LogId<C::NodeId>) -> Result<(), StorageError<C::NodeId>> {
        let mut state = self.inner.lock().await;
        let keys = state
            .log
            .range(log_id.index..)
            .map(|(key, _)| *key)
            .collect::<Vec<_>>();
        for key in keys {
            state.log.remove(&key);
        }
        Ok(())
    }

    async fn purge(&mut self, log_id: LogId<C::NodeId>) -> Result<(), StorageError<C::NodeId>> {
        let mut state = self.inner.lock().await;
        state.last_purged = Some(log_id.clone());
        let keys = state
            .log
            .range(..=log_id.index)
            .map(|(key, _)| *key)
            .collect::<Vec<_>>();
        for key in keys {
            state.log.remove(&key);
        }
        Ok(())
    }

    async fn get_log_reader(&mut self) -> Self::LogReader {
        self.clone()
    }
}

#[derive(Clone, Default)]
struct NetworkState {
    nodes: Arc<RwLock<BTreeMap<u64, TestRaft>>>,
    blocked: Arc<RwLock<BTreeSet<(u64, u64)>>>,
}

#[derive(Clone)]
struct NetworkFactory {
    source: u64,
    state: NetworkState,
}

struct Connection {
    source: u64,
    target: u64,
    state: NetworkState,
}

impl RaftNetworkFactory<TypeConfig> for NetworkFactory {
    type Network = Connection;

    async fn new_client(&mut self, target: u64, _node: &BasicNode) -> Self::Network {
        Connection {
            source: self.source,
            target,
            state: self.state.clone(),
        }
    }
}

impl Connection {
    async fn target<E>(&self) -> Result<TestRaft, TestRpcError<E>>
    where
        E: std::error::Error,
    {
        if self
            .state
            .blocked
            .read()
            .await
            .contains(&(self.source, self.target))
        {
            return Err(RPCError::Network(NetworkError::new(&io::Error::new(
                io::ErrorKind::ConnectionRefused,
                "injected network partition",
            ))));
        }
        self.state
            .nodes
            .read()
            .await
            .get(&self.target)
            .cloned()
            .ok_or_else(|| {
                RPCError::Network(NetworkError::new(&io::Error::new(
                    io::ErrorKind::NotConnected,
                    "target node unavailable",
                )))
            })
    }
}

impl RaftNetwork<TypeConfig> for Connection {
    async fn append_entries(
        &mut self,
        request: AppendEntriesRequest<TypeConfig>,
        _option: RPCOption,
    ) -> Result<AppendEntriesResponse<u64>, TestRpcError> {
        self.target()
            .await?
            .append_entries(request)
            .await
            .map_err(|error| RPCError::RemoteError(RemoteError::new(self.target, error)))
    }

    async fn install_snapshot(
        &mut self,
        request: InstallSnapshotRequest<TypeConfig>,
        _option: RPCOption,
    ) -> Result<InstallSnapshotResponse<u64>, TestRpcError<openraft::error::InstallSnapshotError>>
    {
        self.target()
            .await?
            .install_snapshot(request)
            .await
            .map_err(|error| RPCError::RemoteError(RemoteError::new(self.target, error)))
    }

    async fn vote(
        &mut self,
        request: VoteRequest<u64>,
        _option: RPCOption,
    ) -> Result<VoteResponse<u64>, TestRpcError> {
        self.target()
            .await?
            .vote(request)
            .await
            .map_err(|error| RPCError::RemoteError(RemoteError::new(self.target, error)))
    }
}

fn limits() -> StateMachineLimits {
    StateMachineLimits {
        max_conditions: 32,
        max_mutations: 32,
        max_batch_bytes: 64 * 1024,
        append_only_column_families: BTreeSet::new(),
    }
}

fn key(name: &str) -> StorageKey {
    StorageKey {
        column_family: "stream_meta".into(),
        key: name.as_bytes().to_vec(),
    }
}

fn command(id: &str, record: &str, value: &[u8]) -> PreparedAtomicBatch {
    PreparedAtomicBatch::new(
        id.into(),
        format!("tenant-a/{id}").into_bytes(),
        Vec::new(),
        vec![Mutation::Put {
            storage_key: key(record),
            value: value.to_vec(),
        }],
        value.to_vec(),
    )
}

async fn wait_for_leader(nodes: &[TestRaft], allowed: &BTreeSet<u64>) -> u64 {
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            for node in nodes {
                if let Some(leader) = node.metrics().borrow().current_leader
                    && allowed.contains(&leader)
                {
                    return leader;
                }
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("leader election timed out")
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn minority_partition_cannot_commit_and_cluster_converges_after_failover() {
    let network = NetworkState::default();
    let config = Arc::new(
        openraft::Config {
            heartbeat_interval: 50,
            election_timeout_min: 150,
            election_timeout_max: 300,
            ..Default::default()
        }
        .validate()
        .unwrap(),
    );
    let mut nodes = Vec::new();
    let mut storages = Vec::new();
    for node_id in 1..=3 {
        let storage = InMemoryAtomicStateStorage::default();
        let raft = Raft::new(
            node_id,
            Arc::clone(&config),
            NetworkFactory {
                source: node_id,
                state: network.clone(),
            },
            MemoryLogStore::<TypeConfig>::default(),
            AuthoritativeStateMachine::new(storage.clone(), limits()).unwrap(),
        )
        .await
        .unwrap();
        network.nodes.write().await.insert(node_id, raft.clone());
        nodes.push(raft);
        storages.push(storage);
    }

    nodes[0]
        .initialize(BTreeMap::from([(1, BasicNode::new("node-1"))]))
        .await
        .unwrap();
    nodes[0]
        .add_learner(2, BasicNode::new("node-2"), true)
        .await
        .unwrap();
    nodes[0]
        .add_learner(3, BasicNode::new("node-3"), true)
        .await
        .unwrap();
    nodes[0]
        .change_membership(BTreeSet::from([1, 2, 3]), false)
        .await
        .unwrap();
    nodes[0]
        .client_write(command("before-partition", "stable", b"stable"))
        .await
        .unwrap();

    {
        let mut blocked = network.blocked.write().await;
        for peer in [2, 3] {
            blocked.insert((1, peer));
            blocked.insert((peer, 1));
        }
    }
    let minority_write = tokio::time::timeout(
        Duration::from_millis(700),
        nodes[0].client_write(command("minority", "minority", b"must-not-commit")),
    )
    .await;
    assert!(minority_write.is_err() || minority_write.unwrap().is_err());
    assert_eq!(storages[0].get(&key("minority")).unwrap(), None);

    let majority_leader = wait_for_leader(&nodes[1..], &BTreeSet::from([2, 3])).await;
    nodes[usize::try_from(majority_leader - 1).unwrap()]
        .client_write(command("majority", "committed", b"quorum"))
        .await
        .unwrap();

    network.blocked.write().await.clear();
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            if storages.iter().all(|storage| {
                storage.get(&key("stable")).unwrap() == Some(b"stable".to_vec())
                    && storage.get(&key("committed")).unwrap() == Some(b"quorum".to_vec())
                    && storage.get(&key("minority")).unwrap().is_none()
            }) {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("healed cluster did not converge");

    for raft in nodes {
        raft.shutdown().await.unwrap();
    }
}
