#![cfg(target_os = "linux")]

use std::collections::{BTreeMap, BTreeSet};
use std::io;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use bpmp_adapter_rocksdb::{RocksDbConfig, RocksDbWorkflowStore};
use bpmp_payload_crypto::{CryptoError, EncryptedPayload, EncryptionContext, PayloadCryptoPort};
use bpmp_raft_state_machine::{
    ApplyOutcome, AuthoritativeStateMachine, Mutation, PreparedAtomicBatch, StateMachineLimits,
    StorageKey, TypeConfig,
};
use openraft::error::{NetworkError, RPCError, RaftError};
use openraft::network::RPCOption;
use openraft::raft::{
    AppendEntriesRequest, AppendEntriesResponse, InstallSnapshotRequest, InstallSnapshotResponse,
    VoteRequest, VoteResponse,
};
use openraft::{BasicNode, Raft, RaftNetwork, RaftNetworkFactory};

type TestRpcError<E = openraft::error::Infallible> = RPCError<u64, BasicNode, RaftError<u64, E>>;

#[derive(Clone, Copy)]
struct UnusedCrypto;

impl PayloadCryptoPort for UnusedCrypto {
    fn encrypt(
        &self,
        _context: EncryptionContext<'_>,
        _plaintext: &[u8],
    ) -> Result<EncryptedPayload, CryptoError> {
        Err(CryptoError::KeyUnavailable)
    }

    fn decrypt(
        &self,
        _associated_data: &[u8],
        _payload: &EncryptedPayload,
    ) -> Result<Vec<u8>, CryptoError> {
        Err(CryptoError::KeyUnavailable)
    }
}

#[derive(Clone, Copy)]
struct SingleNodeNetworkFactory;

struct UnreachablePeer;

impl RaftNetworkFactory<TypeConfig> for SingleNodeNetworkFactory {
    type Network = UnreachablePeer;

    async fn new_client(&mut self, _target: u64, _node: &BasicNode) -> Self::Network {
        UnreachablePeer
    }
}

impl RaftNetwork<TypeConfig> for UnreachablePeer {
    async fn append_entries(
        &mut self,
        _rpc: AppendEntriesRequest<TypeConfig>,
        _option: RPCOption,
    ) -> Result<AppendEntriesResponse<u64>, TestRpcError> {
        Err(unreachable())
    }

    async fn install_snapshot(
        &mut self,
        _rpc: InstallSnapshotRequest<TypeConfig>,
        _option: RPCOption,
    ) -> Result<InstallSnapshotResponse<u64>, TestRpcError<openraft::error::InstallSnapshotError>>
    {
        Err(unreachable())
    }

    async fn vote(
        &mut self,
        _rpc: VoteRequest<u64>,
        _option: RPCOption,
    ) -> Result<VoteResponse<u64>, TestRpcError> {
        Err(unreachable())
    }
}

fn unreachable<E>() -> TestRpcError<E>
where
    E: std::error::Error,
{
    RPCError::Network(NetworkError::new(&io::Error::new(
        io::ErrorKind::NotConnected,
        "single-node test has no remote peers",
    )))
}

fn rocks_config(path: &Path) -> RocksDbConfig {
    RocksDbConfig {
        path: path.to_owned(),
        max_open_files: 64,
        write_buffer_size_bytes: 1024 * 1024,
        max_background_jobs: 2,
        max_replay_events: 128,
    }
}

fn state_machine_limits() -> StateMachineLimits {
    StateMachineLimits {
        max_conditions: 16,
        max_mutations: 16,
        max_batch_bytes: 64 * 1024,
        append_only_column_families: BTreeSet::new(),
    }
}

fn command(command_id: &str, key: &str, value: &[u8]) -> PreparedAtomicBatch {
    PreparedAtomicBatch::new(
        command_id.into(),
        format!("tenant-a/{command_id}").into_bytes(),
        Vec::new(),
        vec![Mutation::Put {
            storage_key: StorageKey {
                column_family: "stream_meta".into(),
                key: key.as_bytes().to_vec(),
            },
            value: value.to_vec(),
        }],
        value.to_vec(),
    )
}

fn raft_config() -> Arc<openraft::Config> {
    Arc::new(
        openraft::Config {
            heartbeat_interval: 50,
            election_timeout_min: 150,
            election_timeout_max: 300,
            ..Default::default()
        }
        .validate()
        .unwrap(),
    )
}

async fn wait_for_leader(raft: &Raft<TypeConfig>) {
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            if raft.metrics().borrow().current_leader == Some(1) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("single-node leader election timed out");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn client_write_recovers_log_vote_commit_and_state_after_restart() {
    let directory = tempfile::tempdir().unwrap();
    let config = raft_config();

    {
        let store =
            RocksDbWorkflowStore::open(rocks_config(directory.path()), UnusedCrypto).unwrap();
        let raft = Raft::new(
            1,
            Arc::clone(&config),
            SingleNodeNetworkFactory,
            store.raft_log_storage(),
            AuthoritativeStateMachine::new(
                store.authoritative_state_storage(1024 * 1024).unwrap(),
                state_machine_limits(),
            )
            .unwrap(),
        )
        .await
        .unwrap();
        raft.initialize(BTreeMap::from([(1, BasicNode::new("127.0.0.1:19001"))]))
            .await
            .unwrap();
        wait_for_leader(&raft).await;

        let response = raft
            .client_write(command("before-restart", "first", b"committed-before"))
            .await
            .unwrap();
        assert_eq!(response.data.outcome, ApplyOutcome::Applied);
        raft.shutdown().await.unwrap();
    }

    let reopened =
        RocksDbWorkflowStore::open(rocks_config(directory.path()), UnusedCrypto).unwrap();
    let raft = Raft::new(
        1,
        Arc::clone(&config),
        SingleNodeNetworkFactory,
        reopened.raft_log_storage(),
        AuthoritativeStateMachine::new(
            reopened.authoritative_state_storage(1024 * 1024).unwrap(),
            state_machine_limits(),
        )
        .unwrap(),
    )
    .await
    .unwrap();
    wait_for_leader(&raft).await;

    let duplicate = raft
        .client_write(command("before-restart", "first", b"committed-before"))
        .await
        .unwrap();
    assert_eq!(duplicate.data.outcome, ApplyOutcome::Duplicate);
    let after = raft
        .client_write(command("after-restart", "second", b"committed-after"))
        .await
        .unwrap();
    assert_eq!(after.data.outcome, ApplyOutcome::Applied);
    assert!(raft.metrics().borrow().last_applied.is_some());
    raft.shutdown().await.unwrap();
}
