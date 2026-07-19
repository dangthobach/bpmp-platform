use std::path::PathBuf;
use std::sync::Mutex;

use bpmp_authz_contracts::authorization::v1::{
    AuthorizationAuditRecord as ContractAuthorizationAuditRecord, AuthorizationDecisionType,
};
use bpmp_contracts::storage::v1::{
    BoundarySignalRecord, BoundarySubscriptionRecord, BoundaryTimerScheduleRecord,
    EncryptedAuthorizationAuditRecord, EncryptedEventRecord, EncryptedSnapshotRecord, OutboxEntry,
    StoredCommandResult,
};
use bpmp_domain_core::{
    ActorId, BoundaryTimerKind, BoundaryTrigger, CommandId, ConfigVersion, IdempotencyKey,
    InstanceId, KeyScope, NodeId, PolicyVersion, TenantId, WorkflowType, WorkflowVersion,
};
use bpmp_engine::{
    BoundaryProjectionMutation, BoundaryRuntimeError, BoundaryRuntimeStorePort, BoundarySignal,
    BoundarySignalKind, BoundarySubscriptionKey, ClaimedCorrelation, ClaimedTimer, CommitOutcome,
    CommitRequest, CommittedResult, EventCodec, EventEnvelope, LoadedInstance, OutboxError,
    OutboxRecord, OutboxStorePort, ProjectedBoundarySubscription, SignalEnqueueOutcome,
    SnapshotCodec, SnapshotEnvelope, StoreError, TimerDispatchCompletion, TimerSchedule,
    WorkflowStorePort,
};
use bpmp_payload_crypto::{EncryptedPayload, EncryptionContext, PayloadCryptoPort};
use prost::Message;
use rocksdb::{
    ColumnFamilyDescriptor, DB, Direction, IteratorMode, Options, WriteBatch, WriteOptions,
};
use thiserror::Error;

const EVENTS_CF: &str = "events";
const SNAPSHOTS_CF: &str = "snapshots";
const STREAM_META_CF: &str = "stream_meta";
const DEDUP_CF: &str = "dedup";
const OUTBOX_CF: &str = "outbox";
const IDEMPOTENCY_CF: &str = "idempotency";
const AUTHORIZATION_AUDIT_CF: &str = "authorization_audit";
const OUTBOX_META_CF: &str = "outbox_meta";
const BOUNDARY_SUBSCRIPTIONS_CF: &str = "boundary_subscriptions";
const BOUNDARY_TIMER_INDEX_CF: &str = "boundary_timer_index";
const BOUNDARY_SIGNALS_CF: &str = "boundary_signals";
const BOUNDARY_SIGNAL_INDEX_CF: &str = "boundary_signal_index";
const BOUNDARY_META_CF: &str = "boundary_meta";
const STORAGE_SCHEMA_VERSION: u32 = 1;
const OUTBOX_TAIL_KEY: &[u8] = b"tail";
const OUTBOX_CHECKPOINT_KEY: &[u8] = b"publisher-checkpoint";
const BOUNDARY_PROJECTION_CHECKPOINT_KEY: &[u8] = b"projection-checkpoint";

#[derive(Debug, Clone)]
pub struct RocksDbConfig {
    pub path: PathBuf,
    pub max_open_files: i32,
    pub write_buffer_size_bytes: usize,
    pub max_background_jobs: i32,
    pub max_replay_events: usize,
}

impl RocksDbConfig {
    /// Validates operational `RocksDB` settings supplied by configuration.
    ///
    /// # Errors
    ///
    /// Returns [`RocksDbConfigError`] when a required path/value is empty or non-positive.
    pub fn validate(&self) -> Result<(), RocksDbConfigError> {
        if self.path.as_os_str().is_empty() {
            return Err(RocksDbConfigError::EmptyPath);
        }
        if self.max_open_files <= 0 {
            return Err(RocksDbConfigError::NonPositive("max_open_files"));
        }
        if self.write_buffer_size_bytes == 0 {
            return Err(RocksDbConfigError::NonPositive("write_buffer_size_bytes"));
        }
        if self.max_background_jobs <= 0 {
            return Err(RocksDbConfigError::NonPositive("max_background_jobs"));
        }
        if self.max_replay_events == 0 {
            return Err(RocksDbConfigError::NonPositive("max_replay_events"));
        }
        Ok(())
    }
}

pub struct RocksDbWorkflowStore<C> {
    db: DB,
    crypto: C,
    max_replay_events: usize,
    // P0 single-node commits serialize here so version/dedup checks and WriteBatch are atomic.
    commit_lock: Mutex<()>,
}

impl<C: PayloadCryptoPort> RocksDbWorkflowStore<C> {
    /// Opens the authoritative local `RocksDB` with all required column families.
    ///
    /// # Errors
    ///
    /// Returns [`RocksDbOpenError`] when configuration is invalid or `RocksDB` cannot open.
    #[allow(clippy::needless_pass_by_value)]
    pub fn open(config: RocksDbConfig, crypto: C) -> Result<Self, RocksDbOpenError> {
        config.validate()?;
        let mut options = Options::default();
        options.create_if_missing(true);
        options.create_missing_column_families(true);
        options.set_max_open_files(config.max_open_files);
        options.set_write_buffer_size(config.write_buffer_size_bytes);
        options.set_max_background_jobs(config.max_background_jobs);
        let descriptors = [
            EVENTS_CF,
            SNAPSHOTS_CF,
            STREAM_META_CF,
            DEDUP_CF,
            OUTBOX_CF,
            IDEMPOTENCY_CF,
            AUTHORIZATION_AUDIT_CF,
            OUTBOX_META_CF,
            BOUNDARY_SUBSCRIPTIONS_CF,
            BOUNDARY_TIMER_INDEX_CF,
            BOUNDARY_SIGNALS_CF,
            BOUNDARY_SIGNAL_INDEX_CF,
            BOUNDARY_META_CF,
        ]
        .into_iter()
        .map(|name| ColumnFamilyDescriptor::new(name, Options::default()));
        let db = DB::open_cf_descriptors(&options, &config.path, descriptors)
            .map_err(|error| RocksDbOpenError::Open(error.to_string()))?;
        Ok(Self {
            db,
            crypto,
            max_replay_events: config.max_replay_events,
            commit_lock: Mutex::new(()),
        })
    }

    fn load_result(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError> {
        let key = idempotency_storage_key(tenant_id, actor_id, idempotency_key);
        let bytes = self
            .db
            .get_cf(cf(&self.db, IDEMPOTENCY_CF)?, key)
            .map_err(unavailable)?;
        let Some(bytes) = bytes else {
            return Ok(None);
        };
        let stored = StoredCommandResult::decode(bytes.as_slice())
            .map_err(|error| StoreError::CorruptData(error.to_string()))?;
        if stored.command_id != command_id.as_str() {
            return Err(StoreError::IdempotencyConflict);
        }
        Ok(Some(stored_result(stored)?))
    }
}

impl<C: PayloadCryptoPort> WorkflowStorePort for RocksDbWorkflowStore<C> {
    fn lookup_idempotency(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError> {
        self.load_result(tenant_id, actor_id, idempotency_key, command_id)
    }

    fn load(
        &self,
        tenant_id: &TenantId,
        instance_id: &InstanceId,
    ) -> Result<LoadedInstance, StoreError> {
        let version = read_version(&self.db, tenant_id, instance_id)?;
        let snapshot = load_snapshot(&self.db, &self.crypto, tenant_id, instance_id)?;
        let snapshot_sequence = snapshot
            .as_ref()
            .map_or(0, |snapshot| snapshot.state.sequence);
        if snapshot_sequence > version {
            return Err(StoreError::CorruptData(
                "snapshot sequence exceeds stream version".into(),
            ));
        }
        let prefix = stream_prefix(tenant_id, instance_id);
        let mut events = Vec::new();
        for item in self.db.iterator_cf(
            cf(&self.db, EVENTS_CF)?,
            IteratorMode::From(&prefix, Direction::Forward),
        ) {
            let (key, value) = item.map_err(unavailable)?;
            if !key.starts_with(&prefix) {
                break;
            }
            let sequence = sequence_from_event_key(&prefix, key.as_ref())?;
            if sequence <= snapshot_sequence {
                continue;
            }
            if events.len() == self.max_replay_events {
                return Err(StoreError::ReplayLimitExceeded {
                    configured_limit: self.max_replay_events,
                });
            }
            let record = EncryptedEventRecord::decode(value.as_ref())
                .map_err(|error| StoreError::CorruptData(error.to_string()))?;
            let encrypted = encrypted_payload(record)?;
            let plaintext = self
                .crypto
                .decrypt(key.as_ref(), &encrypted)
                .map_err(|_| StoreError::CryptoUnavailable)?;
            let event = EventCodec::decode(&plaintext)
                .map_err(|error| StoreError::CorruptData(error.to_string()))?;
            if event.metadata.tenant_id != *tenant_id || event.metadata.instance_id != *instance_id
            {
                return Err(StoreError::CorruptData(
                    "event scope does not match its storage key".into(),
                ));
            }
            events.push(event);
        }
        let replayed_version = snapshot_sequence
            .checked_add(u64::try_from(events.len()).map_err(|error| {
                StoreError::CorruptData(format!("event count cannot fit stream version: {error}"))
            })?)
            .ok_or_else(|| StoreError::CorruptData("stream version overflow".into()))?;
        if replayed_version != version {
            return Err(StoreError::CorruptData(
                "stream metadata version does not match snapshot and tail events".into(),
            ));
        }
        Ok(LoadedInstance {
            snapshot,
            events,
            version,
        })
    }

    fn commit(&self, request: CommitRequest) -> Result<CommitOutcome, StoreError> {
        request.validate_authorization_audit()?;
        validate_sequences(&request)?;
        let prepared = prepare_encrypted_events(&self.crypto, &request)?;
        let prepared_snapshot = prepare_encrypted_snapshot(&self.crypto, &request)?;
        let prepared_audit = prepare_encrypted_authorization_audit(&self.crypto, &request)?;
        let _guard = self
            .commit_lock
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        if let Some(result) = self.load_result(
            &request.tenant_id,
            &request.actor_id,
            &request.idempotency_key,
            &request.command_id,
        )? {
            let audit_key =
                authorization_audit_storage_key(&request.tenant_id, &request.command_id);
            if self
                .db
                .get_cf(cf(&self.db, AUTHORIZATION_AUDIT_CF)?, audit_key)
                .map_err(unavailable)?
                .is_none()
            {
                return Err(StoreError::CorruptData(
                    "idempotency result exists without authorization audit".into(),
                ));
            }
            return Ok(CommitOutcome::Duplicate(result));
        }
        let actual = read_version(&self.db, &request.tenant_id, &request.instance_id)?;
        if actual != request.expected_version {
            return Err(StoreError::VersionConflict {
                expected: request.expected_version,
                actual,
            });
        }
        let outbox_tail = read_u64_value(&self.db, OUTBOX_META_CF, OUTBOX_TAIL_KEY)?;
        let batch = build_commit_batch(
            &self.db,
            &request,
            prepared,
            prepared_snapshot,
            prepared_audit,
            outbox_tail,
        )?;
        let mut write_options = WriteOptions::default();
        write_options.set_sync(true);
        self.db
            .write_opt(batch, &write_options)
            .map_err(unavailable)?;
        Ok(CommitOutcome::Committed(request.result))
    }
}

impl<C: PayloadCryptoPort> OutboxStorePort for RocksDbWorkflowStore<C> {
    fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError> {
        if limit == 0 {
            return Err(OutboxError::InvalidConfiguration);
        }
        let start = cursor
            .checked_add(1)
            .ok_or_else(|| OutboxError::StoreUnavailable("outbox cursor overflow".into()))?
            .to_be_bytes();
        let mut records = Vec::with_capacity(limit);
        for item in self.db.iterator_cf(
            outbox_cf(&self.db)?,
            IteratorMode::From(&start, Direction::Forward),
        ) {
            if records.len() == limit {
                break;
            }
            let (key, value) = item.map_err(outbox_unavailable)?;
            let stored_cursor = decode_outbox_cursor(key.as_ref())?;
            let entry = OutboxEntry::decode(value.as_ref())
                .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
            if entry.outbox_sequence != stored_cursor || entry.outbox_sequence <= cursor {
                return Err(OutboxError::StoreUnavailable(
                    "outbox key and payload sequence mismatch".into(),
                ));
            }
            records.push(load_outbox_record(&self.db, &self.crypto, entry)?);
        }
        Ok(records)
    }

    fn checkpoint(&self, expected: u64, committed: u64) -> Result<(), OutboxError> {
        let _guard = self
            .commit_lock
            .lock()
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        let current = read_outbox_u64(&self.db, OUTBOX_CHECKPOINT_KEY)?;
        let tail = read_outbox_u64(&self.db, OUTBOX_TAIL_KEY)?;
        if current != expected {
            return Err(OutboxError::CheckpointConflict);
        }
        if committed <= expected || committed > tail {
            return Err(OutboxError::StoreUnavailable(
                "outbox checkpoint is outside the committed range".into(),
            ));
        }
        let mut options = WriteOptions::default();
        options.set_sync(true);
        self.db
            .put_cf_opt(
                outbox_meta_cf(&self.db)?,
                OUTBOX_CHECKPOINT_KEY,
                committed.to_be_bytes(),
                &options,
            )
            .map_err(outbox_unavailable)
    }
}

impl<C: PayloadCryptoPort> BoundaryRuntimeStorePort for RocksDbWorkflowStore<C> {
    fn projection_checkpoint(&self) -> Result<u64, BoundaryRuntimeError> {
        read_boundary_u64(&self.db, BOUNDARY_PROJECTION_CHECKPOINT_KEY)
    }

    fn apply_projection(
        &self,
        expected_checkpoint: u64,
        committed_checkpoint: u64,
        mutations: &[BoundaryProjectionMutation],
    ) -> Result<(), BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        let current = read_boundary_u64(&self.db, BOUNDARY_PROJECTION_CHECKPOINT_KEY)?;
        if current != expected_checkpoint {
            return Err(BoundaryRuntimeError::ProjectionCheckpointConflict);
        }
        if committed_checkpoint <= expected_checkpoint {
            return Err(BoundaryRuntimeError::NonContiguousProjection);
        }
        let mut batch = WriteBatch::default();
        for mutation in mutations {
            apply_boundary_projection_mutation(&self.db, &mut batch, mutation)?;
        }
        batch.put_cf(
            boundary_meta_cf(&self.db)?,
            BOUNDARY_PROJECTION_CHECKPOINT_KEY,
            committed_checkpoint.to_be_bytes(),
        );
        write_boundary_batch(&self.db, batch)
    }

    fn claim_due_timers(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
    ) -> Result<Vec<ClaimedTimer>, BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        let mut batch = WriteBatch::default();
        let mut claims = Vec::with_capacity(limit);
        for item in self
            .db
            .iterator_cf(boundary_timer_index_cf(&self.db)?, IteratorMode::Start)
        {
            if claims.len() == limit {
                break;
            }
            let (index_key, _) = item.map_err(boundary_unavailable)?;
            let (available_at, subscription_key) = split_due_index_key(index_key.as_ref())?;
            if available_at > now_epoch_ms {
                break;
            }
            let Some(bytes) = self
                .db
                .get_cf(boundary_subscriptions_cf(&self.db)?, subscription_key)
                .map_err(boundary_unavailable)?
            else {
                return Err(BoundaryRuntimeError::Store(
                    "timer index references a missing subscription".into(),
                ));
            };
            let mut record = decode_boundary_subscription(&bytes)?;
            if record.timer_schedule.is_none() || record.available_at_epoch_ms != available_at {
                return Err(BoundaryRuntimeError::Store(
                    "timer index and subscription disagree".into(),
                ));
            }
            if record.dead_lettered || record.lease_until_epoch_ms > now_epoch_ms {
                continue;
            }
            record.attempts = record.attempts.saturating_add(1);
            record.lease_version = record.lease_version.saturating_add(1);
            record.lease_until_epoch_ms = lease_until_epoch_ms;
            worker_id.clone_into(&mut record.worker_id);
            let subscription = subscription_from_record(&record)?;
            claims.push(ClaimedTimer {
                subscription,
                generation: record.generation,
                attempts: record.attempts,
                lease_version: record.lease_version,
            });
            batch.put_cf(
                boundary_subscriptions_cf(&self.db)?,
                subscription_key,
                record.encode_to_vec(),
            );
        }
        if !claims.is_empty() {
            write_boundary_batch(&self.db, batch)?;
        }
        Ok(claims)
    }

    fn complete_timer(
        &self,
        claim: &ClaimedTimer,
        completion: TimerDispatchCompletion,
    ) -> Result<(), BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        let key = boundary_subscription_storage_key(&claim.subscription.key);
        let mut record = load_boundary_subscription_record(&self.db, &key)?;
        validate_boundary_timer_claim(&record, claim)?;
        let old_index = boundary_due_index_key(record.available_at_epoch_ms, &key);
        let mut batch = WriteBatch::default();
        batch.delete_cf(boundary_timer_index_cf(&self.db)?, old_index);
        if let Some(schedule) = completion.next_schedule {
            set_record_schedule(&mut record, Some(schedule));
            record.generation = record.generation.saturating_add(1);
            record.attempts = 0;
            record.available_at_epoch_ms = schedule.due_at_epoch_ms;
            record.lease_until_epoch_ms = 0;
            record.worker_id.clear();
            batch.put_cf(
                boundary_timer_index_cf(&self.db)?,
                boundary_due_index_key(schedule.due_at_epoch_ms, &key),
                [],
            );
        } else {
            set_record_schedule(&mut record, None);
            record.lease_until_epoch_ms = 0;
            record.worker_id.clear();
        }
        batch.put_cf(
            boundary_subscriptions_cf(&self.db)?,
            key,
            record.encode_to_vec(),
        );
        write_boundary_batch(&self.db, batch)
    }

    fn fail_timer(
        &self,
        claim: &ClaimedTimer,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        let key = boundary_subscription_storage_key(&claim.subscription.key);
        let mut record = load_boundary_subscription_record(&self.db, &key)?;
        validate_boundary_timer_claim(&record, claim)?;
        let mut batch = WriteBatch::default();
        batch.delete_cf(
            boundary_timer_index_cf(&self.db)?,
            boundary_due_index_key(record.available_at_epoch_ms, &key),
        );
        record.available_at_epoch_ms = retry_at_epoch_ms;
        record.lease_until_epoch_ms = 0;
        record.worker_id.clear();
        record.dead_lettered = dead_letter;
        if !dead_letter {
            batch.put_cf(
                boundary_timer_index_cf(&self.db)?,
                boundary_due_index_key(retry_at_epoch_ms, &key),
                [],
            );
        }
        batch.put_cf(
            boundary_subscriptions_cf(&self.db)?,
            key,
            record.encode_to_vec(),
        );
        write_boundary_batch(&self.db, batch)
    }

    fn enqueue_signal(
        &self,
        signal: &BoundarySignal,
    ) -> Result<SignalEnqueueOutcome, BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        let key = boundary_signal_storage_key(&signal.tenant_id, &signal.signal_id);
        if let Some(bytes) = self
            .db
            .get_cf(boundary_signals_cf(&self.db)?, &key)
            .map_err(boundary_unavailable)?
        {
            let existing = decode_boundary_signal(&bytes)?;
            return if signal_from_record(&existing)? == *signal {
                Ok(SignalEnqueueOutcome::Duplicate)
            } else {
                Err(BoundaryRuntimeError::SignalConflict)
            };
        }
        let record = signal_to_record(signal);
        let mut batch = WriteBatch::default();
        batch.put_cf(boundary_signals_cf(&self.db)?, &key, record.encode_to_vec());
        batch.put_cf(
            boundary_signal_index_cf(&self.db)?,
            boundary_due_index_key(record.available_at_epoch_ms, &key),
            [],
        );
        write_boundary_batch(&self.db, batch)?;
        Ok(SignalEnqueueOutcome::Enqueued)
    }

    fn claim_correlations(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
        max_subscriptions_per_instance: usize,
    ) -> Result<Vec<ClaimedCorrelation>, BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        let mut batch = WriteBatch::default();
        let mut claims = Vec::with_capacity(limit);
        for item in self
            .db
            .iterator_cf(boundary_signal_index_cf(&self.db)?, IteratorMode::Start)
        {
            if claims.len() == limit {
                break;
            }
            let (index_key, _) = item.map_err(boundary_unavailable)?;
            let (available_at, signal_key) = split_due_index_key(index_key.as_ref())?;
            if available_at > now_epoch_ms {
                break;
            }
            let Some(bytes) = self
                .db
                .get_cf(boundary_signals_cf(&self.db)?, signal_key)
                .map_err(boundary_unavailable)?
            else {
                return Err(BoundaryRuntimeError::Store(
                    "signal index references a missing record".into(),
                ));
            };
            let mut record = decode_boundary_signal(&bytes)?;
            if record.available_at_epoch_ms != available_at {
                return Err(BoundaryRuntimeError::Store(
                    "signal index and record disagree".into(),
                ));
            }
            if record.completed
                || record.dead_lettered
                || record.lease_until_epoch_ms > now_epoch_ms
            {
                continue;
            }
            let signal = signal_from_record(&record)?;
            let subscription =
                correlate_boundary_subscription(&self.db, &signal, max_subscriptions_per_instance)?;
            record.attempts = record.attempts.saturating_add(1);
            record.lease_version = record.lease_version.saturating_add(1);
            record.lease_until_epoch_ms = lease_until_epoch_ms;
            worker_id.clone_into(&mut record.worker_id);
            claims.push(ClaimedCorrelation {
                signal,
                subscription,
                attempts: record.attempts,
                lease_version: record.lease_version,
            });
            batch.put_cf(
                boundary_signals_cf(&self.db)?,
                signal_key,
                record.encode_to_vec(),
            );
        }
        if !claims.is_empty() {
            write_boundary_batch(&self.db, batch)?;
        }
        Ok(claims)
    }

    fn complete_correlation(&self, claim: &ClaimedCorrelation) -> Result<(), BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        update_boundary_signal_claim(&self.db, claim, 0, false, true)
    }

    fn fail_correlation(
        &self,
        claim: &ClaimedCorrelation,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError> {
        let _guard = self.commit_lock.lock().map_err(boundary_lock_error)?;
        update_boundary_signal_claim(&self.db, claim, retry_at_epoch_ms, dead_letter, false)
    }
}

fn apply_boundary_projection_mutation(
    db: &DB,
    batch: &mut WriteBatch,
    mutation: &BoundaryProjectionMutation,
) -> Result<(), BoundaryRuntimeError> {
    match mutation {
        BoundaryProjectionMutation::Upsert(subscription) => {
            let key = boundary_subscription_storage_key(&subscription.key);
            if let Some(bytes) = db
                .get_cf(boundary_subscriptions_cf(db)?, &key)
                .map_err(boundary_unavailable)?
            {
                let existing = decode_boundary_subscription(&bytes)?;
                if existing.timer_schedule.is_some() {
                    batch.delete_cf(
                        boundary_timer_index_cf(db)?,
                        boundary_due_index_key(existing.available_at_epoch_ms, &key),
                    );
                }
            }
            let record = subscription_to_record(subscription);
            if record.timer_schedule.is_some() {
                batch.put_cf(
                    boundary_timer_index_cf(db)?,
                    boundary_due_index_key(record.available_at_epoch_ms, &key),
                    [],
                );
            }
            batch.put_cf(boundary_subscriptions_cf(db)?, key, record.encode_to_vec());
        }
        BoundaryProjectionMutation::DisarmBoundary(key) => {
            delete_boundary_subscription(db, batch, key)?;
        }
        BoundaryProjectionMutation::DisarmAttached {
            tenant_id,
            instance_id,
            attached_node_id,
        } => {
            for (key, record) in scan_boundary_subscriptions(db, tenant_id, instance_id)? {
                if record.attached_node_id == attached_node_id.as_str() {
                    delete_boundary_subscription_record(db, batch, &key, &record)?;
                }
            }
        }
        BoundaryProjectionMutation::RemoveInstance {
            tenant_id,
            instance_id,
        } => {
            for (key, record) in scan_boundary_subscriptions(db, tenant_id, instance_id)? {
                delete_boundary_subscription_record(db, batch, &key, &record)?;
            }
        }
    }
    Ok(())
}

fn delete_boundary_subscription(
    db: &DB,
    batch: &mut WriteBatch,
    key: &BoundarySubscriptionKey,
) -> Result<(), BoundaryRuntimeError> {
    let storage_key = boundary_subscription_storage_key(key);
    let Some(bytes) = db
        .get_cf(boundary_subscriptions_cf(db)?, &storage_key)
        .map_err(boundary_unavailable)?
    else {
        return Ok(());
    };
    let record = decode_boundary_subscription(&bytes)?;
    delete_boundary_subscription_record(db, batch, &storage_key, &record)
}

fn delete_boundary_subscription_record(
    db: &DB,
    batch: &mut WriteBatch,
    storage_key: &[u8],
    record: &BoundarySubscriptionRecord,
) -> Result<(), BoundaryRuntimeError> {
    if record.timer_schedule.is_some() {
        batch.delete_cf(
            boundary_timer_index_cf(db)?,
            boundary_due_index_key(record.available_at_epoch_ms, storage_key),
        );
    }
    batch.delete_cf(boundary_subscriptions_cf(db)?, storage_key);
    Ok(())
}

fn scan_boundary_subscriptions(
    db: &DB,
    tenant_id: &TenantId,
    instance_id: &InstanceId,
) -> Result<Vec<(Vec<u8>, BoundarySubscriptionRecord)>, BoundaryRuntimeError> {
    let prefix = boundary_subscription_prefix(tenant_id, instance_id);
    let mut records = Vec::new();
    for item in db.iterator_cf(
        boundary_subscriptions_cf(db)?,
        IteratorMode::From(&prefix, Direction::Forward),
    ) {
        let (key, value) = item.map_err(boundary_unavailable)?;
        if !key.starts_with(&prefix) {
            break;
        }
        records.push((key.to_vec(), decode_boundary_subscription(value.as_ref())?));
    }
    Ok(records)
}

fn correlate_boundary_subscription(
    db: &DB,
    signal: &BoundarySignal,
    max_subscriptions_per_instance: usize,
) -> Result<Option<ProjectedBoundarySubscription>, BoundaryRuntimeError> {
    let records = scan_boundary_subscriptions(db, &signal.tenant_id, &signal.instance_id)?;
    if records.len() > max_subscriptions_per_instance {
        return Err(BoundaryRuntimeError::CorrelationScanLimitExceeded);
    }
    let mut matched = None;
    for (_, record) in records {
        let subscription = subscription_from_record(&record)?;
        if rocks_trigger_matches_signal(&subscription.trigger, signal) {
            if matched.is_some() {
                return Err(BoundaryRuntimeError::AmbiguousCorrelation);
            }
            matched = Some(subscription);
        }
    }
    Ok(matched)
}

fn rocks_trigger_matches_signal(trigger: &BoundaryTrigger, signal: &BoundarySignal) -> bool {
    match (trigger, signal.kind) {
        (BoundaryTrigger::Message { message_ref }, BoundarySignalKind::Message) => {
            signal.reference.as_ref() == Some(message_ref)
        }
        (BoundaryTrigger::Error { error_ref }, BoundarySignalKind::Error) => {
            error_ref.is_none() || error_ref.as_ref() == signal.reference.as_ref()
        }
        _ => false,
    }
}

fn update_boundary_signal_claim(
    db: &DB,
    claim: &ClaimedCorrelation,
    retry_at_epoch_ms: u64,
    dead_letter: bool,
    completed: bool,
) -> Result<(), BoundaryRuntimeError> {
    let key = boundary_signal_storage_key(&claim.signal.tenant_id, &claim.signal.signal_id);
    let mut record = load_boundary_signal_record(db, &key)?;
    if record.lease_version != claim.lease_version || record.worker_id.is_empty() {
        return Err(BoundaryRuntimeError::LeaseConflict);
    }
    let mut batch = WriteBatch::default();
    batch.delete_cf(
        boundary_signal_index_cf(db)?,
        boundary_due_index_key(record.available_at_epoch_ms, &key),
    );
    record.available_at_epoch_ms = retry_at_epoch_ms;
    record.lease_until_epoch_ms = 0;
    record.worker_id.clear();
    record.dead_lettered = dead_letter;
    record.completed = completed;
    if !dead_letter && !completed {
        batch.put_cf(
            boundary_signal_index_cf(db)?,
            boundary_due_index_key(retry_at_epoch_ms, &key),
            [],
        );
    }
    batch.put_cf(boundary_signals_cf(db)?, key, record.encode_to_vec());
    write_boundary_batch(db, batch)
}

fn validate_boundary_timer_claim(
    record: &BoundarySubscriptionRecord,
    claim: &ClaimedTimer,
) -> Result<(), BoundaryRuntimeError> {
    if record.lease_version != claim.lease_version
        || record.generation != claim.generation
        || record.worker_id.is_empty()
    {
        Err(BoundaryRuntimeError::LeaseConflict)
    } else {
        Ok(())
    }
}

fn subscription_to_record(
    subscription: &ProjectedBoundarySubscription,
) -> BoundarySubscriptionRecord {
    let (trigger_kind, trigger_reference) = boundary_trigger_to_storage(&subscription.trigger);
    let mut record = BoundarySubscriptionRecord {
        storage_schema_version: STORAGE_SCHEMA_VERSION,
        tenant_id: subscription.key.tenant_id.to_string(),
        instance_id: subscription.key.instance_id.to_string(),
        boundary_event_id: subscription.key.boundary_event_id.to_string(),
        attached_node_id: subscription.attached_node_id.to_string(),
        target_node_id: subscription.target_node_id.to_string(),
        cancel_activity: subscription.cancel_activity,
        trigger_kind,
        trigger_reference,
        armed_at_epoch_ms: subscription.armed_at_epoch_ms,
        armed_event_id: subscription.armed_event_id.clone(),
        timer_schedule: None,
        generation: 0,
        attempts: 0,
        available_at_epoch_ms: 0,
        lease_until_epoch_ms: 0,
        lease_version: 0,
        worker_id: String::new(),
        dead_lettered: false,
        workflow_type: subscription.workflow_type.to_string(),
        workflow_version: subscription.workflow_version.to_string(),
    };
    set_record_schedule(&mut record, subscription.timer_schedule);
    if let Some(schedule) = subscription.timer_schedule {
        record.available_at_epoch_ms = schedule.due_at_epoch_ms;
    }
    record
}

fn subscription_from_record(
    record: &BoundarySubscriptionRecord,
) -> Result<ProjectedBoundarySubscription, BoundaryRuntimeError> {
    validate_boundary_storage_schema(record.storage_schema_version)?;
    Ok(ProjectedBoundarySubscription {
        key: BoundarySubscriptionKey {
            tenant_id: TenantId::new(record.tenant_id.clone())
                .map_err(boundary_identifier_error)?,
            instance_id: InstanceId::new(record.instance_id.clone())
                .map_err(boundary_identifier_error)?,
            boundary_event_id: NodeId::new(record.boundary_event_id.clone())
                .map_err(boundary_identifier_error)?,
        },
        attached_node_id: NodeId::new(record.attached_node_id.clone())
            .map_err(boundary_identifier_error)?,
        target_node_id: NodeId::new(record.target_node_id.clone())
            .map_err(boundary_identifier_error)?,
        cancel_activity: record.cancel_activity,
        trigger: boundary_trigger_from_storage(record.trigger_kind, &record.trigger_reference)?,
        armed_at_epoch_ms: record.armed_at_epoch_ms,
        armed_event_id: non_empty_boundary(&record.armed_event_id, "armed_event_id")?.to_owned(),
        timer_schedule: record_schedule(record)?,
        workflow_type: WorkflowType::new(record.workflow_type.clone())
            .map_err(boundary_identifier_error)?,
        workflow_version: WorkflowVersion::new(record.workflow_version.clone())
            .map_err(boundary_identifier_error)?,
    })
}

fn set_record_schedule(record: &mut BoundarySubscriptionRecord, schedule: Option<TimerSchedule>) {
    record.timer_schedule = schedule.map(|schedule| BoundaryTimerScheduleRecord {
        due_at_epoch_ms: schedule.due_at_epoch_ms,
        interval_ms: schedule.interval_ms,
        remaining_firings: schedule.remaining_firings,
    });
}

fn record_schedule(
    record: &BoundarySubscriptionRecord,
) -> Result<Option<TimerSchedule>, BoundaryRuntimeError> {
    let Some(schedule) = &record.timer_schedule else {
        return Ok(None);
    };
    if schedule.due_at_epoch_ms == 0
        || schedule.interval_ms == Some(0)
        || schedule.remaining_firings == Some(0)
    {
        return Err(BoundaryRuntimeError::Store(
            "invalid persisted timer schedule".into(),
        ));
    }
    Ok(Some(TimerSchedule {
        due_at_epoch_ms: schedule.due_at_epoch_ms,
        interval_ms: schedule.interval_ms,
        remaining_firings: schedule.remaining_firings,
    }))
}

fn signal_to_record(signal: &BoundarySignal) -> BoundarySignalRecord {
    BoundarySignalRecord {
        storage_schema_version: STORAGE_SCHEMA_VERSION,
        signal_id: signal.signal_id.clone(),
        tenant_id: signal.tenant_id.to_string(),
        instance_id: signal.instance_id.to_string(),
        signal_kind: match signal.kind {
            BoundarySignalKind::Message => 1,
            BoundarySignalKind::Error => 2,
        },
        reference: signal.reference.clone(),
        occurred_at_epoch_ms: signal.occurred_at_epoch_ms,
        authorization_context_ref: signal.authorization_context_ref.clone(),
        attempts: 0,
        available_at_epoch_ms: signal.occurred_at_epoch_ms,
        lease_until_epoch_ms: 0,
        lease_version: 0,
        worker_id: String::new(),
        completed: false,
        dead_lettered: false,
    }
}

fn signal_from_record(
    record: &BoundarySignalRecord,
) -> Result<BoundarySignal, BoundaryRuntimeError> {
    validate_boundary_storage_schema(record.storage_schema_version)?;
    let kind = match record.signal_kind {
        1 => BoundarySignalKind::Message,
        2 => BoundarySignalKind::Error,
        _ => {
            return Err(BoundaryRuntimeError::Store(
                "invalid persisted boundary signal kind".into(),
            ));
        }
    };
    Ok(BoundarySignal {
        signal_id: non_empty_boundary(&record.signal_id, "signal_id")?.to_owned(),
        tenant_id: TenantId::new(record.tenant_id.clone()).map_err(boundary_identifier_error)?,
        instance_id: InstanceId::new(record.instance_id.clone())
            .map_err(boundary_identifier_error)?,
        kind,
        reference: record.reference.clone(),
        occurred_at_epoch_ms: record.occurred_at_epoch_ms,
        authorization_context_ref: non_empty_boundary(
            &record.authorization_context_ref,
            "authorization_context_ref",
        )?
        .to_owned(),
    })
}

fn boundary_trigger_to_storage(trigger: &BoundaryTrigger) -> (i32, String) {
    match trigger {
        BoundaryTrigger::Timer { kind, expression } => (
            match kind {
                BoundaryTimerKind::Date => 1,
                BoundaryTimerKind::Duration => 2,
                BoundaryTimerKind::Cycle => 3,
            },
            expression.clone(),
        ),
        BoundaryTrigger::Error { error_ref } => (4, error_ref.clone().unwrap_or_default()),
        BoundaryTrigger::Message { message_ref } => (5, message_ref.clone()),
    }
}

fn boundary_trigger_from_storage(
    kind: i32,
    reference: &str,
) -> Result<BoundaryTrigger, BoundaryRuntimeError> {
    match kind {
        1 => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Date,
            expression: non_empty_boundary(reference, "timer_expression")?.to_owned(),
        }),
        2 => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Duration,
            expression: non_empty_boundary(reference, "timer_expression")?.to_owned(),
        }),
        3 => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Cycle,
            expression: non_empty_boundary(reference, "timer_expression")?.to_owned(),
        }),
        4 => Ok(BoundaryTrigger::Error {
            error_ref: (!reference.is_empty()).then(|| reference.to_owned()),
        }),
        5 => Ok(BoundaryTrigger::Message {
            message_ref: non_empty_boundary(reference, "message_ref")?.to_owned(),
        }),
        _ => Err(BoundaryRuntimeError::Store(
            "invalid persisted boundary trigger kind".into(),
        )),
    }
}

fn load_boundary_subscription_record(
    db: &DB,
    key: &[u8],
) -> Result<BoundarySubscriptionRecord, BoundaryRuntimeError> {
    let bytes = db
        .get_cf(boundary_subscriptions_cf(db)?, key)
        .map_err(boundary_unavailable)?
        .ok_or(BoundaryRuntimeError::LeaseConflict)?;
    decode_boundary_subscription(&bytes)
}

fn load_boundary_signal_record(
    db: &DB,
    key: &[u8],
) -> Result<BoundarySignalRecord, BoundaryRuntimeError> {
    let bytes = db
        .get_cf(boundary_signals_cf(db)?, key)
        .map_err(boundary_unavailable)?
        .ok_or(BoundaryRuntimeError::LeaseConflict)?;
    decode_boundary_signal(&bytes)
}

fn decode_boundary_subscription(
    bytes: &[u8],
) -> Result<BoundarySubscriptionRecord, BoundaryRuntimeError> {
    let record = BoundarySubscriptionRecord::decode(bytes)
        .map_err(|error| BoundaryRuntimeError::Store(error.to_string()))?;
    validate_boundary_storage_schema(record.storage_schema_version)?;
    Ok(record)
}

fn decode_boundary_signal(bytes: &[u8]) -> Result<BoundarySignalRecord, BoundaryRuntimeError> {
    let record = BoundarySignalRecord::decode(bytes)
        .map_err(|error| BoundaryRuntimeError::Store(error.to_string()))?;
    validate_boundary_storage_schema(record.storage_schema_version)?;
    Ok(record)
}

fn validate_boundary_storage_schema(version: u32) -> Result<(), BoundaryRuntimeError> {
    if version == STORAGE_SCHEMA_VERSION {
        Ok(())
    } else {
        Err(BoundaryRuntimeError::Store(format!(
            "unsupported boundary storage schema {version}"
        )))
    }
}

fn read_boundary_u64(db: &DB, key: &[u8]) -> Result<u64, BoundaryRuntimeError> {
    let value = db
        .get_cf(boundary_meta_cf(db)?, key)
        .map_err(boundary_unavailable)?;
    decode_u64_value(value.as_deref()).map_err(BoundaryRuntimeError::Store)
}

fn write_boundary_batch(db: &DB, batch: WriteBatch) -> Result<(), BoundaryRuntimeError> {
    let mut options = WriteOptions::default();
    options.set_sync(true);
    db.write_opt(batch, &options).map_err(boundary_unavailable)
}

fn split_due_index_key(key: &[u8]) -> Result<(u64, &[u8]), BoundaryRuntimeError> {
    let (due, suffix) = key
        .split_at_checked(8)
        .ok_or_else(|| BoundaryRuntimeError::Store("invalid boundary due-index key".into()))?;
    let due: [u8; 8] = due
        .try_into()
        .map_err(|_| BoundaryRuntimeError::Store("invalid boundary due-index key".into()))?;
    if suffix.is_empty() {
        return Err(BoundaryRuntimeError::Store(
            "empty boundary due-index suffix".into(),
        ));
    }
    Ok((u64::from_be_bytes(due), suffix))
}

fn boundary_due_index_key(available_at_epoch_ms: u64, storage_key: &[u8]) -> Vec<u8> {
    let mut key = available_at_epoch_ms.to_be_bytes().to_vec();
    key.extend_from_slice(storage_key);
    key
}

fn boundary_subscription_prefix(tenant: &TenantId, instance: &InstanceId) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, instance.as_str());
    key
}

fn boundary_subscription_storage_key(key: &BoundarySubscriptionKey) -> Vec<u8> {
    let mut storage_key = boundary_subscription_prefix(&key.tenant_id, &key.instance_id);
    push_component(&mut storage_key, key.boundary_event_id.as_str());
    storage_key
}

fn boundary_signal_storage_key(tenant: &TenantId, signal_id: &str) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, signal_id);
    key
}

fn non_empty_boundary<'a>(value: &'a str, field: &str) -> Result<&'a str, BoundaryRuntimeError> {
    if value.trim().is_empty() {
        Err(BoundaryRuntimeError::Store(format!(
            "persisted boundary field {field} is empty"
        )))
    } else {
        Ok(value)
    }
}

fn boundary_identifier_error(error: impl std::fmt::Display) -> BoundaryRuntimeError {
    BoundaryRuntimeError::Store(error.to_string())
}

fn boundary_lock_error(error: impl std::fmt::Display) -> BoundaryRuntimeError {
    BoundaryRuntimeError::Store(error.to_string())
}

#[allow(clippy::needless_pass_by_value)]
fn boundary_unavailable(error: rocksdb::Error) -> BoundaryRuntimeError {
    BoundaryRuntimeError::Store(error.to_string())
}

fn boundary_subscriptions_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, BoundaryRuntimeError> {
    boundary_named_cf(db, BOUNDARY_SUBSCRIPTIONS_CF)
}

fn boundary_timer_index_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, BoundaryRuntimeError> {
    boundary_named_cf(db, BOUNDARY_TIMER_INDEX_CF)
}

fn boundary_signals_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, BoundaryRuntimeError> {
    boundary_named_cf(db, BOUNDARY_SIGNALS_CF)
}

fn boundary_signal_index_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, BoundaryRuntimeError> {
    boundary_named_cf(db, BOUNDARY_SIGNAL_INDEX_CF)
}

fn boundary_meta_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, BoundaryRuntimeError> {
    boundary_named_cf(db, BOUNDARY_META_CF)
}

fn boundary_named_cf<'a>(
    db: &'a DB,
    name: &str,
) -> Result<&'a rocksdb::ColumnFamily, BoundaryRuntimeError> {
    db.cf_handle(name)
        .ok_or_else(|| BoundaryRuntimeError::Store(format!("missing RocksDB column family {name}")))
}

fn build_commit_batch(
    db: &DB,
    request: &CommitRequest,
    prepared_events: Vec<(EventEnvelope, EncryptedEventRecord, Vec<u8>)>,
    prepared_snapshot: Option<(Vec<u8>, EncryptedSnapshotRecord)>,
    prepared_audit: (Vec<u8>, EncryptedAuthorizationAuditRecord),
    outbox_tail: u64,
) -> Result<WriteBatch, StoreError> {
    if db
        .get_cf(cf(db, AUTHORIZATION_AUDIT_CF)?, &prepared_audit.0)
        .map_err(unavailable)?
        .is_some()
    {
        return Err(StoreError::InvalidAuthorizationAudit);
    }
    let mut batch = WriteBatch::default();
    let mut outbox_sequence = outbox_tail;
    for (event, record, event_key) in prepared_events {
        let dedup_key = dedup_storage_key(&request.tenant_id, &event.metadata.event_id);
        if db
            .get_cf(cf(db, DEDUP_CF)?, &dedup_key)
            .map_err(unavailable)?
            .is_some()
        {
            return Err(StoreError::DuplicateEvent);
        }
        batch.put_cf(cf(db, EVENTS_CF)?, &event_key, record.encode_to_vec());
        batch.put_cf(cf(db, DEDUP_CF)?, dedup_key, []);
        outbox_sequence = outbox_sequence
            .checked_add(1)
            .ok_or_else(|| StoreError::Unavailable("outbox sequence overflow".into()))?;
        batch.put_cf(
            cf(db, OUTBOX_CF)?,
            outbox_sequence.to_be_bytes(),
            OutboxEntry {
                tenant_id: request.tenant_id.to_string(),
                instance_id: request.instance_id.to_string(),
                sequence: event.metadata.sequence,
                event_id: event.metadata.event_id,
                outbox_sequence,
            }
            .encode_to_vec(),
        );
    }
    batch.put_cf(
        cf(db, OUTBOX_META_CF)?,
        OUTBOX_TAIL_KEY,
        outbox_sequence.to_be_bytes(),
    );
    if let Some((snapshot_key, snapshot)) = prepared_snapshot {
        batch.put_cf(
            cf(db, SNAPSHOTS_CF)?,
            snapshot_key,
            snapshot.encode_to_vec(),
        );
    }
    batch.put_cf(
        cf(db, STREAM_META_CF)?,
        stream_meta_key(&request.tenant_id, &request.instance_id),
        request.result.version.to_be_bytes(),
    );
    batch.put_cf(
        cf(db, IDEMPOTENCY_CF)?,
        idempotency_storage_key(
            &request.tenant_id,
            &request.actor_id,
            &request.idempotency_key,
        ),
        command_result(request).encode_to_vec(),
    );
    batch.put_cf(
        cf(db, AUTHORIZATION_AUDIT_CF)?,
        prepared_audit.0,
        prepared_audit.1.encode_to_vec(),
    );
    Ok(batch)
}

fn prepare_encrypted_authorization_audit<C: PayloadCryptoPort>(
    crypto: &C,
    request: &CommitRequest,
) -> Result<(Vec<u8>, EncryptedAuthorizationAuditRecord), StoreError> {
    let audit = &request.authorization_audit;
    let key = authorization_audit_storage_key(&audit.tenant_id, &audit.command_id);
    let plaintext = ContractAuthorizationAuditRecord {
        decision_id: audit.decision_id.clone(),
        tenant_id: audit.tenant_id.to_string(),
        actor_id: audit.actor_id.to_string(),
        workload_id: audit.workload_id.clone(),
        action: audit.action.clone(),
        resource_ref: audit.instance_id.to_string(),
        decision: AuthorizationDecisionType::Allow.into(),
        deny_reason_code: String::new(),
        policy_version: audit.policy_version.to_string(),
        revoke_epoch: audit.revoke_epoch,
        occurred_at_epoch_ms: audit.occurred_at_epoch_ms,
        command_id: audit.command_id.to_string(),
        correlation_id: audit.correlation_id.to_string(),
        workflow_type: audit.workflow_type.to_string(),
        workflow_version: audit.workflow_version.to_string(),
        instance_id: audit.instance_id.to_string(),
        active_node_id: audit.active_node_id.clone(),
        roles: audit.roles.clone(),
        config_version: audit.config_version.to_string(),
        bundle_sequence: audit.bundle_sequence,
        matched_grant_ids: audit.matched_grant_ids.clone(),
    }
    .encode_to_vec();
    let encrypted = crypto
        .encrypt(
            EncryptionContext {
                key_scope: &audit.encryption_key_scope,
                associated_data: &key,
            },
            &plaintext,
        )
        .map_err(|_| StoreError::CryptoUnavailable)?;
    Ok((
        key,
        EncryptedAuthorizationAuditRecord {
            storage_schema_version: STORAGE_SCHEMA_VERSION,
            key_scope: encrypted.key_scope.to_string(),
            key_version: encrypted.key_version,
            key_epoch: encrypted.key_epoch,
            algorithm: encrypted.algorithm,
            nonce: encrypted.nonce,
            ciphertext: encrypted.ciphertext,
        },
    ))
}

fn load_outbox_record<C: PayloadCryptoPort>(
    db: &DB,
    crypto: &C,
    entry: OutboxEntry,
) -> Result<OutboxRecord, OutboxError> {
    let tenant_id = TenantId::new(entry.tenant_id.clone())
        .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
    let instance_id = InstanceId::new(entry.instance_id.clone())
        .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
    let event_key = event_storage_key(&tenant_id, &instance_id, entry.sequence);
    let bytes = db
        .get_cf(outbox_named_cf(db, EVENTS_CF)?, &event_key)
        .map_err(outbox_unavailable)?
        .ok_or_else(|| OutboxError::StoreUnavailable("outbox event is missing".into()))?;
    let encrypted_record = EncryptedEventRecord::decode(bytes.as_slice())
        .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
    let encrypted = encrypted_payload(encrypted_record)
        .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
    let payload = crypto
        .decrypt(&event_key, &encrypted)
        .map_err(|_| OutboxError::StoreUnavailable("outbox event cannot be decrypted".into()))?;
    Ok(OutboxRecord {
        cursor: entry.outbox_sequence,
        tenant_id: entry.tenant_id,
        instance_id: entry.instance_id,
        event_id: entry.event_id,
        payload,
    })
}

fn read_u64_value(db: &DB, family: &str, key: &[u8]) -> Result<u64, StoreError> {
    let value = db.get_cf(cf(db, family)?, key).map_err(unavailable)?;
    decode_u64_value(value.as_deref()).map_err(StoreError::CorruptData)
}

fn read_outbox_u64(db: &DB, key: &[u8]) -> Result<u64, OutboxError> {
    let value = db
        .get_cf(outbox_meta_cf(db)?, key)
        .map_err(outbox_unavailable)?;
    decode_u64_value(value.as_deref()).map_err(OutboxError::StoreUnavailable)
}

fn decode_u64_value(value: Option<&[u8]>) -> Result<u64, String> {
    let Some(value) = value else {
        return Ok(0);
    };
    let bytes: [u8; 8] = value
        .try_into()
        .map_err(|_| "invalid durable u64 value".to_owned())?;
    Ok(u64::from_be_bytes(bytes))
}

fn decode_outbox_cursor(key: &[u8]) -> Result<u64, OutboxError> {
    let bytes: [u8; 8] = key
        .try_into()
        .map_err(|_| OutboxError::StoreUnavailable("invalid outbox cursor key".into()))?;
    Ok(u64::from_be_bytes(bytes))
}

fn outbox_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, OutboxError> {
    outbox_named_cf(db, OUTBOX_CF)
}

fn outbox_meta_cf(db: &DB) -> Result<&rocksdb::ColumnFamily, OutboxError> {
    outbox_named_cf(db, OUTBOX_META_CF)
}

fn outbox_named_cf<'a>(db: &'a DB, name: &str) -> Result<&'a rocksdb::ColumnFamily, OutboxError> {
    db.cf_handle(name).ok_or_else(|| {
        OutboxError::StoreUnavailable(format!("missing RocksDB column family {name}"))
    })
}

#[allow(clippy::needless_pass_by_value)]
fn outbox_unavailable(error: rocksdb::Error) -> OutboxError {
    OutboxError::StoreUnavailable(error.to_string())
}

fn prepare_encrypted_events<C: PayloadCryptoPort>(
    crypto: &C,
    request: &CommitRequest,
) -> Result<Vec<(EventEnvelope, EncryptedEventRecord, Vec<u8>)>, StoreError> {
    request
        .events
        .iter()
        .map(|event| {
            let key = event_storage_key(
                &request.tenant_id,
                &request.instance_id,
                event.metadata.sequence,
            );
            let encrypted = crypto
                .encrypt(
                    EncryptionContext {
                        key_scope: &event.metadata.encryption_key_scope,
                        associated_data: &key,
                    },
                    &EventCodec::encode(event),
                )
                .map_err(|_| StoreError::CryptoUnavailable)?;
            Ok((event.clone(), encrypted_record(encrypted), key))
        })
        .collect()
}

fn prepare_encrypted_snapshot<C: PayloadCryptoPort>(
    crypto: &C,
    request: &CommitRequest,
) -> Result<Option<(Vec<u8>, EncryptedSnapshotRecord)>, StoreError> {
    request
        .snapshot
        .as_ref()
        .map(|snapshot| {
            let key = snapshot_storage_key(&request.tenant_id, &request.instance_id);
            let encrypted = crypto
                .encrypt(
                    EncryptionContext {
                        key_scope: &snapshot.encryption_key_scope,
                        associated_data: &key,
                    },
                    &SnapshotCodec::encode(snapshot),
                )
                .map_err(|_| StoreError::CryptoUnavailable)?;
            Ok((
                key,
                EncryptedSnapshotRecord {
                    storage_schema_version: STORAGE_SCHEMA_VERSION,
                    snapshot_sequence: snapshot.state.sequence,
                    key_scope: encrypted.key_scope.to_string(),
                    key_version: encrypted.key_version,
                    key_epoch: encrypted.key_epoch,
                    algorithm: encrypted.algorithm,
                    nonce: encrypted.nonce,
                    ciphertext: encrypted.ciphertext,
                },
            ))
        })
        .transpose()
}

fn validate_sequences(request: &CommitRequest) -> Result<(), StoreError> {
    let first = request.events.first().map(|event| event.metadata.sequence);
    if first.is_some_and(|sequence| sequence != request.expected_version + 1)
        || request
            .events
            .windows(2)
            .any(|events| events[1].metadata.sequence != events[0].metadata.sequence + 1)
        || request.events.iter().any(|event| {
            event.metadata.tenant_id != request.tenant_id
                || event.metadata.instance_id != request.instance_id
        })
    {
        return Err(StoreError::NonContiguousSequence);
    }
    if request.snapshot.as_ref().is_some_and(|snapshot| {
        snapshot.tenant_id != request.tenant_id
            || snapshot.instance_id != request.instance_id
            || snapshot.state.sequence <= request.expected_version
            || snapshot.state.sequence > request.result.version
    }) {
        return Err(StoreError::InvalidSnapshot);
    }
    Ok(())
}

fn load_snapshot<C: PayloadCryptoPort>(
    db: &DB,
    crypto: &C,
    tenant_id: &TenantId,
    instance_id: &InstanceId,
) -> Result<Option<SnapshotEnvelope>, StoreError> {
    let key = snapshot_storage_key(tenant_id, instance_id);
    let Some(bytes) = db
        .get_cf(cf(db, SNAPSHOTS_CF)?, &key)
        .map_err(unavailable)?
    else {
        return Ok(None);
    };
    let record = EncryptedSnapshotRecord::decode(bytes.as_slice())
        .map_err(|error| StoreError::CorruptData(error.to_string()))?;
    if record.storage_schema_version != STORAGE_SCHEMA_VERSION {
        return Err(StoreError::CorruptData(
            "unsupported snapshot storage schema version".into(),
        ));
    }
    let recorded_sequence = record.snapshot_sequence;
    let encrypted = EncryptedPayload {
        key_scope: KeyScope::new(record.key_scope)
            .map_err(|error| StoreError::CorruptData(error.to_string()))?,
        key_version: record.key_version,
        key_epoch: record.key_epoch,
        algorithm: record.algorithm,
        nonce: record.nonce,
        ciphertext: record.ciphertext,
    };
    let plaintext = crypto
        .decrypt(&key, &encrypted)
        .map_err(|_| StoreError::CryptoUnavailable)?;
    let snapshot = SnapshotCodec::decode(&plaintext)
        .map_err(|error| StoreError::CorruptData(error.to_string()))?;
    if snapshot.tenant_id != *tenant_id
        || snapshot.instance_id != *instance_id
        || snapshot.state.sequence != recorded_sequence
    {
        return Err(StoreError::CorruptData(
            "snapshot scope or sequence does not match its storage record".into(),
        ));
    }
    Ok(Some(snapshot))
}

fn encrypted_record(payload: EncryptedPayload) -> EncryptedEventRecord {
    EncryptedEventRecord {
        storage_schema_version: STORAGE_SCHEMA_VERSION,
        key_scope: payload.key_scope.to_string(),
        key_version: payload.key_version,
        key_epoch: payload.key_epoch,
        algorithm: payload.algorithm,
        nonce: payload.nonce,
        ciphertext: payload.ciphertext,
    }
}

fn encrypted_payload(record: EncryptedEventRecord) -> Result<EncryptedPayload, StoreError> {
    if record.storage_schema_version != STORAGE_SCHEMA_VERSION {
        return Err(StoreError::CorruptData(
            "unsupported storage schema version".into(),
        ));
    }
    Ok(EncryptedPayload {
        key_scope: KeyScope::new(record.key_scope)
            .map_err(|error| StoreError::CorruptData(error.to_string()))?,
        key_version: record.key_version,
        key_epoch: record.key_epoch,
        algorithm: record.algorithm,
        nonce: record.nonce,
        ciphertext: record.ciphertext,
    })
}

fn command_result(request: &CommitRequest) -> StoredCommandResult {
    StoredCommandResult {
        command_id: request.command_id.to_string(),
        version: request.result.version,
        event_ids: request.result.event_ids.clone(),
        config_version: request.result.config_version.to_string(),
        policy_version: request.result.policy_version.to_string(),
    }
}

fn stored_result(stored: StoredCommandResult) -> Result<CommittedResult, StoreError> {
    Ok(CommittedResult {
        version: stored.version,
        event_ids: stored.event_ids,
        config_version: ConfigVersion::new(stored.config_version)
            .map_err(|error| StoreError::CorruptData(error.to_string()))?,
        policy_version: PolicyVersion::new(stored.policy_version)
            .map_err(|error| StoreError::CorruptData(error.to_string()))?,
    })
}

fn read_version(db: &DB, tenant: &TenantId, instance: &InstanceId) -> Result<u64, StoreError> {
    let value = db
        .get_cf(cf(db, STREAM_META_CF)?, stream_meta_key(tenant, instance))
        .map_err(unavailable)?;
    let Some(value) = value else {
        return Ok(0);
    };
    let bytes: [u8; 8] = value
        .as_slice()
        .try_into()
        .map_err(|_| StoreError::CorruptData("invalid stream version".into()))?;
    Ok(u64::from_be_bytes(bytes))
}

fn cf<'a>(db: &'a DB, name: &str) -> Result<&'a rocksdb::ColumnFamily, StoreError> {
    db.cf_handle(name)
        .ok_or_else(|| StoreError::Unavailable(format!("missing RocksDB column family {name}")))
}

fn stream_prefix(tenant: &TenantId, instance: &InstanceId) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, instance.as_str());
    key
}

fn event_storage_key(tenant: &TenantId, instance: &InstanceId, sequence: u64) -> Vec<u8> {
    let mut key = stream_prefix(tenant, instance);
    key.extend_from_slice(&sequence.to_be_bytes());
    key
}

fn sequence_from_event_key(prefix: &[u8], key: &[u8]) -> Result<u64, StoreError> {
    let sequence = key
        .strip_prefix(prefix)
        .and_then(|suffix| <&[u8; 8]>::try_from(suffix).ok())
        .ok_or_else(|| StoreError::CorruptData("invalid event storage key".into()))?;
    Ok(u64::from_be_bytes(*sequence))
}

fn snapshot_storage_key(tenant: &TenantId, instance: &InstanceId) -> Vec<u8> {
    let mut key = Vec::from(b"snapshot\0".as_slice());
    key.extend_from_slice(&stream_prefix(tenant, instance));
    key
}

fn stream_meta_key(tenant: &TenantId, instance: &InstanceId) -> Vec<u8> {
    stream_prefix(tenant, instance)
}

fn dedup_storage_key(tenant: &TenantId, event_id: &str) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, event_id);
    key
}

fn idempotency_storage_key(
    tenant: &TenantId,
    actor: &ActorId,
    idempotency_key: &IdempotencyKey,
) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, actor.as_str());
    push_component(&mut key, idempotency_key.as_str());
    key
}

fn authorization_audit_storage_key(tenant: &TenantId, command: &CommandId) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, command.as_str());
    key
}

fn push_component(key: &mut Vec<u8>, value: &str) {
    let length = u32::try_from(value.len()).unwrap_or(u32::MAX);
    key.extend_from_slice(&length.to_be_bytes());
    key.extend_from_slice(value.as_bytes());
}

#[allow(clippy::needless_pass_by_value)]
fn unavailable(error: rocksdb::Error) -> StoreError {
    StoreError::Unavailable(error.to_string())
}

#[derive(Debug, Error)]
pub enum RocksDbConfigError {
    #[error("RocksDB path must not be empty")]
    EmptyPath,
    #[error("RocksDB setting {0} must be greater than zero")]
    NonPositive(&'static str),
}

#[derive(Debug, Error)]
pub enum RocksDbOpenError {
    #[error(transparent)]
    Configuration(#[from] RocksDbConfigError),
    #[error("failed to open RocksDB: {0}")]
    Open(String),
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::path::Path;

    use bpmp_domain_core::{
        ActorId, CommandId, ConfigVersion, CorrelationId, DomainEvent, IdempotencyKey, InstanceId,
        KeyScope, Lifecycle, NodeId, PolicyVersion, TenantId, WorkflowType, WorkflowVersion,
        rehydrate,
    };
    use bpmp_engine::{AuthorizationAudit, EVENT_SCHEMA_VERSION, EventMetadata};
    use bpmp_payload_crypto::CryptoError;

    use super::*;

    #[derive(Clone, Copy)]
    struct TestCrypto {
        fail: bool,
    }

    impl PayloadCryptoPort for TestCrypto {
        fn encrypt(
            &self,
            context: EncryptionContext<'_>,
            plaintext: &[u8],
        ) -> Result<EncryptedPayload, CryptoError> {
            if self.fail {
                return Err(CryptoError::KeyUnavailable);
            }
            Ok(EncryptedPayload {
                key_scope: context.key_scope.clone(),
                key_version: "test-key-v1".into(),
                key_epoch: 1,
                algorithm: "test-only-xor".into(),
                nonce: vec![1, 2, 3],
                ciphertext: plaintext.iter().map(|byte| byte ^ 0xA5).collect(),
            })
        }

        fn decrypt(
            &self,
            _associated_data: &[u8],
            payload: &EncryptedPayload,
        ) -> Result<Vec<u8>, CryptoError> {
            if self.fail {
                return Err(CryptoError::KeyUnavailable);
            }
            Ok(payload.ciphertext.iter().map(|byte| byte ^ 0xA5).collect())
        }
    }

    fn config(path: &Path) -> RocksDbConfig {
        RocksDbConfig {
            path: path.to_owned(),
            max_open_files: 64,
            write_buffer_size_bytes: 1_048_576,
            max_background_jobs: 2,
            max_replay_events: 100,
        }
    }

    fn event() -> EventEnvelope {
        EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 42,
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
            },
            event: DomainEvent::WorkflowStarted {
                tenant_id: TenantId::new("tenant-a").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                start_node_id: NodeId::new("start").unwrap(),
                occurred_at_epoch_ms: 42,
            },
        }
    }

    fn boundary_subscription(
        boundary_event_id: &str,
        trigger: BoundaryTrigger,
        timer_schedule: Option<TimerSchedule>,
    ) -> ProjectedBoundarySubscription {
        ProjectedBoundarySubscription {
            key: BoundarySubscriptionKey {
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                boundary_event_id: NodeId::new(boundary_event_id).unwrap(),
            },
            attached_node_id: NodeId::new("work").unwrap(),
            target_node_id: NodeId::new("recovery").unwrap(),
            cancel_activity: false,
            trigger,
            armed_at_epoch_ms: 100,
            armed_event_id: format!("armed-{boundary_event_id}"),
            timer_schedule,
            workflow_type: WorkflowType::new("boundary-workflow").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
        }
    }

    fn request() -> CommitRequest {
        CommitRequest {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            actor_id: ActorId::new("actor-1").unwrap(),
            idempotency_key: IdempotencyKey::new("idempotency-1").unwrap(),
            command_id: CommandId::new("command-1").unwrap(),
            expected_version: 0,
            events: vec![event()],
            snapshot: Some(SnapshotEnvelope {
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                state: bpmp_domain_core::InstanceState {
                    lifecycle: Lifecycle::Active {
                        active_node: NodeId::new("start").unwrap(),
                    },
                    sequence: 1,
                    variables: BTreeMap::default(),
                    active_tokens: BTreeMap::default(),
                    pending_gateway_joins: BTreeMap::default(),
                    active_multi_instances: BTreeMap::default(),
                    active_boundary_subscriptions: BTreeMap::default(),
                },
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            }),
            authorization_audit: AuthorizationAudit {
                decision_id: "allow:command-1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                workload_id: "api-gateway".into(),
                roles: vec!["operator".into()],
                action: "START".into(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                active_node_id: "start".into(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                config_version: ConfigVersion::new("config-1").unwrap(),
                bundle_sequence: 1,
                revoke_epoch: 1,
                occurred_at_epoch_ms: 42,
                command_id: CommandId::new("command-1").unwrap(),
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                matched_grant_ids: vec!["allow-start".into()],
                encryption_key_scope: KeyScope::new("tenant-a/compliance-audit").unwrap(),
            },
            result: CommittedResult {
                version: 1,
                event_ids: vec!["event-1".into()],
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
            },
        }
    }

    #[test]
    #[allow(clippy::too_many_lines)]
    fn commit_is_encrypted_atomic_and_replays_after_reopen() {
        let directory = tempfile::tempdir().unwrap();
        let expected_event = event();
        {
            let store =
                RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                    .unwrap();
            assert!(matches!(
                store.commit(request()).unwrap(),
                CommitOutcome::Committed(_)
            ));

            let key = event_storage_key(
                &TenantId::new("tenant-a").unwrap(),
                &InstanceId::new("instance-1").unwrap(),
                1,
            );
            let stored = store
                .db
                .get_cf(cf(&store.db, EVENTS_CF).unwrap(), key)
                .unwrap()
                .unwrap();
            let record = EncryptedEventRecord::decode(stored.as_slice()).unwrap();
            assert_ne!(record.ciphertext, EventCodec::encode(&expected_event));
            let snapshot_key = snapshot_storage_key(
                &TenantId::new("tenant-a").unwrap(),
                &InstanceId::new("instance-1").unwrap(),
            );
            let snapshot_bytes = store
                .db
                .get_cf(cf(&store.db, SNAPSHOTS_CF).unwrap(), snapshot_key)
                .unwrap()
                .unwrap();
            let snapshot_record =
                EncryptedSnapshotRecord::decode(snapshot_bytes.as_slice()).unwrap();
            assert_eq!(snapshot_record.snapshot_sequence, 1);
            assert_eq!(
                store
                    .db
                    .iterator_cf(cf(&store.db, OUTBOX_CF).unwrap(), IteratorMode::Start)
                    .count(),
                1
            );
            let outbox = store.read_after(0, 10).unwrap();
            assert_eq!(outbox.len(), 1);
            assert_eq!(outbox[0].cursor, 1);
            assert_eq!(outbox[0].event_id, "event-1");
            assert_eq!(
                EventCodec::decode(&outbox[0].payload).unwrap(),
                expected_event
            );
            store.checkpoint(0, 1).unwrap();
            assert_eq!(store.checkpoint(0, 1), Err(OutboxError::CheckpointConflict));
            let audit_key = authorization_audit_storage_key(
                &TenantId::new("tenant-a").unwrap(),
                &CommandId::new("command-1").unwrap(),
            );
            let audit_bytes = store
                .db
                .get_cf(cf(&store.db, AUTHORIZATION_AUDIT_CF).unwrap(), &audit_key)
                .unwrap()
                .unwrap();
            let audit_record =
                EncryptedAuthorizationAuditRecord::decode(audit_bytes.as_slice()).unwrap();
            assert_eq!(audit_record.key_scope, "tenant-a/compliance-audit");
            let audit_plaintext = store
                .crypto
                .decrypt(
                    &audit_key,
                    &EncryptedPayload {
                        key_scope: KeyScope::new(audit_record.key_scope).unwrap(),
                        key_version: audit_record.key_version,
                        key_epoch: audit_record.key_epoch,
                        algorithm: audit_record.algorithm,
                        nonce: audit_record.nonce,
                        ciphertext: audit_record.ciphertext,
                    },
                )
                .unwrap();
            let audit =
                ContractAuthorizationAuditRecord::decode(audit_plaintext.as_slice()).unwrap();
            assert_eq!(audit.actor_id, "actor-1");
            assert_eq!(audit.roles, ["operator"]);
        }

        let reopened =
            RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                .unwrap();
        let loaded = reopened
            .load(
                &TenantId::new("tenant-a").unwrap(),
                &InstanceId::new("instance-1").unwrap(),
            )
            .unwrap();
        assert!(loaded.events.is_empty());
        assert_eq!(loaded.snapshot.as_ref().unwrap().state.sequence, 1);
        assert_eq!(loaded.version, 1);
        assert!(matches!(
            rehydrate(
                loaded.snapshot.map(|snapshot| snapshot.state),
                &loaded
                    .events
                    .iter()
                    .map(|event| event.event.clone())
                    .collect::<Vec<_>>()
            )
            .lifecycle,
            Lifecycle::Active { .. }
        ));
        assert!(matches!(
            reopened.commit(request()).unwrap(),
            CommitOutcome::Duplicate(_)
        ));
    }

    #[test]
    fn boundary_runtime_projection_leases_and_correlation_survive_reopen() {
        let directory = tempfile::tempdir().unwrap();
        let timer = boundary_subscription(
            "timeout",
            BoundaryTrigger::Timer {
                kind: BoundaryTimerKind::Duration,
                expression: "PT1S".into(),
            },
            Some(TimerSchedule {
                due_at_epoch_ms: 1_100,
                interval_ms: None,
                remaining_firings: Some(1),
            }),
        );
        {
            let store =
                RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                    .unwrap();
            store
                .apply_projection(0, 1, &[BoundaryProjectionMutation::Upsert(timer.clone())])
                .unwrap();
            let claims = store.claim_due_timers(1_100, 1_200, "worker-a", 8).unwrap();
            assert_eq!(claims.len(), 1);
            assert_eq!(claims[0].generation, 0);
            assert_eq!(claims[0].attempts, 1);
        }

        let message = boundary_subscription(
            "message-boundary",
            BoundaryTrigger::Message {
                message_ref: "order.cancelled".into(),
            },
            None,
        );
        let signal = BoundarySignal {
            signal_id: "message-1".into(),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            kind: BoundarySignalKind::Message,
            reference: Some("order.cancelled".into()),
            occurred_at_epoch_ms: 1_150,
            authorization_context_ref: "auth-context/message-1".into(),
        };
        {
            let store =
                RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                    .unwrap();
            assert!(
                store
                    .claim_due_timers(1_199, 1_300, "worker-b", 8)
                    .unwrap()
                    .is_empty()
            );
            let reclaimed = store.claim_due_timers(1_200, 1_300, "worker-b", 8).unwrap();
            assert_eq!(reclaimed.len(), 1);
            assert_eq!(reclaimed[0].generation, 0);
            assert_eq!(reclaimed[0].attempts, 2);
            store
                .complete_timer(
                    &reclaimed[0],
                    TimerDispatchCompletion {
                        next_schedule: None,
                    },
                )
                .unwrap();
            store
                .apply_projection(1, 2, &[BoundaryProjectionMutation::Upsert(message)])
                .unwrap();
            assert_eq!(
                store.enqueue_signal(&signal).unwrap(),
                SignalEnqueueOutcome::Enqueued
            );
            let correlations = store
                .claim_correlations(1_150, 1_250, "worker-a", 8, 64)
                .unwrap();
            assert_eq!(correlations.len(), 1);
            assert_eq!(
                correlations[0]
                    .subscription
                    .as_ref()
                    .unwrap()
                    .key
                    .boundary_event_id
                    .as_str(),
                "message-boundary"
            );
            store.complete_correlation(&correlations[0]).unwrap();
        }
        let reopened =
            RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                .unwrap();
        assert_eq!(reopened.projection_checkpoint().unwrap(), 2);
        assert_eq!(
            reopened.enqueue_signal(&signal).unwrap(),
            SignalEnqueueOutcome::Duplicate
        );
        assert!(
            reopened
                .claim_correlations(2_000, 2_100, "worker-c", 8, 64)
                .unwrap()
                .is_empty()
        );
    }

    #[test]
    fn crypto_failure_leaves_every_column_family_unchanged() {
        let directory = tempfile::tempdir().unwrap();
        let store = RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: true })
            .unwrap();
        assert_eq!(store.commit(request()), Err(StoreError::CryptoUnavailable));
        for name in [
            EVENTS_CF,
            SNAPSHOTS_CF,
            STREAM_META_CF,
            DEDUP_CF,
            OUTBOX_CF,
            IDEMPOTENCY_CF,
            AUTHORIZATION_AUDIT_CF,
            OUTBOX_META_CF,
        ] {
            assert_eq!(
                store
                    .db
                    .iterator_cf(cf(&store.db, name).unwrap(), IteratorMode::Start)
                    .count(),
                0,
                "column family {name} must remain empty"
            );
        }
    }
}
