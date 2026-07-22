use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use bpmp_authz_contracts::authorization::v1::{
    AuthorizationAuditRecord as ContractAuthorizationAuditRecord, AuthorizationDecisionType,
};
use bpmp_contracts::storage::v1::{
    BoundarySignalRecord, BoundarySubscriptionRecord, BoundaryTimerScheduleRecord,
    CompensationLedgerRecord, EncryptedAuthorizationAuditRecord, EncryptedEventRecord,
    EncryptedGovernanceRecord, EncryptedSnapshotRecord, GovernanceApprovalAuditRef,
    GovernanceDecisionAuditRecord, OutboxEntry, ReconciliationWorkItemRecord, StoredCommandResult,
};
use bpmp_domain_core::{
    ActorId, BoundaryTimerKind, BoundaryTrigger, CommandId, ConfigVersion, IdempotencyKey,
    InstanceId, KeyScope, NodeId, PolicyVersion, TenantId, WorkflowType, WorkflowVersion,
};
use bpmp_engine::{
    BoundaryProjectionMutation, BoundaryRuntimeError, BoundaryRuntimeStorePort, BoundarySignal,
    BoundarySignalKind, BoundarySubscriptionKey, ClaimedCorrelation, ClaimedTimer, CommitOutcome,
    CommitRequest, CommittedResult, EventCodec, EventEnvelope, GovernanceTransitionPlan,
    LoadedInstance, LocalTaskRuntimeError, LocalTaskRuntimeStorePort, OutboxError, OutboxRecord,
    OutboxStorePort, ProjectedBoundarySubscription, SignalEnqueueOutcome, SnapshotCodec,
    SnapshotEnvelope, StoreError, TimerDispatchCompletion, TimerSchedule, WorkflowStorePort,
};
use bpmp_governance_domain::{CompensationLedgerEntry, CompensationStatus, GovernanceAuditRef};
use bpmp_payload_crypto::{EncryptedPayload, EncryptionContext, PayloadCryptoPort};
use bpmp_raft_state_machine::{
    ApplyOutcome, ApplyResponse, AtomicStateStorage, AtomicStorageError, ExpectedValue, Mutation,
    PreparedAtomicBatch, StateMachineLimits, StorageKey, value_digest,
};
use prost::Message;
use rocksdb::{
    ColumnFamilyDescriptor, DB, Direction, IteratorMode, Options, WriteBatch, WriteOptions,
};
use serde::{Deserialize, Serialize};
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
const LOCAL_TASK_META_CF: &str = "local_task_meta";
const COMPENSATION_LEDGER_CF: &str = "compensation_ledger";
const RECONCILIATION_WORK_ITEMS_CF: &str = "reconciliation_work_items";
const GOVERNANCE_AUDIT_CF: &str = "governance_audit";
const RAFT_APPLIED_COMMANDS_CF: &str = "raft_applied_commands";
const STORAGE_SCHEMA_VERSION: u32 = 1;
const OUTBOX_TAIL_KEY: &[u8] = b"tail";
const OUTBOX_CHECKPOINT_KEY: &[u8] = b"publisher-checkpoint";
const BOUNDARY_PROJECTION_CHECKPOINT_KEY: &[u8] = b"projection-checkpoint";
const LOCAL_TASK_CHECKPOINT_KEY: &[u8] = b"checkpoint";

const fn authoritative_column_families() -> &'static [&'static str] {
    &[
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
        LOCAL_TASK_META_CF,
        COMPENSATION_LEDGER_CF,
        RECONCILIATION_WORK_ITEMS_CF,
        GOVERNANCE_AUDIT_CF,
        RAFT_APPLIED_COMMANDS_CF,
    ]
}

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
    db: Arc<DB>,
    crypto: C,
    max_replay_events: usize,
    // P0 single-node commits serialize here so version/dedup checks and WriteBatch are atomic.
    commit_lock: Arc<Mutex<()>>,
}

#[derive(Clone)]
pub struct RocksDbAtomicStateStorage {
    db: Arc<DB>,
    write_lock: Arc<Mutex<()>>,
    snapshot_column_families: Arc<[String]>,
    max_snapshot_bytes: u64,
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
            LOCAL_TASK_META_CF,
            COMPENSATION_LEDGER_CF,
            RECONCILIATION_WORK_ITEMS_CF,
            GOVERNANCE_AUDIT_CF,
            RAFT_APPLIED_COMMANDS_CF,
        ]
        .into_iter()
        .map(|name| ColumnFamilyDescriptor::new(name, Options::default()));
        let db = DB::open_cf_descriptors(&options, &config.path, descriptors)
            .map_err(|error| RocksDbOpenError::Open(error.to_string()))?;
        Ok(Self {
            db: Arc::new(db),
            crypto,
            max_replay_events: config.max_replay_events,
            commit_lock: Arc::new(Mutex::new(())),
        })
    }

    /// Creates the local durable apply half of the `OpenRaft` state machine.
    ///
    /// `max_snapshot_bytes` is resolved from versioned operational configuration;
    /// snapshots fail closed before unbounded memory growth.
    ///
    /// # Errors
    ///
    /// Returns [`AtomicStorageError`] for a zero snapshot bound.
    pub fn authoritative_state_storage(
        &self,
        max_snapshot_bytes: u64,
    ) -> Result<RocksDbAtomicStateStorage, AtomicStorageError> {
        if max_snapshot_bytes == 0 {
            return Err(AtomicStorageError(
                "max_snapshot_bytes must be positive".into(),
            ));
        }
        Ok(RocksDbAtomicStateStorage {
            db: Arc::clone(&self.db),
            write_lock: Arc::clone(&self.commit_lock),
            snapshot_column_families: Arc::from(
                authoritative_column_families()
                    .iter()
                    .map(ToString::to_string)
                    .collect::<Vec<_>>(),
            ),
            max_snapshot_bytes,
        })
    }

    /// Materializes an engine-approved governance plan into exact bytes for one
    /// `OpenRaft` log entry. All randomness (encryption nonce) is resolved before
    /// proposal; followers apply these bytes unchanged.
    ///
    /// # Errors
    ///
    /// Fails closed when ledger state changed, scope is inconsistent, crypto is
    /// unavailable, or a bounded sequence cannot be represented.
    #[allow(clippy::too_many_lines)]
    pub fn prepare_governance_batch(
        &self,
        plan: &GovernanceTransitionPlan,
        idempotency_scope: Vec<u8>,
    ) -> Result<PreparedAtomicBatch, StoreError> {
        if idempotency_scope.is_empty()
            || plan.event.metadata.sequence != plan.snapshot.state.sequence
            || plan.event.metadata.tenant_id != plan.snapshot.tenant_id
            || plan.event.metadata.instance_id != plan.snapshot.instance_id
            || plan.event.metadata.encryption_key_scope != plan.snapshot.encryption_key_scope
            || plan.ledger_updates.len() != plan.decision.work_items.len()
        {
            return Err(StoreError::InvalidGovernanceTransition(
                "plan scope, sequence, key scope, or obligation count is inconsistent".into(),
            ));
        }
        let bpmp_domain_core::DomainEvent::WorkflowTerminatedForCompliance {
            policy_id,
            request_digest,
            reason_code,
            reconciliation_count,
            ..
        } = &plan.event.event
        else {
            return Err(StoreError::InvalidGovernanceTransition(
                "plan does not contain the compliance terminal event".into(),
            ));
        };
        if request_digest != &plan.decision.request_digest
            || usize::try_from(*reconciliation_count).ok() != Some(plan.decision.work_items.len())
        {
            return Err(StoreError::InvalidGovernanceTransition(
                "terminal event is not bound to the governance decision".into(),
            ));
        }

        let tenant = &plan.event.metadata.tenant_id;
        let instance = &plan.event.metadata.instance_id;
        let key_scope = &plan.event.metadata.encryption_key_scope;
        let command = &plan.event.metadata.causation_command_id;
        let expected_version = plan.event.metadata.sequence.checked_sub(1).ok_or_else(|| {
            StoreError::InvalidGovernanceTransition(
                "terminal event sequence must be positive".into(),
            )
        })?;
        let event_key = event_storage_key(tenant, instance, plan.event.metadata.sequence);
        let encrypted_event = self
            .crypto
            .encrypt(
                EncryptionContext {
                    key_scope,
                    associated_data: &event_key,
                },
                &EventCodec::encode(&plan.event),
            )
            .map_err(|_| StoreError::CryptoUnavailable)?;
        let snapshot_key = snapshot_storage_key(tenant, instance);
        let encrypted_snapshot = self
            .crypto
            .encrypt(
                EncryptionContext {
                    key_scope,
                    associated_data: &snapshot_key,
                },
                &SnapshotCodec::encode(&plan.snapshot),
            )
            .map_err(|_| StoreError::CryptoUnavailable)?;

        let mut preconditions = Vec::new();
        let mut mutations = Vec::new();
        add_current_value_precondition(
            &mut preconditions,
            STREAM_META_CF,
            stream_meta_key(tenant, instance),
            Some(expected_version.to_be_bytes().as_slice()).filter(|_| expected_version > 0),
        );
        add_missing_precondition(&mut preconditions, EVENTS_CF, event_key.clone());
        let dedup_key = dedup_storage_key(tenant, &plan.event.metadata.event_id);
        add_missing_precondition(&mut preconditions, DEDUP_CF, dedup_key.clone());
        add_current_db_precondition(
            &self.db,
            &mut preconditions,
            SNAPSHOTS_CF,
            snapshot_key.clone(),
        )?;

        let outbox_tail = read_u64_value(&self.db, OUTBOX_META_CF, OUTBOX_TAIL_KEY)?;
        let next_outbox = outbox_tail
            .checked_add(1)
            .ok_or_else(|| StoreError::Unavailable("outbox sequence overflow".into()))?;
        add_current_db_precondition(
            &self.db,
            &mut preconditions,
            OUTBOX_META_CF,
            OUTBOX_TAIL_KEY.to_vec(),
        )?;
        add_missing_precondition(
            &mut preconditions,
            OUTBOX_CF,
            next_outbox.to_be_bytes().to_vec(),
        );

        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(EVENTS_CF, event_key),
            value: encrypted_record(encrypted_event).encode_to_vec(),
        });
        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(DEDUP_CF, dedup_key),
            value: Vec::new(),
        });
        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(SNAPSHOTS_CF, snapshot_key),
            value: EncryptedSnapshotRecord {
                storage_schema_version: STORAGE_SCHEMA_VERSION,
                snapshot_sequence: plan.snapshot.state.sequence,
                key_scope: encrypted_snapshot.key_scope.to_string(),
                key_version: encrypted_snapshot.key_version,
                key_epoch: encrypted_snapshot.key_epoch,
                algorithm: encrypted_snapshot.algorithm,
                nonce: encrypted_snapshot.nonce,
                ciphertext: encrypted_snapshot.ciphertext,
            }
            .encode_to_vec(),
        });
        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(STREAM_META_CF, stream_meta_key(tenant, instance)),
            value: plan.event.metadata.sequence.to_be_bytes().to_vec(),
        });
        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(OUTBOX_CF, next_outbox.to_be_bytes().to_vec()),
            value: OutboxEntry {
                tenant_id: tenant.to_string(),
                instance_id: instance.to_string(),
                sequence: plan.event.metadata.sequence,
                event_id: plan.event.metadata.event_id.clone(),
                outbox_sequence: next_outbox,
            }
            .encode_to_vec(),
        });
        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(OUTBOX_META_CF, OUTBOX_TAIL_KEY.to_vec()),
            value: next_outbox.to_be_bytes().to_vec(),
        });

        for update in &plan.ledger_updates {
            let previous_sequence = update.ledger_sequence.checked_sub(1).ok_or_else(|| {
                StoreError::InvalidGovernanceTransition(
                    "ledger update sequence must follow a pending record".into(),
                )
            })?;
            let previous_key = compensation_ledger_storage_key(
                tenant,
                &update.saga_ref,
                update.effect_sequence,
                previous_sequence,
            );
            let previous_bytes = self
                .db
                .get_cf(cf(&self.db, COMPENSATION_LEDGER_CF)?, &previous_key)
                .map_err(unavailable)?
                .ok_or_else(|| {
                    StoreError::InvalidGovernanceTransition(format!(
                        "pending ledger record {} is missing",
                        update.ledger_entry_id
                    ))
                })?;
            let previous = decrypt_governance_record(&self.crypto, &previous_key, &previous_bytes)
                .and_then(|bytes| {
                    CompensationLedgerRecord::decode(bytes.as_slice())
                        .map_err(|error| StoreError::CorruptData(error.to_string()))
                })?;
            validate_pending_ledger_record(&previous, update, previous_sequence)?;
            preconditions.push(bpmp_raft_state_machine::Precondition {
                storage_key: raft_storage_key(COMPENSATION_LEDGER_CF, previous_key),
                expected: ExpectedValue::Digest(value_digest(&previous_bytes)),
            });
            let update_key = compensation_ledger_storage_key(
                tenant,
                &update.saga_ref,
                update.effect_sequence,
                update.ledger_sequence,
            );
            add_missing_precondition(
                &mut preconditions,
                COMPENSATION_LEDGER_CF,
                update_key.clone(),
            );
            let record = compensation_record(update, "RECONCILIATION_REQUIRED");
            mutations.push(Mutation::Put {
                storage_key: raft_storage_key(COMPENSATION_LEDGER_CF, update_key.clone()),
                value: encrypt_governance_record(
                    &self.crypto,
                    key_scope,
                    &update_key,
                    &record.encode_to_vec(),
                )?
                .encode_to_vec(),
            });
        }

        for work_item in &plan.decision.work_items {
            let key = reconciliation_storage_key(tenant, &work_item.reconciliation_id);
            add_missing_precondition(
                &mut preconditions,
                RECONCILIATION_WORK_ITEMS_CF,
                key.clone(),
            );
            let record = ReconciliationWorkItemRecord {
                tenant_id: work_item.tenant_id.clone(),
                instance_id: work_item.instance_id.clone(),
                reconciliation_id: work_item.reconciliation_id.clone(),
                ledger_entry_id: work_item.ledger_entry_id.clone(),
                side_effect_type: work_item.side_effect_type.clone(),
                target_system: work_item.target_system.clone(),
                handler_ref: work_item.handler_ref.clone(),
                opaque_operation_ref: work_item.opaque_operation_ref.clone(),
                deadline_epoch_ms: work_item.deadline_epoch_ms,
                status: "OPEN".into(),
            };
            mutations.push(Mutation::Put {
                storage_key: raft_storage_key(RECONCILIATION_WORK_ITEMS_CF, key.clone()),
                value: encrypt_governance_record(
                    &self.crypto,
                    key_scope,
                    &key,
                    &record.encode_to_vec(),
                )?
                .encode_to_vec(),
            });
        }

        let audit_key = governance_audit_storage_key(tenant, command);
        add_missing_precondition(&mut preconditions, GOVERNANCE_AUDIT_CF, audit_key.clone());
        let audit = GovernanceDecisionAuditRecord {
            tenant_id: tenant.to_string(),
            instance_id: instance.to_string(),
            command_id: command.to_string(),
            policy_id: policy_id.clone(),
            request_digest: plan.decision.request_digest.to_vec(),
            reason_code: reason_code.clone(),
            requester: Some(governance_audit_ref(&plan.decision.requester_audit)),
            approvers: plan
                .decision
                .approver_audits
                .iter()
                .map(governance_audit_ref)
                .collect(),
            occurred_at_epoch_ms: plan.event.metadata.occurred_at_epoch_ms,
        };
        mutations.push(Mutation::Put {
            storage_key: raft_storage_key(GOVERNANCE_AUDIT_CF, audit_key.clone()),
            value: encrypt_governance_record(
                &self.crypto,
                key_scope,
                &audit_key,
                &audit.encode_to_vec(),
            )?
            .encode_to_vec(),
        });

        let result = StoredCommandResult {
            command_id: command.to_string(),
            version: plan.event.metadata.sequence,
            event_ids: vec![plan.event.metadata.event_id.clone()],
            config_version: plan.event.metadata.config_version.to_string(),
            policy_version: plan.event.metadata.policy_version.to_string(),
        };
        Ok(PreparedAtomicBatch::new(
            command.to_string(),
            idempotency_scope,
            preconditions,
            mutations,
            result.encode_to_vec(),
        ))
    }

    /// Prepares one append-only compensation-ledger record for Raft proposal.
    /// External side-effect completion and compensation progress use this same
    /// path; direct local writes are intentionally not exposed.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] for invalid scope, crypto failure, or an empty
    /// command/idempotency identity.
    pub fn prepare_compensation_ledger_batch(
        &self,
        entry: &CompensationLedgerEntry,
        operational_key_scope: &KeyScope,
        command_id: &CommandId,
        idempotency_scope: Vec<u8>,
    ) -> Result<PreparedAtomicBatch, StoreError> {
        if entry.tenant_id.trim().is_empty()
            || entry.instance_id.trim().is_empty()
            || entry.saga_ref.trim().is_empty()
            || entry.ledger_entry_id.trim().is_empty()
            || entry.opaque_operation_ref.trim().is_empty()
            || idempotency_scope.is_empty()
        {
            return Err(StoreError::InvalidGovernanceTransition(
                "compensation ledger entry or command identity is incomplete".into(),
            ));
        }
        let tenant = TenantId::new(entry.tenant_id.clone()).map_err(|error| {
            StoreError::InvalidGovernanceTransition(format!("invalid ledger tenant: {error}"))
        })?;
        InstanceId::new(entry.instance_id.clone()).map_err(|error| {
            StoreError::InvalidGovernanceTransition(format!("invalid ledger instance: {error}"))
        })?;
        let key = compensation_ledger_storage_key(
            &tenant,
            &entry.saga_ref,
            entry.effect_sequence,
            entry.ledger_sequence,
        );
        let status = compensation_status_name(entry.status);
        let encrypted = encrypt_governance_record(
            &self.crypto,
            operational_key_scope,
            &key,
            &compensation_record(entry, status).encode_to_vec(),
        )?;
        Ok(PreparedAtomicBatch::new(
            command_id.to_string(),
            idempotency_scope,
            vec![bpmp_raft_state_machine::Precondition {
                storage_key: raft_storage_key(COMPENSATION_LEDGER_CF, key.clone()),
                expected: ExpectedValue::Missing,
            }],
            vec![Mutation::Put {
                storage_key: raft_storage_key(COMPENSATION_LEDGER_CF, key),
                value: encrypted.encode_to_vec(),
            }],
            entry.ledger_entry_id.as_bytes().to_vec(),
        ))
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

#[derive(Debug, Serialize, Deserialize)]
struct RocksStateSnapshot {
    column_families: Vec<String>,
    records: Vec<RocksStateSnapshotRecord>,
}

#[derive(Debug, Serialize, Deserialize)]
struct RocksStateSnapshotRecord {
    column_family: String,
    key: Vec<u8>,
    value: Vec<u8>,
}

impl AtomicStateStorage for RocksDbAtomicStateStorage {
    fn apply(
        &self,
        batch: &PreparedAtomicBatch,
        limits: &StateMachineLimits,
    ) -> Result<ApplyResponse, AtomicStorageError> {
        if let Err(error) = batch.validate(limits) {
            return Ok(raft_rejected(batch, error.to_string()));
        }
        let _guard = self.write_lock.lock().map_err(raft_lock_error)?;
        let applied_cf = raft_cf(&self.db, RAFT_APPLIED_COMMANDS_CF)?;
        if let Some(bytes) = self
            .db
            .get_cf(applied_cf, &batch.idempotency_scope)
            .map_err(raft_unavailable)?
        {
            let previous: ApplyResponse = serde_json::from_slice(&bytes)
                .map_err(|error| AtomicStorageError(error.to_string()))?;
            if previous.command_id == batch.command_id
                && previous.batch_digest == batch.batch_digest
            {
                return Ok(ApplyResponse {
                    outcome: ApplyOutcome::Duplicate,
                    ..previous
                });
            }
            return Ok(raft_rejected(batch, "idempotency-scope-conflict".into()));
        }

        for (index, condition) in batch.preconditions.iter().enumerate() {
            let family = raft_cf(&self.db, &condition.storage_key.column_family)?;
            let actual = self
                .db
                .get_cf(family, &condition.storage_key.key)
                .map_err(raft_unavailable)?;
            let satisfied = match (&condition.expected, actual.as_deref()) {
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

        let response = ApplyResponse {
            command_id: batch.command_id.clone(),
            batch_digest: batch.batch_digest,
            outcome: ApplyOutcome::Applied,
            response_payload: batch.response_payload.clone(),
        };
        let mut write_batch = WriteBatch::default();
        for mutation in &batch.mutations {
            match mutation {
                Mutation::Put { storage_key, value } => write_batch.put_cf(
                    raft_cf(&self.db, &storage_key.column_family)?,
                    &storage_key.key,
                    value,
                ),
                Mutation::Delete { storage_key } => write_batch.delete_cf(
                    raft_cf(&self.db, &storage_key.column_family)?,
                    &storage_key.key,
                ),
            }
        }
        write_batch.put_cf(
            applied_cf,
            &batch.idempotency_scope,
            serde_json::to_vec(&response).map_err(|error| AtomicStorageError(error.to_string()))?,
        );
        let mut options = WriteOptions::default();
        options.set_sync(true);
        self.db
            .write_opt(write_batch, &options)
            .map_err(raft_unavailable)?;
        Ok(response)
    }

    fn export_snapshot(&self) -> Result<Vec<u8>, AtomicStorageError> {
        let _guard = self.write_lock.lock().map_err(raft_lock_error)?;
        let mut estimated_bytes = 0_u64;
        let mut records = Vec::new();
        for family_name in self.snapshot_column_families.iter() {
            let family = raft_cf(&self.db, family_name)?;
            for item in self.db.iterator_cf(family, IteratorMode::Start) {
                let (key, value) = item.map_err(raft_unavailable)?;
                estimated_bytes = estimated_bytes
                    .checked_add(family_name.len() as u64)
                    .and_then(|size| size.checked_add(key.len() as u64))
                    .and_then(|size| size.checked_add(value.len() as u64))
                    .ok_or_else(|| AtomicStorageError("snapshot size overflow".into()))?;
                if estimated_bytes > self.max_snapshot_bytes {
                    return Err(AtomicStorageError(format!(
                        "snapshot exceeds configured byte limit {}",
                        self.max_snapshot_bytes
                    )));
                }
                records.push(RocksStateSnapshotRecord {
                    column_family: family_name.clone(),
                    key: key.to_vec(),
                    value: value.to_vec(),
                });
            }
        }
        let snapshot = serde_json::to_vec(&RocksStateSnapshot {
            column_families: self.snapshot_column_families.to_vec(),
            records,
        })
        .map_err(|error| AtomicStorageError(error.to_string()))?;
        if snapshot.len() as u64 > self.max_snapshot_bytes {
            return Err(AtomicStorageError(format!(
                "serialized snapshot exceeds configured byte limit {}",
                self.max_snapshot_bytes
            )));
        }
        Ok(snapshot)
    }

    fn install_snapshot(&self, snapshot: &[u8]) -> Result<(), AtomicStorageError> {
        if snapshot.len() as u64 > self.max_snapshot_bytes {
            return Err(AtomicStorageError(format!(
                "snapshot exceeds configured byte limit {}",
                self.max_snapshot_bytes
            )));
        }
        let snapshot: RocksStateSnapshot = serde_json::from_slice(snapshot)
            .map_err(|error| AtomicStorageError(error.to_string()))?;
        let configured = self
            .snapshot_column_families
            .iter()
            .cloned()
            .collect::<BTreeSet<_>>();
        let supplied = snapshot
            .column_families
            .iter()
            .cloned()
            .collect::<BTreeSet<_>>();
        if configured != supplied || supplied.len() != snapshot.column_families.len() {
            return Err(AtomicStorageError(
                "snapshot column-family schema mismatch".into(),
            ));
        }
        if snapshot
            .records
            .iter()
            .any(|record| !configured.contains(&record.column_family))
        {
            return Err(AtomicStorageError(
                "snapshot contains an unknown column family".into(),
            ));
        }

        let _guard = self.write_lock.lock().map_err(raft_lock_error)?;
        let mut write_batch = WriteBatch::default();
        let mut current_bytes = 0_u64;
        for family_name in self.snapshot_column_families.iter() {
            let family = raft_cf(&self.db, family_name)?;
            for item in self.db.iterator_cf(family, IteratorMode::Start) {
                let (key, value) = item.map_err(raft_unavailable)?;
                current_bytes = current_bytes
                    .checked_add(key.len() as u64)
                    .and_then(|size| size.checked_add(value.len() as u64))
                    .ok_or_else(|| AtomicStorageError("current state size overflow".into()))?;
                if current_bytes > self.max_snapshot_bytes {
                    return Err(AtomicStorageError(
                        "current state exceeds configured snapshot install bound".into(),
                    ));
                }
                write_batch.delete_cf(family, key);
            }
        }
        for record in snapshot.records {
            write_batch.put_cf(
                raft_cf(&self.db, &record.column_family)?,
                record.key,
                record.value,
            );
        }
        let mut options = WriteOptions::default();
        options.set_sync(true);
        self.db
            .write_opt(write_batch, &options)
            .map_err(raft_unavailable)
    }
}

impl<C: PayloadCryptoPort> OutboxStorePort for RocksDbWorkflowStore<C> {
    fn publisher_checkpoint(&self) -> Result<u64, OutboxError> {
        read_outbox_u64(&self.db, OUTBOX_CHECKPOINT_KEY)
    }

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

impl<C: PayloadCryptoPort> LocalTaskRuntimeStorePort for RocksDbWorkflowStore<C> {
    fn local_task_checkpoint(&self) -> Result<u64, LocalTaskRuntimeError> {
        read_meta_u64(&self.db, LOCAL_TASK_META_CF, LOCAL_TASK_CHECKPOINT_KEY)
            .map_err(|error| LocalTaskRuntimeError::Store(error.clone()))
    }

    fn checkpoint_local_task(
        &self,
        expected: u64,
        committed: u64,
    ) -> Result<(), LocalTaskRuntimeError> {
        let _guard = self
            .commit_lock
            .lock()
            .map_err(|error| LocalTaskRuntimeError::Store(error.to_string()))?;
        let current = read_meta_u64(&self.db, LOCAL_TASK_META_CF, LOCAL_TASK_CHECKPOINT_KEY)
            .map_err(|error| LocalTaskRuntimeError::Store(error.clone()))?;
        let tail = read_outbox_u64(&self.db, OUTBOX_TAIL_KEY)
            .map_err(|error| LocalTaskRuntimeError::Store(error.to_string()))?;
        if current != expected {
            return Err(LocalTaskRuntimeError::CheckpointConflict);
        }
        if committed <= expected || committed > tail {
            return Err(LocalTaskRuntimeError::Store(
                "local task checkpoint is outside the committed outbox range".into(),
            ));
        }
        let mut options = WriteOptions::default();
        options.set_sync(true);
        self.db
            .put_cf_opt(
                cf(&self.db, LOCAL_TASK_META_CF)
                    .map_err(|error| LocalTaskRuntimeError::Store(error.to_string()))?,
                LOCAL_TASK_CHECKPOINT_KEY,
                committed.to_be_bytes(),
                &options,
            )
            .map_err(|error| LocalTaskRuntimeError::Store(error.to_string()))
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

fn read_meta_u64(db: &DB, column_family: &str, key: &[u8]) -> Result<u64, String> {
    let family = cf(db, column_family).map_err(|error| error.to_string())?;
    let value = db.get_cf(family, key).map_err(|error| error.to_string())?;
    decode_u64_value(value.as_deref())
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

fn raft_storage_key(column_family: &str, key: Vec<u8>) -> StorageKey {
    StorageKey {
        column_family: column_family.into(),
        key,
    }
}

fn add_missing_precondition(
    preconditions: &mut Vec<bpmp_raft_state_machine::Precondition>,
    column_family: &str,
    key: Vec<u8>,
) {
    preconditions.push(bpmp_raft_state_machine::Precondition {
        storage_key: raft_storage_key(column_family, key),
        expected: ExpectedValue::Missing,
    });
}

fn add_current_value_precondition(
    preconditions: &mut Vec<bpmp_raft_state_machine::Precondition>,
    column_family: &str,
    key: Vec<u8>,
    expected: Option<&[u8]>,
) {
    preconditions.push(bpmp_raft_state_machine::Precondition {
        storage_key: raft_storage_key(column_family, key),
        expected: expected.map_or(ExpectedValue::Missing, |value| {
            ExpectedValue::Digest(value_digest(value))
        }),
    });
}

fn add_current_db_precondition(
    db: &DB,
    preconditions: &mut Vec<bpmp_raft_state_machine::Precondition>,
    column_family: &str,
    key: Vec<u8>,
) -> Result<(), StoreError> {
    let current = db
        .get_cf(cf(db, column_family)?, &key)
        .map_err(unavailable)?;
    preconditions.push(bpmp_raft_state_machine::Precondition {
        storage_key: raft_storage_key(column_family, key),
        expected: current.as_deref().map_or(ExpectedValue::Missing, |value| {
            ExpectedValue::Digest(value_digest(value))
        }),
    });
    Ok(())
}

fn compensation_ledger_storage_key(
    tenant: &TenantId,
    saga_ref: &str,
    effect_sequence: u64,
    ledger_sequence: u64,
) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, saga_ref);
    key.extend_from_slice(&effect_sequence.to_be_bytes());
    key.extend_from_slice(&ledger_sequence.to_be_bytes());
    key
}

fn reconciliation_storage_key(tenant: &TenantId, reconciliation_id: &str) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, reconciliation_id);
    key
}

fn governance_audit_storage_key(tenant: &TenantId, command: &CommandId) -> Vec<u8> {
    let mut key = Vec::new();
    push_component(&mut key, tenant.as_str());
    push_component(&mut key, command.as_str());
    key
}

fn compensation_record(entry: &CompensationLedgerEntry, status: &str) -> CompensationLedgerRecord {
    CompensationLedgerRecord {
        tenant_id: entry.tenant_id.clone(),
        instance_id: entry.instance_id.clone(),
        saga_ref: entry.saga_ref.clone(),
        ledger_entry_id: entry.ledger_entry_id.clone(),
        effect_sequence: entry.effect_sequence,
        ledger_sequence: entry.ledger_sequence,
        side_effect_type: entry.side_effect_type.clone(),
        target_system: entry.target_system.clone(),
        handler_ref: entry.handler_ref.clone(),
        opaque_operation_ref: entry.opaque_operation_ref.clone(),
        idempotency_key: entry.idempotency_key.clone(),
        status: status.into(),
        updated_at_epoch_ms: entry.updated_at_epoch_ms,
    }
}

const fn compensation_status_name(status: CompensationStatus) -> &'static str {
    match status {
        CompensationStatus::Pending => "PENDING",
        CompensationStatus::Compensated => "COMPENSATED",
        CompensationStatus::ReconciliationRequired => "RECONCILIATION_REQUIRED",
    }
}

fn validate_pending_ledger_record(
    previous: &CompensationLedgerRecord,
    update: &CompensationLedgerEntry,
    previous_sequence: u64,
) -> Result<(), StoreError> {
    if previous.tenant_id != update.tenant_id
        || previous.instance_id != update.instance_id
        || previous.saga_ref != update.saga_ref
        || previous.ledger_entry_id != update.ledger_entry_id
        || previous.effect_sequence != update.effect_sequence
        || previous.ledger_sequence != previous_sequence
        || previous.side_effect_type != update.side_effect_type
        || previous.target_system != update.target_system
        || previous.handler_ref != update.handler_ref
        || previous.opaque_operation_ref != update.opaque_operation_ref
        || previous.idempotency_key != update.idempotency_key
        || previous.status != "PENDING"
        || update.status != CompensationStatus::ReconciliationRequired
    {
        return Err(StoreError::InvalidGovernanceTransition(format!(
            "pending ledger record {} changed after approval",
            update.ledger_entry_id
        )));
    }
    Ok(())
}

fn encrypt_governance_record<C: PayloadCryptoPort>(
    crypto: &C,
    key_scope: &KeyScope,
    storage_key: &[u8],
    plaintext: &[u8],
) -> Result<EncryptedGovernanceRecord, StoreError> {
    let encrypted = crypto
        .encrypt(
            EncryptionContext {
                key_scope,
                associated_data: storage_key,
            },
            plaintext,
        )
        .map_err(|_| StoreError::CryptoUnavailable)?;
    Ok(EncryptedGovernanceRecord {
        storage_schema_version: STORAGE_SCHEMA_VERSION,
        key_scope: encrypted.key_scope.to_string(),
        key_version: encrypted.key_version,
        key_epoch: encrypted.key_epoch,
        algorithm: encrypted.algorithm,
        nonce: encrypted.nonce,
        ciphertext: encrypted.ciphertext,
    })
}

fn decrypt_governance_record<C: PayloadCryptoPort>(
    crypto: &C,
    storage_key: &[u8],
    bytes: &[u8],
) -> Result<Vec<u8>, StoreError> {
    let record = EncryptedGovernanceRecord::decode(bytes)
        .map_err(|error| StoreError::CorruptData(error.to_string()))?;
    if record.storage_schema_version != STORAGE_SCHEMA_VERSION {
        return Err(StoreError::CorruptData(
            "unsupported governance storage schema version".into(),
        ));
    }
    crypto
        .decrypt(
            storage_key,
            &EncryptedPayload {
                key_scope: KeyScope::new(record.key_scope)
                    .map_err(|error| StoreError::CorruptData(error.to_string()))?,
                key_version: record.key_version,
                key_epoch: record.key_epoch,
                algorithm: record.algorithm,
                nonce: record.nonce,
                ciphertext: record.ciphertext,
            },
        )
        .map_err(|_| StoreError::CryptoUnavailable)
}

fn governance_audit_ref(reference: &GovernanceAuditRef) -> GovernanceApprovalAuditRef {
    GovernanceApprovalAuditRef {
        actor_id: reference.actor_id.clone(),
        approved_at_epoch_ms: reference.approved_at_epoch_ms,
        key_id: reference.key_id.clone(),
    }
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

fn raft_cf<'a>(db: &'a DB, name: &str) -> Result<&'a rocksdb::ColumnFamily, AtomicStorageError> {
    db.cf_handle(name)
        .ok_or_else(|| AtomicStorageError(format!("missing RocksDB column family {name}")))
}

#[allow(clippy::needless_pass_by_value)]
fn raft_unavailable(error: rocksdb::Error) -> AtomicStorageError {
    AtomicStorageError(error.to_string())
}

#[allow(clippy::needless_pass_by_value)]
fn raft_lock_error<T>(error: std::sync::PoisonError<T>) -> AtomicStorageError {
    AtomicStorageError(error.to_string())
}

fn raft_rejected(batch: &PreparedAtomicBatch, reason: String) -> ApplyResponse {
    ApplyResponse {
        command_id: batch.command_id.clone(),
        batch_digest: batch.batch_digest,
        outcome: ApplyOutcome::Rejected { reason },
        response_payload: Vec::new(),
    }
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
    use bpmp_engine::{
        AuthorizationAudit, EVENT_SCHEMA_VERSION, EventMetadata, GovernanceTransitionPlan,
    };
    use bpmp_governance_domain::{
        AbortAndReconcileDecision, CompensationLedgerEntry, CompensationStatus, GovernanceAuditRef,
        ReconciliationWorkItem,
    };
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

    fn raft_limits() -> StateMachineLimits {
        StateMachineLimits {
            max_conditions: 64,
            max_mutations: 64,
            max_batch_bytes: 1024 * 1024,
            append_only_column_families: BTreeSet::from([
                EVENTS_CF.into(),
                DEDUP_CF.into(),
                OUTBOX_CF.into(),
                COMPENSATION_LEDGER_CF.into(),
                RECONCILIATION_WORK_ITEMS_CF.into(),
                GOVERNANCE_AUDIT_CF.into(),
            ]),
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

    fn pending_ledger_entry() -> CompensationLedgerEntry {
        CompensationLedgerEntry {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-1".into(),
            saga_ref: "saga-1".into(),
            ledger_entry_id: "ledger-1".into(),
            effect_sequence: 1,
            ledger_sequence: 1,
            side_effect_type: "payment".into(),
            target_system: "bank".into(),
            handler_ref: "refund-v1".into(),
            opaque_operation_ref: "opaque-operation".into(),
            idempotency_key: "effect-idempotency".into(),
            status: CompensationStatus::Pending,
            updated_at_epoch_ms: 40,
        }
    }

    fn governance_plan() -> GovernanceTransitionPlan {
        let mut update = pending_ledger_entry();
        update.ledger_sequence = 2;
        update.status = CompensationStatus::ReconciliationRequired;
        update.updated_at_epoch_ms = 50;
        let state = bpmp_domain_core::InstanceState {
            lifecycle: Lifecycle::TerminatedForCompliance,
            sequence: 1,
            ..bpmp_domain_core::InstanceState::default()
        };
        let event = EventEnvelope {
            metadata: EventMetadata {
                event_id: "governance-command:1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("governance-correlation").unwrap(),
                causation_command_id: CommandId::new("governance-command").unwrap(),
                occurred_at_epoch_ms: 50,
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                actor_id: ActorId::new("requester").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/governance").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
            },
            event: DomainEvent::WorkflowTerminatedForCompliance {
                policy_id: "erasure-policy".into(),
                request_digest: [8; 32],
                reason_code: "legal-deadline".into(),
                reconciliation_count: 1,
                occurred_at_epoch_ms: 50,
            },
        };
        GovernanceTransitionPlan {
            snapshot: SnapshotEnvelope {
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                state,
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/governance").unwrap(),
            },
            event,
            ledger_updates: vec![update],
            decision: AbortAndReconcileDecision {
                request_digest: [8; 32],
                ledger_digest: [9; 32],
                requester_audit: GovernanceAuditRef {
                    actor_id: "requester".into(),
                    approved_at_epoch_ms: 45,
                    key_id: "requester-key".into(),
                },
                approver_audits: vec![GovernanceAuditRef {
                    actor_id: "approver".into(),
                    approved_at_epoch_ms: 46,
                    key_id: "approver-key".into(),
                }],
                work_items: vec![ReconciliationWorkItem {
                    tenant_id: "tenant-a".into(),
                    instance_id: "instance-1".into(),
                    reconciliation_id: "reconciliation-1".into(),
                    ledger_entry_id: "ledger-1".into(),
                    side_effect_type: "payment".into(),
                    target_system: "bank".into(),
                    handler_ref: "refund-v1".into(),
                    opaque_operation_ref: "opaque-operation".into(),
                    deadline_epoch_ms: 500,
                }],
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
                    active_scopes: BTreeMap::default(),
                    scope_invocation_counts: BTreeMap::default(),
                    active_cases: BTreeMap::default(),
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

    #[test]
    fn raft_state_machine_batch_is_atomic_idempotent_and_snapshot_restorable() {
        let directory = tempfile::tempdir().unwrap();
        let store =
            RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                .unwrap();
        let raft = store.authoritative_state_storage(1024 * 1024).unwrap();
        let limits = StateMachineLimits {
            max_conditions: 16,
            max_mutations: 16,
            max_batch_bytes: 64 * 1024,
            append_only_column_families: BTreeSet::from([
                EVENTS_CF.into(),
                COMPENSATION_LEDGER_CF.into(),
                RECONCILIATION_WORK_ITEMS_CF.into(),
                GOVERNANCE_AUDIT_CF.into(),
            ]),
        };
        let storage_key = |column_family: &str, key: &[u8]| StorageKey {
            column_family: column_family.into(),
            key: key.to_vec(),
        };
        let immutable_keys = [
            storage_key(EVENTS_CF, b"tenant/instance/1"),
            storage_key(COMPENSATION_LEDGER_CF, b"tenant/saga/effect/2"),
            storage_key(RECONCILIATION_WORK_ITEMS_CF, b"tenant/reconcile/1"),
            storage_key(GOVERNANCE_AUDIT_CF, b"tenant/command/requester"),
        ];
        let batch = PreparedAtomicBatch::new(
            "abort-command".into(),
            b"tenant/requester/idempotency".to_vec(),
            immutable_keys
                .iter()
                .cloned()
                .map(|storage_key| bpmp_raft_state_machine::Precondition {
                    storage_key,
                    expected: ExpectedValue::Missing,
                })
                .collect(),
            immutable_keys
                .iter()
                .cloned()
                .zip([
                    b"terminated-event".as_slice(),
                    b"reconciliation-required".as_slice(),
                    b"work-item".as_slice(),
                    b"dual-control-audit".as_slice(),
                ])
                .map(|(storage_key, value)| Mutation::Put {
                    storage_key,
                    value: value.to_vec(),
                })
                .collect(),
            b"committed-version-1".to_vec(),
        );

        let applied = raft.apply(&batch, &limits).unwrap();
        let duplicate = raft.apply(&batch, &limits).unwrap();
        let snapshot = raft.export_snapshot().unwrap();

        assert_eq!(applied.outcome, ApplyOutcome::Applied);
        assert_eq!(duplicate.outcome, ApplyOutcome::Duplicate);
        for key in &immutable_keys {
            assert!(
                store
                    .db
                    .get_cf(cf(&store.db, &key.column_family).unwrap(), &key.key)
                    .unwrap()
                    .is_some()
            );
        }

        let later_key = storage_key(RECONCILIATION_WORK_ITEMS_CF, b"tenant/reconcile/later");
        let later = PreparedAtomicBatch::new(
            "later-command".into(),
            b"tenant/requester/later".to_vec(),
            vec![bpmp_raft_state_machine::Precondition {
                storage_key: later_key.clone(),
                expected: ExpectedValue::Missing,
            }],
            vec![Mutation::Put {
                storage_key: later_key.clone(),
                value: b"later".to_vec(),
            }],
            Vec::new(),
        );
        assert_eq!(
            raft.apply(&later, &limits).unwrap().outcome,
            ApplyOutcome::Applied
        );
        raft.install_snapshot(&snapshot).unwrap();
        assert!(
            store
                .db
                .get_cf(
                    cf(&store.db, &later_key.column_family).unwrap(),
                    &later_key.key,
                )
                .unwrap()
                .is_none()
        );
        assert_eq!(
            raft.apply(&batch, &limits).unwrap().outcome,
            ApplyOutcome::Duplicate
        );
    }

    #[test]
    fn governance_plan_commits_terminal_event_ledger_work_item_and_audit_atomically() {
        let directory = tempfile::tempdir().unwrap();
        let store =
            RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                .unwrap();
        let raft = store.authoritative_state_storage(2 * 1024 * 1024).unwrap();
        let ledger = pending_ledger_entry();
        let ledger_batch = store
            .prepare_compensation_ledger_batch(
                &ledger,
                &KeyScope::new("tenant-a/governance").unwrap(),
                &CommandId::new("record-effect").unwrap(),
                b"tenant/record-effect".to_vec(),
            )
            .unwrap();
        assert_eq!(
            raft.apply(&ledger_batch, &raft_limits()).unwrap().outcome,
            ApplyOutcome::Applied
        );
        let plan = governance_plan();
        let governance_batch = store
            .prepare_governance_batch(&plan, b"tenant/governance-command".to_vec())
            .unwrap();

        let response = raft.apply(&governance_batch, &raft_limits()).unwrap();

        assert_eq!(response.outcome, ApplyOutcome::Applied);
        let loaded = store
            .load(
                &TenantId::new("tenant-a").unwrap(),
                &InstanceId::new("instance-1").unwrap(),
            )
            .unwrap();
        assert_eq!(loaded.version, 1);
        assert_eq!(
            loaded.snapshot.unwrap().state.lifecycle,
            Lifecycle::TerminatedForCompliance
        );
        assert_eq!(
            store
                .db
                .iterator_cf(
                    cf(&store.db, COMPENSATION_LEDGER_CF).unwrap(),
                    IteratorMode::Start,
                )
                .count(),
            2
        );
        assert_eq!(
            store
                .db
                .iterator_cf(
                    cf(&store.db, RECONCILIATION_WORK_ITEMS_CF).unwrap(),
                    IteratorMode::Start,
                )
                .count(),
            1
        );
        assert_eq!(
            store
                .db
                .iterator_cf(
                    cf(&store.db, GOVERNANCE_AUDIT_CF).unwrap(),
                    IteratorMode::Start,
                )
                .count(),
            1
        );
    }

    #[test]
    fn ledger_change_after_governance_prepare_prevents_every_governance_effect() {
        let directory = tempfile::tempdir().unwrap();
        let store =
            RocksDbWorkflowStore::open(config(directory.path()), TestCrypto { fail: false })
                .unwrap();
        let raft = store.authoritative_state_storage(2 * 1024 * 1024).unwrap();
        let ledger = pending_ledger_entry();
        let ledger_batch = store
            .prepare_compensation_ledger_batch(
                &ledger,
                &KeyScope::new("tenant-a/governance").unwrap(),
                &CommandId::new("record-effect").unwrap(),
                b"tenant/record-effect".to_vec(),
            )
            .unwrap();
        raft.apply(&ledger_batch, &raft_limits()).unwrap();
        let governance_batch = store
            .prepare_governance_batch(&governance_plan(), b"tenant/governance-command".to_vec())
            .unwrap();
        let ledger_key =
            compensation_ledger_storage_key(&TenantId::new("tenant-a").unwrap(), "saga-1", 1, 1);
        store
            .db
            .put_cf(
                cf(&store.db, COMPENSATION_LEDGER_CF).unwrap(),
                ledger_key,
                b"injected-concurrent-change",
            )
            .unwrap();

        let response = raft.apply(&governance_batch, &raft_limits()).unwrap();

        assert!(matches!(
            response.outcome,
            ApplyOutcome::PreconditionFailed { .. }
        ));
        for family in [EVENTS_CF, RECONCILIATION_WORK_ITEMS_CF, GOVERNANCE_AUDIT_CF] {
            assert_eq!(
                store
                    .db
                    .iterator_cf(cf(&store.db, family).unwrap(), IteratorMode::Start)
                    .count(),
                0,
                "column family {family} must remain unchanged"
            );
        }
    }
}
