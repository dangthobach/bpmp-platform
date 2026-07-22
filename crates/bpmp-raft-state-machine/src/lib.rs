//! `OpenRaft` state machine for exact, bounded authoritative write batches.
//!
//! Leaders prepare ciphertext and immutable metadata once. Raft replicates the
//! resulting bytes, and every node evaluates the same preconditions before one
//! atomic local apply. External I/O is deliberately excluded from `apply()`.

use std::collections::{BTreeMap, BTreeSet};
use std::io::Cursor;
use std::sync::{Arc, Mutex};

use openraft::storage::{RaftStateMachine, Snapshot};
use openraft::{
    BasicNode, EntryPayload, LogId, OptionalSend, RaftSnapshotBuilder, SnapshotMeta, StorageError,
    StorageIOError, StoredMembership,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};
use thiserror::Error;

pub type NodeId = u64;
pub type ValueDigest = [u8; 32];

openraft::declare_raft_types!(
    pub TypeConfig:
        D = PreparedAtomicBatch,
        R = ApplyResponse,
        Node = BasicNode,
);

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct StateMachineLimits {
    pub max_conditions: u32,
    pub max_mutations: u32,
    pub max_batch_bytes: u64,
    pub append_only_column_families: BTreeSet<String>,
}

impl StateMachineLimits {
    /// Validates dynamic state-machine safety limits.
    ///
    /// # Errors
    ///
    /// Returns [`BatchValidationError`] when any bound is zero or a configured
    /// column-family name is empty.
    pub fn validate(&self) -> Result<(), BatchValidationError> {
        if self.max_conditions == 0 || self.max_mutations == 0 || self.max_batch_bytes == 0 {
            return Err(BatchValidationError::InvalidLimits);
        }
        if self
            .append_only_column_families
            .iter()
            .any(|name| name.trim().is_empty())
        {
            return Err(BatchValidationError::InvalidLimits);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd, Serialize, Deserialize)]
pub struct StorageKey {
    pub column_family: String,
    pub key: Vec<u8>,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub enum ExpectedValue {
    Missing,
    Digest(ValueDigest),
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct Precondition {
    pub storage_key: StorageKey,
    pub expected: ExpectedValue,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub enum Mutation {
    Put {
        storage_key: StorageKey,
        value: Vec<u8>,
    },
    Delete {
        storage_key: StorageKey,
    },
}

impl Mutation {
    fn storage_key(&self) -> &StorageKey {
        match self {
            Self::Put { storage_key, .. } | Self::Delete { storage_key } => storage_key,
        }
    }
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct PreparedAtomicBatch {
    pub command_id: String,
    pub idempotency_scope: Vec<u8>,
    pub preconditions: Vec<Precondition>,
    pub mutations: Vec<Mutation>,
    pub response_payload: Vec<u8>,
    pub batch_digest: ValueDigest,
}

impl PreparedAtomicBatch {
    #[must_use]
    pub fn new(
        command_id: String,
        idempotency_scope: Vec<u8>,
        preconditions: Vec<Precondition>,
        mutations: Vec<Mutation>,
        response_payload: Vec<u8>,
    ) -> Self {
        let mut batch = Self {
            command_id,
            idempotency_scope,
            preconditions,
            mutations,
            response_payload,
            batch_digest: [0; 32],
        };
        batch.batch_digest = batch.calculate_digest();
        batch
    }

    /// Validates integrity, bounds, duplicate keys and append-only semantics.
    ///
    /// # Errors
    ///
    /// Returns [`BatchValidationError`] without applying any mutation.
    pub fn validate(&self, limits: &StateMachineLimits) -> Result<(), BatchValidationError> {
        limits.validate()?;
        if self.command_id.trim().is_empty() || self.idempotency_scope.is_empty() {
            return Err(BatchValidationError::MissingIdentity);
        }
        if self.calculate_digest() != self.batch_digest {
            return Err(BatchValidationError::DigestMismatch);
        }
        if self.preconditions.len() > limits.max_conditions as usize {
            return Err(BatchValidationError::ConditionLimitExceeded);
        }
        if self.mutations.len() > limits.max_mutations as usize {
            return Err(BatchValidationError::MutationLimitExceeded);
        }
        let serialized_size = serde_json::to_vec(self)
            .map_err(|_| BatchValidationError::Serialization)?
            .len() as u64;
        if serialized_size > limits.max_batch_bytes {
            return Err(BatchValidationError::BatchByteLimitExceeded);
        }

        let mut conditions = BTreeSet::new();
        for condition in &self.preconditions {
            validate_storage_key(&condition.storage_key)?;
            if !conditions.insert(condition.storage_key.clone()) {
                return Err(BatchValidationError::DuplicateCondition);
            }
        }
        let mut mutations = BTreeSet::new();
        for mutation in &self.mutations {
            let key = mutation.storage_key();
            validate_storage_key(key)?;
            if !mutations.insert(key.clone()) {
                return Err(BatchValidationError::DuplicateMutation);
            }
            if limits
                .append_only_column_families
                .contains(&key.column_family)
                && (matches!(mutation, Mutation::Delete { .. })
                    || !self.preconditions.iter().any(|condition| {
                        condition.storage_key == *key
                            && condition.expected == ExpectedValue::Missing
                    }))
            {
                return Err(BatchValidationError::AppendOnlyViolation);
            }
        }
        Ok(())
    }

    fn calculate_digest(&self) -> ValueDigest {
        let mut hasher = Sha256::new();
        digest_bytes(&mut hasher, b"bpmp-authoritative-atomic-batch-v1");
        digest_bytes(&mut hasher, self.command_id.as_bytes());
        digest_bytes(&mut hasher, &self.idempotency_scope);
        hasher.update((self.preconditions.len() as u64).to_be_bytes());
        for condition in &self.preconditions {
            digest_key(&mut hasher, &condition.storage_key);
            match condition.expected {
                ExpectedValue::Missing => hasher.update([0]),
                ExpectedValue::Digest(digest) => {
                    hasher.update([1]);
                    hasher.update(digest);
                }
            }
        }
        hasher.update((self.mutations.len() as u64).to_be_bytes());
        for mutation in &self.mutations {
            match mutation {
                Mutation::Put { storage_key, value } => {
                    hasher.update([1]);
                    digest_key(&mut hasher, storage_key);
                    digest_bytes(&mut hasher, value);
                }
                Mutation::Delete { storage_key } => {
                    hasher.update([2]);
                    digest_key(&mut hasher, storage_key);
                }
            }
        }
        digest_bytes(&mut hasher, &self.response_payload);
        hasher.finalize().into()
    }
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub enum ApplyOutcome {
    Applied,
    Duplicate,
    PreconditionFailed { condition_index: u32 },
    Rejected { reason: String },
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct ApplyResponse {
    pub command_id: String,
    pub batch_digest: ValueDigest,
    pub outcome: ApplyOutcome,
    pub response_payload: Vec<u8>,
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum BatchValidationError {
    #[error("state-machine limits are invalid")]
    InvalidLimits,
    #[error("batch command or idempotency identity is missing")]
    MissingIdentity,
    #[error("batch digest does not match its canonical contents")]
    DigestMismatch,
    #[error("batch condition count exceeds configured limit")]
    ConditionLimitExceeded,
    #[error("batch mutation count exceeds configured limit")]
    MutationLimitExceeded,
    #[error("serialized batch exceeds configured byte limit")]
    BatchByteLimitExceeded,
    #[error("batch cannot be serialized")]
    Serialization,
    #[error("storage key has an empty column family or key")]
    InvalidStorageKey,
    #[error("batch contains duplicate preconditions")]
    DuplicateCondition,
    #[error("batch contains duplicate mutations")]
    DuplicateMutation,
    #[error("batch would overwrite or delete append-only state")]
    AppendOnlyViolation,
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
#[error("authoritative state storage failed: {0}")]
pub struct AtomicStorageError(pub String);

pub trait AtomicStateStorage: Clone + Send + Sync + 'static {
    /// Applies a complete batch under one local atomic durability boundary.
    ///
    /// # Errors
    ///
    /// Returns [`AtomicStorageError`] when local durable storage is unavailable.
    fn apply(
        &self,
        batch: &PreparedAtomicBatch,
        limits: &StateMachineLimits,
    ) -> Result<ApplyResponse, AtomicStorageError>;

    /// Exports one bounded, internally consistent application snapshot.
    ///
    /// # Errors
    ///
    /// Returns [`AtomicStorageError`] when state cannot be read or encoded.
    fn export_snapshot(&self) -> Result<Vec<u8>, AtomicStorageError>;

    /// Replaces application state atomically from a verified Raft snapshot.
    ///
    /// # Errors
    ///
    /// Returns [`AtomicStorageError`] when the snapshot is invalid or cannot be installed.
    fn install_snapshot(&self, snapshot: &[u8]) -> Result<(), AtomicStorageError>;
}

#[derive(Debug, Clone, Default)]
pub struct InMemoryAtomicStateStorage {
    inner: Arc<Mutex<InMemoryState>>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct InMemoryState {
    records: BTreeMap<StorageKey, Vec<u8>>,
    applied: BTreeMap<Vec<u8>, AppliedCommand>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct SerializableInMemoryState {
    records: Vec<(StorageKey, Vec<u8>)>,
    applied: Vec<(Vec<u8>, AppliedCommand)>,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
struct AppliedCommand {
    command_id: String,
    batch_digest: ValueDigest,
    response_payload: Vec<u8>,
}

impl InMemoryAtomicStateStorage {
    /// Reads one record from test state storage.
    ///
    /// # Errors
    ///
    /// Returns [`AtomicStorageError`] if the internal lock was poisoned.
    pub fn get(&self, key: &StorageKey) -> Result<Option<Vec<u8>>, AtomicStorageError> {
        self.inner
            .lock()
            .map_err(lock_error)
            .map(|state| state.records.get(key).cloned())
    }
}

impl AtomicStateStorage for InMemoryAtomicStateStorage {
    fn apply(
        &self,
        batch: &PreparedAtomicBatch,
        limits: &StateMachineLimits,
    ) -> Result<ApplyResponse, AtomicStorageError> {
        if let Err(error) = batch.validate(limits) {
            return Ok(rejected(batch, error.to_string()));
        }
        let mut state = self.inner.lock().map_err(lock_error)?;
        if let Some(applied) = state.applied.get(&batch.idempotency_scope) {
            if applied.command_id == batch.command_id && applied.batch_digest == batch.batch_digest
            {
                return Ok(ApplyResponse {
                    command_id: applied.command_id.clone(),
                    batch_digest: applied.batch_digest,
                    outcome: ApplyOutcome::Duplicate,
                    response_payload: applied.response_payload.clone(),
                });
            }
            return Ok(rejected(batch, "idempotency-scope-conflict".into()));
        }
        for (index, condition) in batch.preconditions.iter().enumerate() {
            let actual = state.records.get(&condition.storage_key);
            let satisfied = match (&condition.expected, actual) {
                (ExpectedValue::Missing, None) => true,
                (ExpectedValue::Digest(expected), Some(value)) => value_digest(value) == *expected,
                _ => false,
            };
            if !satisfied {
                return Ok(ApplyResponse {
                    command_id: batch.command_id.clone(),
                    batch_digest: batch.batch_digest,
                    outcome: ApplyOutcome::PreconditionFailed {
                        condition_index: u32::try_from(index).unwrap_or(u32::MAX),
                    },
                    response_payload: Vec::new(),
                });
            }
        }

        // The lock is the local atomic boundary for this test storage. The RocksDB
        // implementation maps this section to one sync WriteBatch.
        let mut next_records = state.records.clone();
        for mutation in &batch.mutations {
            match mutation {
                Mutation::Put { storage_key, value } => {
                    next_records.insert(storage_key.clone(), value.clone());
                }
                Mutation::Delete { storage_key } => {
                    next_records.remove(storage_key);
                }
            }
        }
        state.records = next_records;
        state.applied.insert(
            batch.idempotency_scope.clone(),
            AppliedCommand {
                command_id: batch.command_id.clone(),
                batch_digest: batch.batch_digest,
                response_payload: batch.response_payload.clone(),
            },
        );
        Ok(ApplyResponse {
            command_id: batch.command_id.clone(),
            batch_digest: batch.batch_digest,
            outcome: ApplyOutcome::Applied,
            response_payload: batch.response_payload.clone(),
        })
    }

    fn export_snapshot(&self) -> Result<Vec<u8>, AtomicStorageError> {
        let state = self.inner.lock().map_err(lock_error)?;
        let serializable = SerializableInMemoryState {
            records: state
                .records
                .iter()
                .map(|(key, value)| (key.clone(), value.clone()))
                .collect(),
            applied: state
                .applied
                .iter()
                .map(|(key, value)| (key.clone(), value.clone()))
                .collect(),
        };
        serde_json::to_vec(&serializable).map_err(|error| AtomicStorageError(error.to_string()))
    }

    fn install_snapshot(&self, snapshot: &[u8]) -> Result<(), AtomicStorageError> {
        let serializable: SerializableInMemoryState = serde_json::from_slice(snapshot)
            .map_err(|error| AtomicStorageError(error.to_string()))?;
        let replacement = InMemoryState {
            records: serializable.records.into_iter().collect(),
            applied: serializable.applied.into_iter().collect(),
        };
        let mut state = self.inner.lock().map_err(lock_error)?;
        *state = replacement;
        Ok(())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct PersistedSnapshot {
    last_applied: Option<LogId<NodeId>>,
    membership: StoredMembership<NodeId, BasicNode>,
    application_state: Vec<u8>,
}

#[derive(Debug, Clone)]
struct CurrentSnapshot {
    meta: SnapshotMeta<NodeId, BasicNode>,
    bytes: Vec<u8>,
}

#[derive(Debug, Clone)]
pub struct AuthoritativeStateMachine<S> {
    storage: S,
    limits: StateMachineLimits,
    last_applied: Option<LogId<NodeId>>,
    membership: StoredMembership<NodeId, BasicNode>,
    snapshot_index: u64,
    current_snapshot: Option<CurrentSnapshot>,
}

impl<S: AtomicStateStorage> AuthoritativeStateMachine<S> {
    /// Creates the state-machine half of an `OpenRaft` node.
    ///
    /// # Errors
    ///
    /// Returns [`BatchValidationError`] for unsafe dynamic bounds.
    pub fn new(storage: S, limits: StateMachineLimits) -> Result<Self, BatchValidationError> {
        limits.validate()?;
        Ok(Self {
            storage,
            limits,
            last_applied: None,
            membership: StoredMembership::default(),
            snapshot_index: 0,
            current_snapshot: None,
        })
    }

    pub const fn storage(&self) -> &S {
        &self.storage
    }
}

impl<S: AtomicStateStorage> RaftSnapshotBuilder<TypeConfig> for AuthoritativeStateMachine<S> {
    async fn build_snapshot(&mut self) -> Result<Snapshot<TypeConfig>, StorageError<NodeId>> {
        let persisted = PersistedSnapshot {
            last_applied: self.last_applied,
            membership: self.membership.clone(),
            application_state: self
                .storage
                .export_snapshot()
                .map_err(|error| StorageIOError::read_state_machine(&error))?,
        };
        let bytes = serde_json::to_vec(&persisted)
            .map_err(|error| StorageIOError::read_state_machine(&error))?;
        let snapshot_id = match self.last_applied {
            Some(log_id) => format!(
                "{}-{}-{}",
                log_id.leader_id, log_id.index, self.snapshot_index
            ),
            None => format!("empty-{}", self.snapshot_index),
        };
        let meta = SnapshotMeta {
            last_log_id: self.last_applied,
            last_membership: self.membership.clone(),
            snapshot_id,
        };
        self.current_snapshot = Some(CurrentSnapshot {
            meta: meta.clone(),
            bytes: bytes.clone(),
        });
        Ok(Snapshot {
            meta,
            snapshot: Box::new(Cursor::new(bytes)),
        })
    }
}

impl<S: AtomicStateStorage> RaftStateMachine<TypeConfig> for AuthoritativeStateMachine<S> {
    type SnapshotBuilder = Self;

    async fn applied_state(
        &mut self,
    ) -> Result<(Option<LogId<NodeId>>, StoredMembership<NodeId, BasicNode>), StorageError<NodeId>>
    {
        Ok((self.last_applied, self.membership.clone()))
    }

    async fn apply<I>(&mut self, entries: I) -> Result<Vec<ApplyResponse>, StorageError<NodeId>>
    where
        I: IntoIterator<Item = openraft::Entry<TypeConfig>> + OptionalSend,
        I::IntoIter: OptionalSend,
    {
        let entries = entries.into_iter();
        let mut responses = Vec::with_capacity(entries.size_hint().0);
        for entry in entries {
            self.last_applied = Some(entry.log_id);
            let response = match entry.payload {
                EntryPayload::Blank => ApplyResponse {
                    command_id: String::new(),
                    batch_digest: [0; 32],
                    outcome: ApplyOutcome::Applied,
                    response_payload: Vec::new(),
                },
                EntryPayload::Normal(batch) => self
                    .storage
                    .apply(&batch, &self.limits)
                    .map_err(|error| StorageIOError::write_state_machine(&error))?,
                EntryPayload::Membership(membership) => {
                    self.membership = StoredMembership::new(Some(entry.log_id), membership);
                    ApplyResponse {
                        command_id: String::new(),
                        batch_digest: [0; 32],
                        outcome: ApplyOutcome::Applied,
                        response_payload: Vec::new(),
                    }
                }
            };
            responses.push(response);
        }
        Ok(responses)
    }

    async fn get_snapshot_builder(&mut self) -> Self::SnapshotBuilder {
        self.snapshot_index = self.snapshot_index.saturating_add(1);
        self.clone()
    }

    async fn begin_receiving_snapshot(
        &mut self,
    ) -> Result<Box<Cursor<Vec<u8>>>, StorageError<NodeId>> {
        Ok(Box::new(Cursor::new(Vec::new())))
    }

    async fn install_snapshot(
        &mut self,
        meta: &SnapshotMeta<NodeId, BasicNode>,
        snapshot: Box<Cursor<Vec<u8>>>,
    ) -> Result<(), StorageError<NodeId>> {
        let bytes = snapshot.into_inner();
        let persisted: PersistedSnapshot = serde_json::from_slice(&bytes)
            .map_err(|error| StorageIOError::read_snapshot(Some(meta.signature()), &error))?;
        if persisted.last_applied != meta.last_log_id
            || persisted.membership != meta.last_membership
        {
            return Err(StorageIOError::read_snapshot(
                Some(meta.signature()),
                &AtomicStorageError("snapshot metadata mismatch".into()),
            )
            .into());
        }
        self.storage
            .install_snapshot(&persisted.application_state)
            .map_err(|error| StorageIOError::write_snapshot(Some(meta.signature()), &error))?;
        self.last_applied = persisted.last_applied;
        self.membership = persisted.membership;
        self.current_snapshot = Some(CurrentSnapshot {
            meta: meta.clone(),
            bytes,
        });
        Ok(())
    }

    async fn get_current_snapshot(
        &mut self,
    ) -> Result<Option<Snapshot<TypeConfig>>, StorageError<NodeId>> {
        Ok(self.current_snapshot.as_ref().map(|snapshot| Snapshot {
            meta: snapshot.meta.clone(),
            snapshot: Box::new(Cursor::new(snapshot.bytes.clone())),
        }))
    }
}

#[must_use]
pub fn value_digest(value: &[u8]) -> ValueDigest {
    Sha256::digest(value).into()
}

fn validate_storage_key(key: &StorageKey) -> Result<(), BatchValidationError> {
    if key.column_family.trim().is_empty() || key.key.is_empty() {
        return Err(BatchValidationError::InvalidStorageKey);
    }
    Ok(())
}

fn digest_key(hasher: &mut Sha256, key: &StorageKey) {
    digest_bytes(hasher, key.column_family.as_bytes());
    digest_bytes(hasher, &key.key);
}

fn digest_bytes(hasher: &mut Sha256, value: &[u8]) {
    hasher.update((value.len() as u64).to_be_bytes());
    hasher.update(value);
}

fn rejected(batch: &PreparedAtomicBatch, reason: String) -> ApplyResponse {
    ApplyResponse {
        command_id: batch.command_id.clone(),
        batch_digest: batch.batch_digest,
        outcome: ApplyOutcome::Rejected { reason },
        response_payload: Vec::new(),
    }
}

#[allow(clippy::needless_pass_by_value)]
fn lock_error<T>(error: std::sync::PoisonError<T>) -> AtomicStorageError {
    AtomicStorageError(error.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn limits() -> StateMachineLimits {
        StateMachineLimits {
            max_conditions: 16,
            max_mutations: 16,
            max_batch_bytes: 64 * 1024,
            append_only_column_families: BTreeSet::from(["events".into(), "audit".into()]),
        }
    }

    fn key(cf: &str, key: &[u8]) -> StorageKey {
        StorageKey {
            column_family: cf.into(),
            key: key.to_vec(),
        }
    }

    #[test]
    fn batch_applies_all_mutations_and_duplicate_returns_original_result() {
        let storage = InMemoryAtomicStateStorage::default();
        let batch = PreparedAtomicBatch::new(
            "command-1".into(),
            b"tenant/actor/idempotency".to_vec(),
            vec![Precondition {
                storage_key: key("events", b"event-1"),
                expected: ExpectedValue::Missing,
            }],
            vec![
                Mutation::Put {
                    storage_key: key("events", b"event-1"),
                    value: b"ciphertext".to_vec(),
                },
                Mutation::Put {
                    storage_key: key("stream_meta", b"stream-1"),
                    value: 1_u64.to_be_bytes().to_vec(),
                },
            ],
            b"version-1".to_vec(),
        );

        let first = storage.apply(&batch, &limits()).unwrap();
        let duplicate = storage.apply(&batch, &limits()).unwrap();

        assert_eq!(first.outcome, ApplyOutcome::Applied);
        assert_eq!(duplicate.outcome, ApplyOutcome::Duplicate);
        assert_eq!(first.response_payload, duplicate.response_payload);
        assert_eq!(
            storage.get(&key("events", b"event-1")).unwrap(),
            Some(b"ciphertext".to_vec())
        );
    }

    #[test]
    fn stale_precondition_changes_nothing() {
        let storage = InMemoryAtomicStateStorage::default();
        let seed = PreparedAtomicBatch::new(
            "seed".into(),
            b"seed-scope".to_vec(),
            Vec::new(),
            vec![Mutation::Put {
                storage_key: key("stream_meta", b"stream"),
                value: 2_u64.to_be_bytes().to_vec(),
            }],
            Vec::new(),
        );
        storage.apply(&seed, &limits()).unwrap();
        let stale = PreparedAtomicBatch::new(
            "stale".into(),
            b"stale-scope".to_vec(),
            vec![Precondition {
                storage_key: key("stream_meta", b"stream"),
                expected: ExpectedValue::Digest(value_digest(&1_u64.to_be_bytes())),
            }],
            vec![Mutation::Put {
                storage_key: key("reconciliation", b"item"),
                value: b"must-not-exist".to_vec(),
            }],
            Vec::new(),
        );

        let response = storage.apply(&stale, &limits()).unwrap();

        assert_eq!(
            response.outcome,
            ApplyOutcome::PreconditionFailed { condition_index: 0 }
        );
        assert_eq!(storage.get(&key("reconciliation", b"item")).unwrap(), None);
    }

    #[test]
    fn append_only_put_requires_missing_precondition() {
        let batch = PreparedAtomicBatch::new(
            "command".into(),
            b"scope".to_vec(),
            Vec::new(),
            vec![Mutation::Put {
                storage_key: key("events", b"event"),
                value: b"value".to_vec(),
            }],
            Vec::new(),
        );

        assert_eq!(
            batch.validate(&limits()),
            Err(BatchValidationError::AppendOnlyViolation)
        );
    }

    #[test]
    fn snapshot_round_trip_preserves_records_and_idempotency() {
        let storage = InMemoryAtomicStateStorage::default();
        let batch = PreparedAtomicBatch::new(
            "command".into(),
            b"scope".to_vec(),
            Vec::new(),
            vec![Mutation::Put {
                storage_key: key("stream_meta", b"stream"),
                value: b"state".to_vec(),
            }],
            b"response".to_vec(),
        );
        storage.apply(&batch, &limits()).unwrap();
        let snapshot = storage.export_snapshot().unwrap();
        let restored = InMemoryAtomicStateStorage::default();

        restored.install_snapshot(&snapshot).unwrap();

        assert_eq!(
            restored.get(&key("stream_meta", b"stream")).unwrap(),
            Some(b"state".to_vec())
        );
        assert_eq!(
            restored.apply(&batch, &limits()).unwrap().outcome,
            ApplyOutcome::Duplicate
        );
    }

    #[tokio::test]
    async fn openraft_apply_and_snapshot_install_preserve_authoritative_state() {
        let storage = InMemoryAtomicStateStorage::default();
        let mut state_machine = AuthoritativeStateMachine::new(storage.clone(), limits()).unwrap();
        let batch = PreparedAtomicBatch::new(
            "raft-command".into(),
            b"raft-scope".to_vec(),
            Vec::new(),
            vec![Mutation::Put {
                storage_key: key("stream_meta", b"stream"),
                value: 7_u64.to_be_bytes().to_vec(),
            }],
            b"committed-7".to_vec(),
        );
        let log_id = LogId::new(openraft::CommittedLeaderId::new(3, 1), 9);
        let entry = openraft::Entry::<TypeConfig> {
            log_id,
            payload: EntryPayload::Normal(batch.clone()),
        };

        let responses = RaftStateMachine::apply(&mut state_machine, [entry])
            .await
            .unwrap();
        let mut builder = RaftStateMachine::get_snapshot_builder(&mut state_machine).await;
        let snapshot = RaftSnapshotBuilder::build_snapshot(&mut builder)
            .await
            .unwrap();
        let restored_storage = InMemoryAtomicStateStorage::default();
        let mut restored =
            AuthoritativeStateMachine::new(restored_storage.clone(), limits()).unwrap();
        RaftStateMachine::install_snapshot(&mut restored, &snapshot.meta, snapshot.snapshot)
            .await
            .unwrap();

        assert_eq!(responses[0].outcome, ApplyOutcome::Applied);
        assert_eq!(
            restored_storage
                .get(&key("stream_meta", b"stream"))
                .unwrap(),
            Some(7_u64.to_be_bytes().to_vec())
        );
        assert_eq!(
            restored_storage.apply(&batch, &limits()).unwrap().outcome,
            ApplyOutcome::Duplicate
        );
        assert_eq!(
            RaftStateMachine::applied_state(&mut restored)
                .await
                .unwrap()
                .0,
            Some(log_id)
        );
    }
}
