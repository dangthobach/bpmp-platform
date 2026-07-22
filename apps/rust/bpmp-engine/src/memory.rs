//! Non-durable adapters for development and tests with synthetic data only.

use std::collections::BTreeMap;
use std::sync::Mutex;

use bpmp_domain_core::{
    ActorId, CommandId, ConfigError, IdempotencyKey, InstanceId, ResolvedConfigSnapshot, TenantId,
};

use crate::ports::{
    CommitOutcome, CommitRequest, ConfigurationLookup, ConfigurationProviderPort, LoadedInstance,
    StoreError, WorkflowStorePort,
};
use crate::{
    AuthorizationAudit, BoundaryProjectionMutation, BoundaryRuntimeError, BoundaryRuntimeStorePort,
    BoundarySignal, BoundarySignalKind, BoundarySubscriptionKey, ClaimedCorrelation, ClaimedTimer,
    CommittedResult, EventCodec, EventEnvelope, LocalTaskRuntimeError, LocalTaskRuntimeStorePort,
    OutboxError, OutboxRecord, OutboxStorePort, ProjectedBoundarySubscription,
    SignalEnqueueOutcome, SnapshotEnvelope, TimerDispatchCompletion,
};

#[derive(Default)]
pub struct InMemoryConfigurationProvider {
    snapshots: BTreeMap<ConfigurationLookup, ResolvedConfigSnapshot>,
}

impl InMemoryConfigurationProvider {
    pub fn insert(
        &mut self,
        lookup: ConfigurationLookup,
        snapshot: ResolvedConfigSnapshot,
    ) -> Option<ResolvedConfigSnapshot> {
        self.snapshots.insert(lookup, snapshot)
    }
}

impl ConfigurationProviderPort for InMemoryConfigurationProvider {
    fn resolve(&self, lookup: &ConfigurationLookup) -> Result<ResolvedConfigSnapshot, ConfigError> {
        self.snapshots
            .get(lookup)
            .cloned()
            .ok_or(ConfigError::MissingPublishedSnapshot)
    }
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct StreamKey {
    tenant_id: TenantId,
    instance_id: InstanceId,
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct IdempotencyScope {
    tenant_id: TenantId,
    actor_id: ActorId,
    idempotency_key: IdempotencyKey,
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct AuthorizationAuditKey {
    tenant_id: TenantId,
    command_id: CommandId,
}

#[derive(Debug, Clone)]
struct StoredResult {
    command_id: CommandId,
    result: CommittedResult,
}

#[derive(Default)]
struct MemoryState {
    streams: BTreeMap<StreamKey, Vec<EventEnvelope>>,
    snapshots: BTreeMap<StreamKey, SnapshotEnvelope>,
    idempotency: BTreeMap<IdempotencyScope, StoredResult>,
    authorization_audits: BTreeMap<AuthorizationAuditKey, AuthorizationAudit>,
    outbox: Vec<OutboxRecord>,
    outbox_checkpoint: u64,
    local_task_checkpoint: u64,
}

#[derive(Default)]
pub struct InMemoryWorkflowStore {
    state: Mutex<MemoryState>,
}

impl InMemoryWorkflowStore {
    /// Reads a synthetic authorization audit from the development adapter.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] when the in-memory lock is poisoned.
    pub fn authorization_audit(
        &self,
        tenant_id: &TenantId,
        command_id: &CommandId,
    ) -> Result<Option<AuthorizationAudit>, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        Ok(state
            .authorization_audits
            .get(&AuthorizationAuditKey {
                tenant_id: tenant_id.clone(),
                command_id: command_id.clone(),
            })
            .cloned())
    }

    /// Returns the number of synthetic audit records held by this adapter.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] when the in-memory lock is poisoned.
    pub fn authorization_audit_count(&self) -> Result<usize, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        Ok(state.authorization_audits.len())
    }
}

impl WorkflowStorePort for InMemoryWorkflowStore {
    fn lookup_idempotency(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let stored = state.idempotency.get(&IdempotencyScope {
            tenant_id: tenant_id.clone(),
            actor_id: actor_id.clone(),
            idempotency_key: idempotency_key.clone(),
        });
        match stored {
            Some(stored) if stored.command_id == *command_id => Ok(Some(stored.result.clone())),
            Some(_) => Err(StoreError::IdempotencyConflict),
            None => Ok(None),
        }
    }

    fn load(
        &self,
        tenant_id: &TenantId,
        instance_id: &InstanceId,
    ) -> Result<LoadedInstance, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let stream_key = StreamKey {
            tenant_id: tenant_id.clone(),
            instance_id: instance_id.clone(),
        };
        let stream = state.streams.get(&stream_key).cloned().unwrap_or_default();
        let version = u64::try_from(stream.len())
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let snapshot = state.snapshots.get(&stream_key).cloned();
        let snapshot_sequence = snapshot
            .as_ref()
            .map_or(0, |snapshot| snapshot.state.sequence);
        let events = stream
            .into_iter()
            .filter(|event| event.metadata.sequence > snapshot_sequence)
            .collect();
        Ok(LoadedInstance {
            snapshot,
            events,
            version,
        })
    }

    fn commit(&self, request: CommitRequest) -> Result<CommitOutcome, StoreError> {
        request.validate_authorization_audit()?;
        let mut state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let idempotency_scope = IdempotencyScope {
            tenant_id: request.tenant_id.clone(),
            actor_id: request.actor_id,
            idempotency_key: request.idempotency_key,
        };
        if let Some(stored) = state.idempotency.get(&idempotency_scope) {
            return if stored.command_id == request.command_id {
                Ok(CommitOutcome::Duplicate(stored.result.clone()))
            } else {
                Err(StoreError::IdempotencyConflict)
            };
        }

        let audit_key = AuthorizationAuditKey {
            tenant_id: request.tenant_id.clone(),
            command_id: request.command_id.clone(),
        };
        if state.authorization_audits.contains_key(&audit_key) {
            return Err(StoreError::InvalidAuthorizationAudit);
        }

        let stream_key = StreamKey {
            tenant_id: request.tenant_id.clone(),
            instance_id: request.instance_id.clone(),
        };
        if request.snapshot.as_ref().is_some_and(|snapshot| {
            snapshot.tenant_id != request.tenant_id
                || snapshot.instance_id != request.instance_id
                || snapshot.state.sequence <= request.expected_version
                || snapshot.state.sequence > request.result.version
        }) {
            return Err(StoreError::InvalidSnapshot);
        }
        let outbox_tail = u64::try_from(state.outbox.len())
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let outbox_records = request
            .events
            .iter()
            .enumerate()
            .map(|(index, event)| {
                let offset = u64::try_from(index)
                    .map_err(|error| StoreError::Unavailable(error.to_string()))?;
                let cursor = outbox_tail
                    .checked_add(offset)
                    .and_then(|value| value.checked_add(1))
                    .ok_or_else(|| StoreError::Unavailable("outbox cursor overflow".into()))?;
                Ok(OutboxRecord {
                    cursor,
                    tenant_id: request.tenant_id.to_string(),
                    instance_id: request.instance_id.to_string(),
                    event_id: event.metadata.event_id.clone(),
                    payload: EventCodec::encode(event),
                })
            })
            .collect::<Result<Vec<_>, StoreError>>()?;
        let stream = state.streams.entry(stream_key.clone()).or_default();
        let actual = u64::try_from(stream.len())
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        if actual != request.expected_version {
            return Err(StoreError::VersionConflict {
                expected: request.expected_version,
                actual,
            });
        }
        let first_sequence = request.events.first().map(|event| event.metadata.sequence);
        if first_sequence.is_some_and(|sequence| sequence != actual + 1)
            || request
                .events
                .windows(2)
                .any(|events| events[1].metadata.sequence != events[0].metadata.sequence + 1)
        {
            return Err(StoreError::NonContiguousSequence);
        }
        stream.extend(request.events);
        state.outbox.extend(outbox_records);
        if let Some(snapshot) = request.snapshot {
            state.snapshots.insert(stream_key, snapshot);
        }
        state.idempotency.insert(
            idempotency_scope,
            StoredResult {
                command_id: request.command_id,
                result: request.result.clone(),
            },
        );
        state
            .authorization_audits
            .insert(audit_key, request.authorization_audit);
        Ok(CommitOutcome::Committed(request.result))
    }
}

impl OutboxStorePort for InMemoryWorkflowStore {
    fn publisher_checkpoint(&self) -> Result<u64, OutboxError> {
        self.state
            .lock()
            .map(|state| state.outbox_checkpoint)
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))
    }

    fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError> {
        if limit == 0 {
            return Err(OutboxError::InvalidConfiguration);
        }
        let state = self
            .state
            .lock()
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        Ok(state
            .outbox
            .iter()
            .filter(|record| record.cursor > cursor)
            .take(limit)
            .cloned()
            .collect())
    }

    fn checkpoint(&self, expected: u64, committed: u64) -> Result<(), OutboxError> {
        let mut state = self
            .state
            .lock()
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        if state.outbox_checkpoint != expected {
            return Err(OutboxError::CheckpointConflict);
        }
        let tail = u64::try_from(state.outbox.len())
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        if committed <= expected || committed > tail {
            return Err(OutboxError::StoreUnavailable(
                "outbox checkpoint is outside the committed range".into(),
            ));
        }
        state.outbox_checkpoint = committed;
        Ok(())
    }
}

impl LocalTaskRuntimeStorePort for InMemoryWorkflowStore {
    fn local_task_checkpoint(&self) -> Result<u64, LocalTaskRuntimeError> {
        self.state
            .lock()
            .map(|state| state.local_task_checkpoint)
            .map_err(|error| LocalTaskRuntimeError::Store(error.to_string()))
    }

    fn checkpoint_local_task(
        &self,
        expected: u64,
        committed: u64,
    ) -> Result<(), LocalTaskRuntimeError> {
        let mut state = self
            .state
            .lock()
            .map_err(|error| LocalTaskRuntimeError::Store(error.to_string()))?;
        if state.local_task_checkpoint != expected {
            return Err(LocalTaskRuntimeError::CheckpointConflict);
        }
        if committed <= expected || committed > state.outbox.len() as u64 {
            return Err(LocalTaskRuntimeError::Store(
                "local task checkpoint is outside the committed outbox range".into(),
            ));
        }
        state.local_task_checkpoint = committed;
        Ok(())
    }
}

#[derive(Debug, Clone)]
struct MemoryTimerRecord {
    subscription: ProjectedBoundarySubscription,
    generation: u64,
    attempts: u32,
    available_at_epoch_ms: u64,
    lease_until_epoch_ms: u64,
    lease_version: u64,
    worker_id: String,
    dead_lettered: bool,
}

#[derive(Debug, Clone)]
struct MemorySignalRecord {
    signal: BoundarySignal,
    attempts: u32,
    available_at_epoch_ms: u64,
    lease_until_epoch_ms: u64,
    lease_version: u64,
    worker_id: String,
    completed: bool,
    dead_lettered: bool,
}

#[derive(Default)]
struct MemoryBoundaryRuntimeState {
    projection_checkpoint: u64,
    subscriptions: BTreeMap<BoundarySubscriptionKey, ProjectedBoundarySubscription>,
    timers: BTreeMap<BoundarySubscriptionKey, MemoryTimerRecord>,
    signals: BTreeMap<(TenantId, String), MemorySignalRecord>,
}

#[derive(Default)]
pub struct InMemoryBoundaryRuntimeStore {
    state: Mutex<MemoryBoundaryRuntimeState>,
}

#[allow(clippy::missing_errors_doc)]
impl InMemoryBoundaryRuntimeStore {
    /// Returns one projected subscription for development assertions and diagnostics.
    pub fn subscription(
        &self,
        key: &BoundarySubscriptionKey,
    ) -> Result<Option<ProjectedBoundarySubscription>, BoundaryRuntimeError> {
        Ok(self
            .state
            .lock()
            .map_err(boundary_lock_error)?
            .subscriptions
            .get(key)
            .cloned())
    }

    pub fn pending_timer_count(&self) -> Result<usize, BoundaryRuntimeError> {
        Ok(self
            .state
            .lock()
            .map_err(boundary_lock_error)?
            .timers
            .values()
            .filter(|timer| !timer.dead_lettered)
            .count())
    }

    pub fn dead_letter_count(&self) -> Result<usize, BoundaryRuntimeError> {
        let state = self.state.lock().map_err(boundary_lock_error)?;
        Ok(state
            .timers
            .values()
            .filter(|timer| timer.dead_lettered)
            .count()
            + state
                .signals
                .values()
                .filter(|signal| signal.dead_lettered)
                .count())
    }
}

impl BoundaryRuntimeStorePort for InMemoryBoundaryRuntimeStore {
    fn projection_checkpoint(&self) -> Result<u64, BoundaryRuntimeError> {
        Ok(self
            .state
            .lock()
            .map_err(boundary_lock_error)?
            .projection_checkpoint)
    }

    fn apply_projection(
        &self,
        expected_checkpoint: u64,
        committed_checkpoint: u64,
        mutations: &[BoundaryProjectionMutation],
    ) -> Result<(), BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        if state.projection_checkpoint != expected_checkpoint {
            return Err(BoundaryRuntimeError::ProjectionCheckpointConflict);
        }
        if committed_checkpoint <= expected_checkpoint {
            return Err(BoundaryRuntimeError::NonContiguousProjection);
        }
        for mutation in mutations {
            apply_memory_projection(&mut state, mutation);
        }
        state.projection_checkpoint = committed_checkpoint;
        Ok(())
    }

    fn claim_due_timers(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
    ) -> Result<Vec<ClaimedTimer>, BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        let keys = state
            .timers
            .iter()
            .filter(|(_, timer)| {
                !timer.dead_lettered
                    && timer.available_at_epoch_ms <= now_epoch_ms
                    && timer.lease_until_epoch_ms <= now_epoch_ms
            })
            .map(|(key, _)| key.clone())
            .take(limit)
            .collect::<Vec<_>>();
        keys.into_iter()
            .map(|key| {
                let timer = state
                    .timers
                    .get_mut(&key)
                    .ok_or_else(|| BoundaryRuntimeError::Store("timer disappeared".into()))?;
                timer.attempts = timer.attempts.saturating_add(1);
                timer.lease_version = timer.lease_version.saturating_add(1);
                timer.lease_until_epoch_ms = lease_until_epoch_ms;
                worker_id.clone_into(&mut timer.worker_id);
                Ok(ClaimedTimer {
                    subscription: timer.subscription.clone(),
                    generation: timer.generation,
                    attempts: timer.attempts,
                    lease_version: timer.lease_version,
                })
            })
            .collect()
    }

    fn complete_timer(
        &self,
        claim: &ClaimedTimer,
        completion: TimerDispatchCompletion,
    ) -> Result<(), BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        let key = &claim.subscription.key;
        validate_timer_lease(&state, claim)?;
        if let Some(schedule) = completion.next_schedule {
            let timer = state
                .timers
                .get_mut(key)
                .ok_or(BoundaryRuntimeError::LeaseConflict)?;
            timer.subscription.timer_schedule = Some(schedule);
            timer.generation = timer.generation.saturating_add(1);
            timer.attempts = 0;
            timer.available_at_epoch_ms = schedule.due_at_epoch_ms;
            timer.lease_until_epoch_ms = 0;
            timer.worker_id.clear();
            if let Some(subscription) = state.subscriptions.get_mut(key) {
                subscription.timer_schedule = Some(schedule);
            }
        } else {
            state.timers.remove(key);
            if let Some(subscription) = state.subscriptions.get_mut(key) {
                subscription.timer_schedule = None;
            }
        }
        Ok(())
    }

    fn fail_timer(
        &self,
        claim: &ClaimedTimer,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        validate_timer_lease(&state, claim)?;
        let timer = state
            .timers
            .get_mut(&claim.subscription.key)
            .ok_or(BoundaryRuntimeError::LeaseConflict)?;
        timer.available_at_epoch_ms = retry_at_epoch_ms;
        timer.lease_until_epoch_ms = 0;
        timer.worker_id.clear();
        timer.dead_lettered = dead_letter;
        Ok(())
    }

    fn enqueue_signal(
        &self,
        signal: &BoundarySignal,
    ) -> Result<SignalEnqueueOutcome, BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        let key = (signal.tenant_id.clone(), signal.signal_id.clone());
        match state.signals.get(&key) {
            Some(existing) if existing.signal == *signal => Ok(SignalEnqueueOutcome::Duplicate),
            Some(_) => Err(BoundaryRuntimeError::SignalConflict),
            None => {
                state.signals.insert(
                    key,
                    MemorySignalRecord {
                        signal: signal.clone(),
                        attempts: 0,
                        available_at_epoch_ms: signal.occurred_at_epoch_ms,
                        lease_until_epoch_ms: 0,
                        lease_version: 0,
                        worker_id: String::new(),
                        completed: false,
                        dead_lettered: false,
                    },
                );
                Ok(SignalEnqueueOutcome::Enqueued)
            }
        }
    }

    fn claim_correlations(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
        max_subscriptions_per_instance: usize,
    ) -> Result<Vec<ClaimedCorrelation>, BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        let keys = state
            .signals
            .iter()
            .filter(|(_, signal)| {
                !signal.completed
                    && !signal.dead_lettered
                    && signal.available_at_epoch_ms <= now_epoch_ms
                    && signal.lease_until_epoch_ms <= now_epoch_ms
            })
            .map(|(key, _)| key.clone())
            .take(limit)
            .collect::<Vec<_>>();
        let mut claims = Vec::with_capacity(keys.len());
        for key in keys {
            let signal = state
                .signals
                .get(&key)
                .ok_or_else(|| BoundaryRuntimeError::Store("signal disappeared".into()))?
                .signal
                .clone();
            let matches = matching_subscriptions(&state.subscriptions, &signal);
            if matches.scanned > max_subscriptions_per_instance {
                return Err(BoundaryRuntimeError::CorrelationScanLimitExceeded);
            }
            if matches.matches.len() > 1 {
                return Err(BoundaryRuntimeError::AmbiguousCorrelation);
            }
            let record = state
                .signals
                .get_mut(&key)
                .ok_or_else(|| BoundaryRuntimeError::Store("signal disappeared".into()))?;
            record.attempts = record.attempts.saturating_add(1);
            record.lease_version = record.lease_version.saturating_add(1);
            record.lease_until_epoch_ms = lease_until_epoch_ms;
            worker_id.clone_into(&mut record.worker_id);
            claims.push(ClaimedCorrelation {
                signal,
                subscription: matches.matches.into_iter().next(),
                attempts: record.attempts,
                lease_version: record.lease_version,
            });
        }
        Ok(claims)
    }

    fn complete_correlation(&self, claim: &ClaimedCorrelation) -> Result<(), BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        validate_signal_lease(&state, claim)?;
        let signal = state
            .signals
            .get_mut(&(
                claim.signal.tenant_id.clone(),
                claim.signal.signal_id.clone(),
            ))
            .ok_or(BoundaryRuntimeError::LeaseConflict)?;
        signal.completed = true;
        signal.lease_until_epoch_ms = 0;
        signal.worker_id.clear();
        Ok(())
    }

    fn fail_correlation(
        &self,
        claim: &ClaimedCorrelation,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError> {
        let mut state = self.state.lock().map_err(boundary_lock_error)?;
        validate_signal_lease(&state, claim)?;
        let signal = state
            .signals
            .get_mut(&(
                claim.signal.tenant_id.clone(),
                claim.signal.signal_id.clone(),
            ))
            .ok_or(BoundaryRuntimeError::LeaseConflict)?;
        signal.available_at_epoch_ms = retry_at_epoch_ms;
        signal.lease_until_epoch_ms = 0;
        signal.worker_id.clear();
        signal.dead_lettered = dead_letter;
        Ok(())
    }
}

fn apply_memory_projection(
    state: &mut MemoryBoundaryRuntimeState,
    mutation: &BoundaryProjectionMutation,
) {
    match mutation {
        BoundaryProjectionMutation::Upsert(subscription) => {
            state
                .subscriptions
                .insert(subscription.key.clone(), subscription.clone());
            if let Some(schedule) = subscription.timer_schedule {
                state.timers.insert(
                    subscription.key.clone(),
                    MemoryTimerRecord {
                        subscription: subscription.clone(),
                        generation: 0,
                        attempts: 0,
                        available_at_epoch_ms: schedule.due_at_epoch_ms,
                        lease_until_epoch_ms: 0,
                        lease_version: 0,
                        worker_id: String::new(),
                        dead_lettered: false,
                    },
                );
            }
        }
        BoundaryProjectionMutation::DisarmBoundary(key) => remove_memory_subscription(state, key),
        BoundaryProjectionMutation::DisarmAttached {
            tenant_id,
            instance_id,
            attached_node_id,
        } => {
            let keys = state
                .subscriptions
                .iter()
                .filter(|(key, subscription)| {
                    &key.tenant_id == tenant_id
                        && &key.instance_id == instance_id
                        && &subscription.attached_node_id == attached_node_id
                })
                .map(|(key, _)| key.clone())
                .collect::<Vec<_>>();
            for key in keys {
                remove_memory_subscription(state, &key);
            }
        }
        BoundaryProjectionMutation::RemoveInstance {
            tenant_id,
            instance_id,
        } => {
            let keys = state
                .subscriptions
                .keys()
                .filter(|key| &key.tenant_id == tenant_id && &key.instance_id == instance_id)
                .cloned()
                .collect::<Vec<_>>();
            for key in keys {
                remove_memory_subscription(state, &key);
            }
        }
    }
}

fn remove_memory_subscription(
    state: &mut MemoryBoundaryRuntimeState,
    key: &BoundarySubscriptionKey,
) {
    state.subscriptions.remove(key);
    state.timers.remove(key);
}

struct SubscriptionMatches {
    scanned: usize,
    matches: Vec<ProjectedBoundarySubscription>,
}

fn matching_subscriptions(
    subscriptions: &BTreeMap<BoundarySubscriptionKey, ProjectedBoundarySubscription>,
    signal: &BoundarySignal,
) -> SubscriptionMatches {
    let scoped = subscriptions.values().filter(|subscription| {
        subscription.key.tenant_id == signal.tenant_id
            && subscription.key.instance_id == signal.instance_id
    });
    let mut scanned = 0;
    let mut matches = Vec::new();
    for subscription in scoped {
        scanned += 1;
        if trigger_matches_signal(&subscription.trigger, signal) {
            matches.push(subscription.clone());
        }
    }
    SubscriptionMatches { scanned, matches }
}

fn trigger_matches_signal(
    trigger: &bpmp_domain_core::BoundaryTrigger,
    signal: &BoundarySignal,
) -> bool {
    match (trigger, signal.kind) {
        (
            bpmp_domain_core::BoundaryTrigger::Message { message_ref },
            BoundarySignalKind::Message,
        ) => signal.reference.as_ref() == Some(message_ref),
        (bpmp_domain_core::BoundaryTrigger::Error { error_ref }, BoundarySignalKind::Error) => {
            error_ref.is_none() || error_ref.as_ref() == signal.reference.as_ref()
        }
        _ => false,
    }
}

fn validate_timer_lease(
    state: &MemoryBoundaryRuntimeState,
    claim: &ClaimedTimer,
) -> Result<(), BoundaryRuntimeError> {
    match state.timers.get(&claim.subscription.key) {
        Some(timer)
            if timer.lease_version == claim.lease_version
                && timer.generation == claim.generation
                && !timer.worker_id.is_empty() =>
        {
            Ok(())
        }
        _ => Err(BoundaryRuntimeError::LeaseConflict),
    }
}

fn validate_signal_lease(
    state: &MemoryBoundaryRuntimeState,
    claim: &ClaimedCorrelation,
) -> Result<(), BoundaryRuntimeError> {
    match state.signals.get(&(
        claim.signal.tenant_id.clone(),
        claim.signal.signal_id.clone(),
    )) {
        Some(signal)
            if signal.lease_version == claim.lease_version && !signal.worker_id.is_empty() =>
        {
            Ok(())
        }
        _ => Err(BoundaryRuntimeError::LeaseConflict),
    }
}

#[allow(clippy::needless_pass_by_value)]
fn boundary_lock_error<T>(error: std::sync::PoisonError<T>) -> BoundaryRuntimeError {
    BoundaryRuntimeError::Store(error.to_string())
}

#[cfg(test)]
mod tests {
    use bpmp_domain_core::{
        ConfigVersion, CorrelationId, KeyScope, PolicyVersion, WorkflowType, WorkflowVersion,
    };

    use super::*;

    #[test]
    fn mismatched_authorization_audit_leaves_store_unchanged() {
        let tenant_id = TenantId::new("tenant-a").unwrap();
        let instance_id = InstanceId::new("instance-1").unwrap();
        let command_id = CommandId::new("command-1").unwrap();
        let policy_version = PolicyVersion::new("policy-1").unwrap();
        let config_version = ConfigVersion::new("config-1").unwrap();
        let store = InMemoryWorkflowStore::default();
        let request = CommitRequest {
            tenant_id: tenant_id.clone(),
            instance_id: instance_id.clone(),
            actor_id: ActorId::new("actor-1").unwrap(),
            idempotency_key: IdempotencyKey::new("idempotency-1").unwrap(),
            command_id: command_id.clone(),
            expected_version: 0,
            events: Vec::new(),
            snapshot: None,
            authorization_audit: AuthorizationAudit {
                decision_id: "allow:command-1".into(),
                tenant_id: tenant_id.clone(),
                actor_id: ActorId::new("different-actor").unwrap(),
                workload_id: "gateway".into(),
                roles: vec!["operator".into()],
                action: "START".into(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                instance_id: instance_id.clone(),
                active_node_id: "start".into(),
                policy_version: policy_version.clone(),
                config_version: config_version.clone(),
                bundle_sequence: 1,
                revoke_epoch: 1,
                occurred_at_epoch_ms: 42,
                command_id: command_id.clone(),
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                matched_grant_ids: vec!["allow-start".into()],
                encryption_key_scope: KeyScope::new("tenant-a/compliance-audit").unwrap(),
            },
            result: CommittedResult {
                version: 0,
                event_ids: Vec::new(),
                config_version,
                policy_version,
            },
        };

        assert_eq!(
            store.commit(request),
            Err(StoreError::InvalidAuthorizationAudit)
        );
        assert_eq!(store.authorization_audit_count().unwrap(), 0);
        assert_eq!(store.load(&tenant_id, &instance_id).unwrap().version, 0);
    }
}
