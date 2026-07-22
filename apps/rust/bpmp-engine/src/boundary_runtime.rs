use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use bpmp_domain_core::{
    BoundaryRuntimePolicy, BoundaryTimerKind, BoundaryTrigger, Command, CommandId, CorrelationId,
    DomainEvent, IdempotencyKey, InstanceId, KeyScope, NodeId, TenantId, WorkflowDefinition,
    WorkflowType, WorkflowVersion,
};
use thiserror::Error;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

use crate::application::{AuthorizedCommand, Engine};
use crate::ports::ActorProofKind;
use crate::ports::{AuthorizationProviderPort, ConfigurationProviderPort, WorkflowStorePort};
use crate::{EventCodec, EventEnvelope, OutboxError, OutboxStorePort};

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
pub struct BoundarySubscriptionKey {
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub boundary_event_id: NodeId,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct TimerSchedule {
    pub due_at_epoch_ms: u64,
    pub interval_ms: Option<u64>,
    pub remaining_firings: Option<u32>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ProjectedBoundarySubscription {
    pub key: BoundarySubscriptionKey,
    pub attached_node_id: NodeId,
    pub target_node_id: NodeId,
    pub cancel_activity: bool,
    pub trigger: BoundaryTrigger,
    pub armed_at_epoch_ms: u64,
    pub armed_event_id: String,
    pub timer_schedule: Option<TimerSchedule>,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum BoundaryProjectionMutation {
    Upsert(ProjectedBoundarySubscription),
    DisarmBoundary(BoundarySubscriptionKey),
    DisarmAttached {
        tenant_id: TenantId,
        instance_id: InstanceId,
        attached_node_id: NodeId,
    },
    RemoveInstance {
        tenant_id: TenantId,
        instance_id: InstanceId,
    },
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct BoundaryProjectionRecord {
    pub cursor: u64,
    pub envelope: EventEnvelope,
}

pub trait BoundaryEventSourcePort: Send + Sync {
    /// Reads committed events strictly after `cursor`, preserving outbox order.
    ///
    /// # Errors
    ///
    /// Returns a typed adapter error when committed payloads cannot be read or decoded.
    fn read_after(
        &self,
        cursor: u64,
        limit: usize,
    ) -> Result<Vec<BoundaryProjectionRecord>, BoundaryRuntimeError>;
}

pub struct OutboxBoundaryEventSource<S> {
    store: S,
}

impl<S> OutboxBoundaryEventSource<S> {
    pub const fn new(store: S) -> Self {
        Self { store }
    }
}

impl<S: OutboxStorePort> BoundaryEventSourcePort for OutboxBoundaryEventSource<S> {
    fn read_after(
        &self,
        cursor: u64,
        limit: usize,
    ) -> Result<Vec<BoundaryProjectionRecord>, BoundaryRuntimeError> {
        self.store
            .read_after(cursor, limit)
            .map_err(BoundaryRuntimeError::Outbox)?
            .into_iter()
            .map(|record| {
                let envelope = EventCodec::decode(&record.payload)
                    .map_err(|error| BoundaryRuntimeError::CorruptEvent(error.to_string()))?;
                if envelope.metadata.event_id != record.event_id
                    || envelope.metadata.tenant_id.as_str() != record.tenant_id
                    || envelope.metadata.instance_id.as_str() != record.instance_id
                {
                    return Err(BoundaryRuntimeError::EventScopeMismatch);
                }
                Ok(BoundaryProjectionRecord {
                    cursor: record.cursor,
                    envelope,
                })
            })
            .collect()
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum BoundarySignalKind {
    Message,
    Error,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct BoundarySignal {
    pub signal_id: String,
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub kind: BoundarySignalKind,
    pub reference: Option<String>,
    pub occurred_at_epoch_ms: u64,
    pub authorization_context_ref: String,
}

impl BoundarySignal {
    /// Validates one external correlation signal against configured ingress bounds.
    ///
    /// # Errors
    ///
    /// Rejects missing identifiers, oversized values, or a message without a reference.
    pub fn validate(&self, policy: &BoundaryRuntimePolicy) -> Result<(), BoundaryRuntimeError> {
        if self.signal_id.trim().is_empty()
            || self.signal_id.len() > policy.max_signal_id_bytes as usize
        {
            return Err(BoundaryRuntimeError::InvalidSignalId);
        }
        if self
            .reference
            .as_ref()
            .is_some_and(|reference| reference.len() > policy.max_reference_bytes as usize)
        {
            return Err(BoundaryRuntimeError::InvalidSignalReference);
        }
        if self.kind == BoundarySignalKind::Message
            && self
                .reference
                .as_ref()
                .is_none_or(|reference| reference.trim().is_empty())
        {
            return Err(BoundaryRuntimeError::InvalidSignalReference);
        }
        if self.authorization_context_ref.trim().is_empty()
            || self.authorization_context_ref.len() > policy.max_reference_bytes as usize
        {
            return Err(BoundaryRuntimeError::InvalidAuthorizationContextReference);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ClaimedTimer {
    pub subscription: ProjectedBoundarySubscription,
    pub generation: u64,
    pub attempts: u32,
    pub lease_version: u64,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ClaimedCorrelation {
    pub signal: BoundarySignal,
    pub subscription: Option<ProjectedBoundarySubscription>,
    pub attempts: u32,
    pub lease_version: u64,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct TimerDispatchCompletion {
    pub next_schedule: Option<TimerSchedule>,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum SignalEnqueueOutcome {
    Enqueued,
    Duplicate,
}

#[allow(clippy::missing_errors_doc)]
pub trait BoundaryRuntimeStorePort: Send + Sync {
    /// Returns the independent projection checkpoint for this adapter.
    fn projection_checkpoint(&self) -> Result<u64, BoundaryRuntimeError>;

    /// Atomically applies mutations and advances the projection checkpoint.
    fn apply_projection(
        &self,
        expected_checkpoint: u64,
        committed_checkpoint: u64,
        mutations: &[BoundaryProjectionMutation],
    ) -> Result<(), BoundaryRuntimeError>;

    /// Claims due timers with a bounded lease. Expired leases may be reclaimed.
    fn claim_due_timers(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
    ) -> Result<Vec<ClaimedTimer>, BoundaryRuntimeError>;

    fn complete_timer(
        &self,
        claim: &ClaimedTimer,
        completion: TimerDispatchCompletion,
    ) -> Result<(), BoundaryRuntimeError>;

    fn fail_timer(
        &self,
        claim: &ClaimedTimer,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError>;

    /// Persists a signal before any correlation or command dispatch.
    fn enqueue_signal(
        &self,
        signal: &BoundarySignal,
    ) -> Result<SignalEnqueueOutcome, BoundaryRuntimeError>;

    /// Claims pending signals and resolves at most one active subscription per signal.
    fn claim_correlations(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
        max_subscriptions_per_instance: usize,
    ) -> Result<Vec<ClaimedCorrelation>, BoundaryRuntimeError>;

    fn complete_correlation(&self, claim: &ClaimedCorrelation) -> Result<(), BoundaryRuntimeError>;

    fn fail_correlation(
        &self,
        claim: &ClaimedCorrelation,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError>;
}

impl<T: BoundaryRuntimeStorePort + ?Sized> BoundaryRuntimeStorePort for Arc<T> {
    fn projection_checkpoint(&self) -> Result<u64, BoundaryRuntimeError> {
        (**self).projection_checkpoint()
    }

    fn apply_projection(
        &self,
        expected_checkpoint: u64,
        committed_checkpoint: u64,
        mutations: &[BoundaryProjectionMutation],
    ) -> Result<(), BoundaryRuntimeError> {
        (**self).apply_projection(expected_checkpoint, committed_checkpoint, mutations)
    }

    fn claim_due_timers(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
    ) -> Result<Vec<ClaimedTimer>, BoundaryRuntimeError> {
        (**self).claim_due_timers(now_epoch_ms, lease_until_epoch_ms, worker_id, limit)
    }

    fn complete_timer(
        &self,
        claim: &ClaimedTimer,
        completion: TimerDispatchCompletion,
    ) -> Result<(), BoundaryRuntimeError> {
        (**self).complete_timer(claim, completion)
    }

    fn fail_timer(
        &self,
        claim: &ClaimedTimer,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError> {
        (**self).fail_timer(claim, retry_at_epoch_ms, dead_letter)
    }

    fn enqueue_signal(
        &self,
        signal: &BoundarySignal,
    ) -> Result<SignalEnqueueOutcome, BoundaryRuntimeError> {
        (**self).enqueue_signal(signal)
    }

    fn claim_correlations(
        &self,
        now_epoch_ms: u64,
        lease_until_epoch_ms: u64,
        worker_id: &str,
        limit: usize,
        max_subscriptions_per_instance: usize,
    ) -> Result<Vec<ClaimedCorrelation>, BoundaryRuntimeError> {
        (**self).claim_correlations(
            now_epoch_ms,
            lease_until_epoch_ms,
            worker_id,
            limit,
            max_subscriptions_per_instance,
        )
    }

    fn complete_correlation(&self, claim: &ClaimedCorrelation) -> Result<(), BoundaryRuntimeError> {
        (**self).complete_correlation(claim)
    }

    fn fail_correlation(
        &self,
        claim: &ClaimedCorrelation,
        retry_at_epoch_ms: u64,
        dead_letter: bool,
    ) -> Result<(), BoundaryRuntimeError> {
        (**self).fail_correlation(claim, retry_at_epoch_ms, dead_letter)
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum BoundaryDispatchSource {
    Timer,
    Message,
    Error,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct BoundaryDispatchRequest {
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub command_id: CommandId,
    pub idempotency_key: IdempotencyKey,
    pub correlation_id: CorrelationId,
    pub command: Command,
    pub source: BoundaryDispatchSource,
    pub occurred_at_epoch_ms: u64,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub authorization_context_ref: Option<String>,
}

#[allow(clippy::missing_errors_doc)]
pub trait BoundaryCommandDispatcherPort: Send + Sync {
    /// Dispatches through the normal authorized engine command path.
    ///
    /// Implementations must reuse the supplied deterministic command and idempotency keys.
    fn dispatch(&self, request: &BoundaryDispatchRequest) -> Result<(), BoundaryRuntimeError>;
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct BoundaryDispatchCredentials {
    pub actor_proof: Vec<u8>,
    pub workload_proof: Vec<u8>,
    pub encryption_key_scope: KeyScope,
}

#[allow(clippy::missing_errors_doc)]
pub trait WorkflowDefinitionProviderPort: Send + Sync {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, BoundaryRuntimeError>;
}

impl<T: WorkflowDefinitionProviderPort + ?Sized> WorkflowDefinitionProviderPort for Arc<T> {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, BoundaryRuntimeError> {
        (**self).resolve(tenant_id, workflow_type, workflow_version)
    }
}

#[allow(clippy::missing_errors_doc)]
pub trait BoundaryDispatchCredentialsPort: Send + Sync {
    /// Resolves command-bound system credentials or the original actor context for signals.
    fn resolve(
        &self,
        request: &BoundaryDispatchRequest,
    ) -> Result<BoundaryDispatchCredentials, BoundaryRuntimeError>;
}

pub struct EngineBoundaryCommandDispatcher<C, S, A, R, P> {
    engine: Engine<C, S, A>,
    definitions: R,
    credentials: P,
}

impl<C, S, A, R, P> EngineBoundaryCommandDispatcher<C, S, A, R, P> {
    pub const fn new(engine: Engine<C, S, A>, definitions: R, credentials: P) -> Self {
        Self {
            engine,
            definitions,
            credentials,
        }
    }

    pub const fn engine(&self) -> &Engine<C, S, A> {
        &self.engine
    }
}

impl<C, S, A, R, P> BoundaryCommandDispatcherPort for EngineBoundaryCommandDispatcher<C, S, A, R, P>
where
    C: ConfigurationProviderPort,
    S: WorkflowStorePort,
    A: AuthorizationProviderPort,
    R: WorkflowDefinitionProviderPort,
    P: BoundaryDispatchCredentialsPort,
{
    fn dispatch(&self, request: &BoundaryDispatchRequest) -> Result<(), BoundaryRuntimeError> {
        let definition = self.definitions.resolve(
            &request.tenant_id,
            &request.workflow_type,
            &request.workflow_version,
        )?;
        if definition.tenant_id != request.tenant_id
            || definition.workflow_type != request.workflow_type
            || definition.workflow_version != request.workflow_version
        {
            return Err(BoundaryRuntimeError::DefinitionScopeMismatch);
        }
        let credentials = self.credentials.resolve(request)?;
        if credentials.actor_proof.is_empty() || credentials.workload_proof.is_empty() {
            return Err(BoundaryRuntimeError::InvalidDispatchCredentials);
        }
        self.engine
            .handle(
                &definition,
                AuthorizedCommand {
                    tenant_id: request.tenant_id.clone(),
                    instance_id: request.instance_id.clone(),
                    command_id: request.command_id.clone(),
                    idempotency_key: request.idempotency_key.clone(),
                    correlation_id: request.correlation_id.clone(),
                    evaluated_at_epoch_ms: request.occurred_at_epoch_ms,
                    actor_proof: credentials.actor_proof,
                    actor_proof_kind: ActorProofKind::SignedInternalContext,
                    workload_proof: credentials.workload_proof,
                    encryption_key_scope: credentials.encryption_key_scope,
                    variables: BTreeMap::new(),
                    command: request.command.clone(),
                },
            )
            .map_err(|error| BoundaryRuntimeError::Dispatch(error.to_string()))?;
        Ok(())
    }
}

#[allow(clippy::missing_errors_doc)]
pub trait ClockPort: Send + Sync {
    fn now_epoch_ms(&self) -> Result<u64, BoundaryRuntimeError>;
}

#[derive(Debug, Clone, Copy, Default)]
pub struct SystemClock;

impl ClockPort for SystemClock {
    fn now_epoch_ms(&self) -> Result<u64, BoundaryRuntimeError> {
        let elapsed = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|error| BoundaryRuntimeError::Clock(error.to_string()))?;
        u64::try_from(elapsed.as_millis()).map_err(|_| BoundaryRuntimeError::ClockOverflow)
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct ProjectionOutcome {
    pub processed: usize,
    pub checkpoint: u64,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq, Default)]
pub struct DispatchOutcome {
    pub dispatched: usize,
    pub retried: usize,
    pub dead_lettered: usize,
}

pub struct BoundaryRuntime<E, S, D, C> {
    event_source: E,
    store: S,
    dispatcher: D,
    clock: C,
    policy: BoundaryRuntimePolicy,
}

#[allow(clippy::missing_errors_doc)]
impl<E, S, D, C> BoundaryRuntime<E, S, D, C>
where
    E: BoundaryEventSourcePort,
    S: BoundaryRuntimeStorePort,
    D: BoundaryCommandDispatcherPort,
    C: ClockPort,
{
    pub fn new(
        event_source: E,
        store: S,
        dispatcher: D,
        clock: C,
        policy: BoundaryRuntimePolicy,
    ) -> Self {
        Self {
            event_source,
            store,
            dispatcher,
            clock,
            policy,
        }
    }

    pub const fn store(&self) -> &S {
        &self.store
    }

    pub const fn dispatcher(&self) -> &D {
        &self.dispatcher
    }

    pub const fn clock(&self) -> &C {
        &self.clock
    }

    /// Projects at most one configured committed-event batch.
    pub fn project_once(&self) -> Result<ProjectionOutcome, BoundaryRuntimeError> {
        let checkpoint = self.store.projection_checkpoint()?;
        let limit = self.policy.projection_batch_size as usize;
        let records = self.event_source.read_after(checkpoint, limit)?;
        validate_projection_records(&records, checkpoint, limit)?;
        let mut mutations = Vec::new();
        for record in &records {
            if let Some(mutation) = projection_mutation(record, &self.policy)? {
                mutations.push(mutation);
            }
        }
        let committed = records.last().map_or(checkpoint, |record| record.cursor);
        if committed != checkpoint {
            self.store
                .apply_projection(checkpoint, committed, &mutations)?;
        }
        Ok(ProjectionOutcome {
            processed: records.len(),
            checkpoint: committed,
        })
    }

    /// Persists one external message/error signal before returning acknowledgement.
    pub fn enqueue_signal(
        &self,
        signal: &BoundarySignal,
    ) -> Result<SignalEnqueueOutcome, BoundaryRuntimeError> {
        signal.validate(&self.policy)?;
        self.store.enqueue_signal(signal)
    }

    /// Claims and dispatches one bounded timer batch.
    pub fn dispatch_due_timers_once(&self) -> Result<DispatchOutcome, BoundaryRuntimeError> {
        let now = self.clock.now_epoch_ms()?;
        let lease_until = now
            .checked_add(self.policy.lease_duration_ms)
            .ok_or(BoundaryRuntimeError::ClockOverflow)?;
        let claims = self.store.claim_due_timers(
            now,
            lease_until,
            &self.policy.worker_id,
            self.policy.dispatch_batch_size as usize,
        )?;
        if claims.len() > self.policy.dispatch_batch_size as usize {
            return Err(BoundaryRuntimeError::AdapterBatchLimitExceeded);
        }
        let mut outcome = DispatchOutcome::default();
        for claim in claims {
            let request = timer_dispatch_request(&claim, now)?;
            match self.dispatcher.dispatch(&request) {
                Ok(()) => {
                    let completion = timer_completion(&claim)?;
                    self.store.complete_timer(&claim, completion)?;
                    outcome.dispatched += 1;
                }
                Err(_) => self.record_timer_failure(&claim, now, &mut outcome)?,
            }
        }
        Ok(outcome)
    }

    /// Claims, correlates, and dispatches one bounded message/error signal batch.
    pub fn dispatch_correlations_once(&self) -> Result<DispatchOutcome, BoundaryRuntimeError> {
        let now = self.clock.now_epoch_ms()?;
        let lease_until = now
            .checked_add(self.policy.lease_duration_ms)
            .ok_or(BoundaryRuntimeError::ClockOverflow)?;
        let claims = self.store.claim_correlations(
            now,
            lease_until,
            &self.policy.worker_id,
            self.policy.dispatch_batch_size as usize,
            self.policy.max_subscriptions_per_instance as usize,
        )?;
        if claims.len() > self.policy.dispatch_batch_size as usize {
            return Err(BoundaryRuntimeError::AdapterBatchLimitExceeded);
        }
        let mut outcome = DispatchOutcome::default();
        for claim in claims {
            let dispatched = claim
                .subscription
                .as_ref()
                .map(|subscription| correlation_dispatch_request(&claim.signal, subscription))
                .transpose()?
                .is_some_and(|request| self.dispatcher.dispatch(&request).is_ok());
            if dispatched {
                self.store.complete_correlation(&claim)?;
                outcome.dispatched += 1;
            } else {
                self.record_correlation_failure(&claim, now, &mut outcome)?;
            }
        }
        Ok(outcome)
    }

    fn record_timer_failure(
        &self,
        claim: &ClaimedTimer,
        now: u64,
        outcome: &mut DispatchOutcome,
    ) -> Result<(), BoundaryRuntimeError> {
        let dead_letter = claim.attempts >= self.policy.max_dispatch_attempts;
        let retry_at = now
            .checked_add(self.policy.retry_delay_ms)
            .ok_or(BoundaryRuntimeError::ClockOverflow)?;
        self.store.fail_timer(claim, retry_at, dead_letter)?;
        if dead_letter {
            outcome.dead_lettered += 1;
        } else {
            outcome.retried += 1;
        }
        Ok(())
    }

    fn record_correlation_failure(
        &self,
        claim: &ClaimedCorrelation,
        now: u64,
        outcome: &mut DispatchOutcome,
    ) -> Result<(), BoundaryRuntimeError> {
        let dead_letter = claim.attempts >= self.policy.max_dispatch_attempts;
        let retry_at = now
            .checked_add(self.policy.retry_delay_ms)
            .ok_or(BoundaryRuntimeError::ClockOverflow)?;
        self.store.fail_correlation(claim, retry_at, dead_letter)?;
        if dead_letter {
            outcome.dead_lettered += 1;
        } else {
            outcome.retried += 1;
        }
        Ok(())
    }
}

fn validate_projection_records(
    records: &[BoundaryProjectionRecord],
    checkpoint: u64,
    limit: usize,
) -> Result<(), BoundaryRuntimeError> {
    if records.len() > limit {
        return Err(BoundaryRuntimeError::AdapterBatchLimitExceeded);
    }
    let mut expected = checkpoint;
    for record in records {
        expected = expected
            .checked_add(1)
            .ok_or(BoundaryRuntimeError::ProjectionCursorOverflow)?;
        if record.cursor != expected {
            return Err(BoundaryRuntimeError::NonContiguousProjection);
        }
    }
    Ok(())
}

fn projection_mutation(
    record: &BoundaryProjectionRecord,
    policy: &BoundaryRuntimePolicy,
) -> Result<Option<BoundaryProjectionMutation>, BoundaryRuntimeError> {
    let metadata = &record.envelope.metadata;
    let mutation = match &record.envelope.event {
        DomainEvent::BoundaryEventArmed {
            boundary_event_id,
            attached_node_id,
            target_node_id,
            cancel_activity,
            trigger,
            occurred_at_epoch_ms,
        } => BoundaryProjectionMutation::Upsert(ProjectedBoundarySubscription {
            key: BoundarySubscriptionKey {
                tenant_id: metadata.tenant_id.clone(),
                instance_id: metadata.instance_id.clone(),
                boundary_event_id: boundary_event_id.clone(),
            },
            attached_node_id: attached_node_id.clone(),
            target_node_id: target_node_id.clone(),
            cancel_activity: *cancel_activity,
            trigger: trigger.clone(),
            armed_at_epoch_ms: *occurred_at_epoch_ms,
            armed_event_id: metadata.event_id.clone(),
            timer_schedule: timer_schedule(trigger, *occurred_at_epoch_ms, policy)?,
            workflow_type: metadata.workflow_type.clone(),
            workflow_version: metadata.workflow_version.clone(),
        }),
        DomainEvent::BoundaryEventsDisarmed {
            boundary_event_ids, ..
        } if boundary_event_ids.len() == 1 => {
            BoundaryProjectionMutation::DisarmBoundary(BoundarySubscriptionKey {
                tenant_id: metadata.tenant_id.clone(),
                instance_id: metadata.instance_id.clone(),
                boundary_event_id: boundary_event_ids[0].clone(),
            })
        }
        DomainEvent::BoundaryEventsDisarmed {
            attached_node_id, ..
        }
        | DomainEvent::ServiceTaskCompleted {
            node_id: attached_node_id,
            ..
        }
        | DomainEvent::UserTaskCompleted {
            node_id: attached_node_id,
            ..
        }
        | DomainEvent::ScriptTaskCompleted {
            node_id: attached_node_id,
            ..
        }
        | DomainEvent::MultiInstanceCompleted {
            node_id: attached_node_id,
            ..
        }
        | DomainEvent::BoundaryEventTriggered {
            attached_node_id,
            cancel_activity: true,
            ..
        } => BoundaryProjectionMutation::DisarmAttached {
            tenant_id: metadata.tenant_id.clone(),
            instance_id: metadata.instance_id.clone(),
            attached_node_id: attached_node_id.clone(),
        },
        DomainEvent::WorkflowCompleted { .. }
        | DomainEvent::WorkflowTerminatedForCompliance { .. } => {
            BoundaryProjectionMutation::RemoveInstance {
                tenant_id: metadata.tenant_id.clone(),
                instance_id: metadata.instance_id.clone(),
            }
        }
        _ => return Ok(None),
    };
    Ok(Some(mutation))
}

fn timer_schedule(
    trigger: &BoundaryTrigger,
    armed_at_epoch_ms: u64,
    policy: &BoundaryRuntimePolicy,
) -> Result<Option<TimerSchedule>, BoundaryRuntimeError> {
    let BoundaryTrigger::Timer { kind, expression } = trigger else {
        return Ok(None);
    };
    if expression.len() > policy.max_expression_bytes as usize {
        return Err(BoundaryRuntimeError::TimerExpressionTooLarge);
    }
    let schedule = match kind {
        BoundaryTimerKind::Date => TimerSchedule {
            due_at_epoch_ms: parse_date(expression)?.max(armed_at_epoch_ms),
            interval_ms: None,
            remaining_firings: Some(1),
        },
        BoundaryTimerKind::Duration => {
            let duration = parse_duration_ms(expression)?;
            TimerSchedule {
                due_at_epoch_ms: armed_at_epoch_ms
                    .checked_add(duration)
                    .ok_or(BoundaryRuntimeError::TimerOverflow)?,
                interval_ms: None,
                remaining_firings: Some(1),
            }
        }
        BoundaryTimerKind::Cycle => parse_cycle(expression, armed_at_epoch_ms)?,
    };
    let horizon = schedule.due_at_epoch_ms.saturating_sub(armed_at_epoch_ms);
    if horizon > policy.max_timer_horizon_ms
        || schedule
            .interval_ms
            .is_some_and(|interval| interval > policy.max_timer_horizon_ms)
    {
        return Err(BoundaryRuntimeError::TimerHorizonExceeded);
    }
    Ok(Some(schedule))
}

fn parse_date(expression: &str) -> Result<u64, BoundaryRuntimeError> {
    let parsed = OffsetDateTime::parse(expression.trim(), &Rfc3339)
        .map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)?;
    let millis = parsed.unix_timestamp_nanos().div_euclid(1_000_000);
    u64::try_from(millis).map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)
}

fn parse_cycle(
    expression: &str,
    armed_at_epoch_ms: u64,
) -> Result<TimerSchedule, BoundaryRuntimeError> {
    let parts = expression.trim().split('/').collect::<Vec<_>>();
    if !(parts.len() == 2 || parts.len() == 3) || !parts[0].starts_with('R') {
        return Err(BoundaryRuntimeError::InvalidTimerExpression);
    }
    let remaining_firings = if parts[0] == "R" {
        None
    } else {
        let count = parts[0][1..]
            .parse::<u32>()
            .map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)?;
        if count == 0 {
            return Err(BoundaryRuntimeError::InvalidTimerExpression);
        }
        Some(count)
    };
    let interval = parse_duration_ms(parts[parts.len() - 1])?;
    let due_at_epoch_ms = if parts.len() == 3 {
        parse_date(parts[1])?.max(armed_at_epoch_ms)
    } else {
        armed_at_epoch_ms
            .checked_add(interval)
            .ok_or(BoundaryRuntimeError::TimerOverflow)?
    };
    Ok(TimerSchedule {
        due_at_epoch_ms,
        interval_ms: Some(interval),
        remaining_firings,
    })
}

fn parse_duration_ms(expression: &str) -> Result<u64, BoundaryRuntimeError> {
    let value = expression.trim();
    let Some(mut rest) = value.strip_prefix('P') else {
        return Err(BoundaryRuntimeError::InvalidTimerExpression);
    };
    if rest.is_empty() || rest.contains(['Y']) {
        return Err(BoundaryRuntimeError::InvalidTimerExpression);
    }
    let mut in_time = false;
    let mut number_start = 0;
    let mut total_ms = 0_u64;
    for (index, character) in rest.char_indices() {
        if character == 'T' {
            if index != number_start {
                return Err(BoundaryRuntimeError::InvalidTimerExpression);
            }
            in_time = true;
            number_start = index + 1;
            continue;
        }
        if character.is_ascii_digit() || character == '.' {
            continue;
        }
        let number = &rest[number_start..index];
        if number.is_empty() {
            return Err(BoundaryRuntimeError::InvalidTimerExpression);
        }
        let unit_ms = match (character, in_time) {
            ('W', false) => 7 * 24 * 60 * 60 * 1_000,
            ('D', false) => 24 * 60 * 60 * 1_000,
            ('H', true) => 60 * 60 * 1_000,
            ('M', true) => 60 * 1_000,
            ('S', true) => 1_000,
            _ => return Err(BoundaryRuntimeError::InvalidTimerExpression),
        };
        let component = decimal_component_ms(number, unit_ms)?;
        total_ms = total_ms
            .checked_add(component)
            .ok_or(BoundaryRuntimeError::TimerOverflow)?;
        number_start = index + character.len_utf8();
    }
    rest = &rest[number_start..];
    if !rest.is_empty() || total_ms == 0 {
        return Err(BoundaryRuntimeError::InvalidTimerExpression);
    }
    Ok(total_ms)
}

fn decimal_component_ms(value: &str, unit_ms: u64) -> Result<u64, BoundaryRuntimeError> {
    let Some((whole, fraction)) = value.split_once('.') else {
        return value
            .parse::<u64>()
            .map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)?
            .checked_mul(unit_ms)
            .ok_or(BoundaryRuntimeError::TimerOverflow);
    };
    if whole.is_empty() || fraction.is_empty() || fraction.len() > 9 {
        return Err(BoundaryRuntimeError::InvalidTimerExpression);
    }
    let whole_ms = whole
        .parse::<u64>()
        .map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)?
        .checked_mul(unit_ms)
        .ok_or(BoundaryRuntimeError::TimerOverflow)?;
    let denominator = 10_u64
        .checked_pow(
            u32::try_from(fraction.len())
                .map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)?,
        )
        .ok_or(BoundaryRuntimeError::TimerOverflow)?;
    let fraction_ms = fraction
        .parse::<u64>()
        .map_err(|_| BoundaryRuntimeError::InvalidTimerExpression)?
        .checked_mul(unit_ms)
        .ok_or(BoundaryRuntimeError::TimerOverflow)?
        / denominator;
    whole_ms
        .checked_add(fraction_ms)
        .ok_or(BoundaryRuntimeError::TimerOverflow)
}

fn timer_dispatch_request(
    claim: &ClaimedTimer,
    occurred_at_epoch_ms: u64,
) -> Result<BoundaryDispatchRequest, BoundaryRuntimeError> {
    dispatch_request(
        &claim.subscription,
        format!(
            "{}:timer:{}",
            claim.subscription.armed_event_id, claim.generation
        ),
        BoundaryDispatchSource::Timer,
        occurred_at_epoch_ms,
        None,
    )
}

fn correlation_dispatch_request(
    signal: &BoundarySignal,
    subscription: &ProjectedBoundarySubscription,
) -> Result<BoundaryDispatchRequest, BoundaryRuntimeError> {
    let source = match signal.kind {
        BoundarySignalKind::Message => BoundaryDispatchSource::Message,
        BoundarySignalKind::Error => BoundaryDispatchSource::Error,
    };
    dispatch_request(
        subscription,
        format!(
            "{}:boundary:{}",
            signal.signal_id, subscription.key.boundary_event_id
        ),
        source,
        signal.occurred_at_epoch_ms,
        Some(signal.authorization_context_ref.clone()),
    )
}

fn dispatch_request(
    subscription: &ProjectedBoundarySubscription,
    identity: String,
    source: BoundaryDispatchSource,
    occurred_at_epoch_ms: u64,
    authorization_context_ref: Option<String>,
) -> Result<BoundaryDispatchRequest, BoundaryRuntimeError> {
    Ok(BoundaryDispatchRequest {
        tenant_id: subscription.key.tenant_id.clone(),
        instance_id: subscription.key.instance_id.clone(),
        command_id: CommandId::new(identity.clone())
            .map_err(|_| BoundaryRuntimeError::InvalidDispatchIdentity)?,
        idempotency_key: IdempotencyKey::new(identity.clone())
            .map_err(|_| BoundaryRuntimeError::InvalidDispatchIdentity)?,
        correlation_id: CorrelationId::new(identity)
            .map_err(|_| BoundaryRuntimeError::InvalidDispatchIdentity)?,
        command: Command::TriggerBoundaryEvent {
            boundary_event_id: subscription.key.boundary_event_id.clone(),
            occurred_at_epoch_ms,
        },
        source,
        occurred_at_epoch_ms,
        workflow_type: subscription.workflow_type.clone(),
        workflow_version: subscription.workflow_version.clone(),
        authorization_context_ref,
    })
}

fn timer_completion(claim: &ClaimedTimer) -> Result<TimerDispatchCompletion, BoundaryRuntimeError> {
    let schedule = claim
        .subscription
        .timer_schedule
        .ok_or(BoundaryRuntimeError::ClaimWithoutTimer)?;
    let next_schedule = if claim.subscription.cancel_activity {
        None
    } else if let Some(interval) = schedule.interval_ms {
        let remaining_firings = match schedule.remaining_firings {
            Some(1) => {
                return Ok(TimerDispatchCompletion {
                    next_schedule: None,
                });
            }
            Some(remaining) => Some(remaining - 1),
            None => None,
        };
        Some(TimerSchedule {
            due_at_epoch_ms: schedule
                .due_at_epoch_ms
                .checked_add(interval)
                .ok_or(BoundaryRuntimeError::TimerOverflow)?,
            interval_ms: Some(interval),
            remaining_firings,
        })
    } else {
        None
    };
    Ok(TimerDispatchCompletion { next_schedule })
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum BoundaryRuntimeError {
    #[error("boundary event source failed: {0}")]
    Outbox(OutboxError),
    #[error("committed boundary event is corrupt: {0}")]
    CorruptEvent(String),
    #[error("committed event payload does not match its outbox scope")]
    EventScopeMismatch,
    #[error("boundary runtime adapter returned more than the configured batch limit")]
    AdapterBatchLimitExceeded,
    #[error("boundary projection records are not contiguous")]
    NonContiguousProjection,
    #[error("boundary projection cursor overflow")]
    ProjectionCursorOverflow,
    #[error("boundary runtime projection checkpoint conflict")]
    ProjectionCheckpointConflict,
    #[error("boundary timer expression exceeds its configured byte limit")]
    TimerExpressionTooLarge,
    #[error("boundary timer expression is unsupported or malformed")]
    InvalidTimerExpression,
    #[error("boundary timer exceeds the configured scheduling horizon")]
    TimerHorizonExceeded,
    #[error("boundary timer arithmetic overflow")]
    TimerOverflow,
    #[error("claimed boundary record is not a timer")]
    ClaimWithoutTimer,
    #[error("boundary signal id is empty or oversized")]
    InvalidSignalId,
    #[error("boundary signal reference is missing or oversized")]
    InvalidSignalReference,
    #[error("boundary signal authorization context reference is missing or oversized")]
    InvalidAuthorizationContextReference,
    #[error("boundary signal id was reused with different content")]
    SignalConflict,
    #[error("boundary signal matches more than one active subscription")]
    AmbiguousCorrelation,
    #[error("boundary correlation scan exceeds the configured per-instance subscription limit")]
    CorrelationScanLimitExceeded,
    #[error("boundary runtime lease compare-and-swap failed")]
    LeaseConflict,
    #[error("boundary runtime clock failed: {0}")]
    Clock(String),
    #[error("boundary runtime clock arithmetic overflow")]
    ClockOverflow,
    #[error("boundary dispatch identity could not be represented")]
    InvalidDispatchIdentity,
    #[error("resolved workflow definition does not match the boundary subscription scope")]
    DefinitionScopeMismatch,
    #[error("workflow definition is unavailable: {0}")]
    DefinitionUnavailable(String),
    #[error("boundary dispatch credentials are empty")]
    InvalidDispatchCredentials,
    #[error("boundary command dispatch failed: {0}")]
    Dispatch(String),
    #[error("boundary runtime durable store failed: {0}")]
    Store(String),
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicU32, AtomicU64, Ordering};

    use bpmp_domain_core::{
        ActorId, ConfigVersion, InstanceId, KeyScope, PolicyVersion, WorkflowType, WorkflowVersion,
    };

    use super::*;
    use crate::memory::InMemoryBoundaryRuntimeStore;
    use crate::{EVENT_SCHEMA_VERSION, EventMetadata};

    struct Source(Vec<BoundaryProjectionRecord>);

    impl BoundaryEventSourcePort for Source {
        fn read_after(
            &self,
            cursor: u64,
            limit: usize,
        ) -> Result<Vec<BoundaryProjectionRecord>, BoundaryRuntimeError> {
            Ok(self
                .0
                .iter()
                .filter(|record| record.cursor > cursor)
                .take(limit)
                .cloned()
                .collect())
        }
    }

    #[derive(Default)]
    struct Dispatcher {
        requests: Mutex<Vec<BoundaryDispatchRequest>>,
        failures_remaining: AtomicU32,
    }

    impl BoundaryCommandDispatcherPort for Dispatcher {
        fn dispatch(&self, request: &BoundaryDispatchRequest) -> Result<(), BoundaryRuntimeError> {
            self.requests.lock().unwrap().push(request.clone());
            if self
                .failures_remaining
                .fetch_update(Ordering::AcqRel, Ordering::Acquire, |remaining| {
                    remaining.checked_sub(1)
                })
                .is_ok()
            {
                Err(BoundaryRuntimeError::Dispatch("injected".into()))
            } else {
                Ok(())
            }
        }
    }

    struct FixedClock(AtomicU64);

    impl FixedClock {
        const fn new(now_epoch_ms: u64) -> Self {
            Self(AtomicU64::new(now_epoch_ms))
        }

        fn set(&self, now_epoch_ms: u64) {
            self.0.store(now_epoch_ms, Ordering::Release);
        }
    }

    impl ClockPort for FixedClock {
        fn now_epoch_ms(&self) -> Result<u64, BoundaryRuntimeError> {
            Ok(self.0.load(Ordering::Acquire))
        }
    }

    fn policy() -> BoundaryRuntimePolicy {
        BoundaryRuntimePolicy {
            projection_batch_size: 32,
            dispatch_batch_size: 8,
            max_dispatch_attempts: 2,
            retry_delay_ms: 1_000,
            lease_duration_ms: 100,
            max_timer_horizon_ms: 365 * 24 * 60 * 60 * 1_000,
            max_expression_bytes: 128,
            worker_id: "boundary-worker-1".into(),
            max_signal_id_bytes: 128,
            max_reference_bytes: 128,
            max_subscriptions_per_instance: 64,
        }
    }

    fn metadata(cursor: u64) -> EventMetadata {
        EventMetadata {
            event_id: format!("event-{cursor}"),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            sequence: cursor,
            schema_version: EVENT_SCHEMA_VERSION,
            correlation_id: CorrelationId::new("correlation-1").unwrap(),
            causation_command_id: CommandId::new("command-1").unwrap(),
            occurred_at_epoch_ms: 100,
            config_version: ConfigVersion::new("config-1").unwrap(),
            policy_version: PolicyVersion::new("policy-1").unwrap(),
            actor_id: ActorId::new("actor-1").unwrap(),
            encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            workflow_type: WorkflowType::new("boundary-workflow").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
        }
    }

    fn armed_record(
        cursor: u64,
        boundary_id: &str,
        trigger: BoundaryTrigger,
    ) -> BoundaryProjectionRecord {
        BoundaryProjectionRecord {
            cursor,
            envelope: EventEnvelope {
                metadata: metadata(cursor),
                event: DomainEvent::BoundaryEventArmed {
                    boundary_event_id: NodeId::new(boundary_id).unwrap(),
                    attached_node_id: NodeId::new("work").unwrap(),
                    target_node_id: NodeId::new("recovery").unwrap(),
                    cancel_activity: false,
                    trigger,
                    occurred_at_epoch_ms: 100,
                },
            },
        }
    }

    #[test]
    fn duration_timer_projects_and_dispatches_only_when_due() {
        let runtime = BoundaryRuntime::new(
            Source(vec![armed_record(
                1,
                "timeout",
                BoundaryTrigger::Timer {
                    kind: BoundaryTimerKind::Duration,
                    expression: "PT1S".into(),
                },
            )]),
            InMemoryBoundaryRuntimeStore::default(),
            Dispatcher::default(),
            FixedClock::new(1_099),
            policy(),
        );
        assert_eq!(
            runtime.project_once().unwrap(),
            ProjectionOutcome {
                processed: 1,
                checkpoint: 1,
            }
        );
        assert_eq!(runtime.dispatch_due_timers_once().unwrap().dispatched, 0);
        runtime.clock().set(1_100);
        assert_eq!(runtime.dispatch_due_timers_once().unwrap().dispatched, 1);
        assert_eq!(runtime.store().pending_timer_count().unwrap(), 0);
        let requests = runtime.dispatcher().requests.lock().unwrap();
        assert_eq!(requests.len(), 1);
        assert!(matches!(
            requests[0].command,
            Command::TriggerBoundaryEvent { .. }
        ));
        assert_eq!(requests[0].source, BoundaryDispatchSource::Timer);
        assert_eq!(requests[0].authorization_context_ref, None);
    }

    #[test]
    fn finite_non_interrupting_cycle_rearms_with_stable_generation_identity() {
        let runtime = BoundaryRuntime::new(
            Source(vec![armed_record(
                1,
                "reminder",
                BoundaryTrigger::Timer {
                    kind: BoundaryTimerKind::Cycle,
                    expression: "R2/PT1S".into(),
                },
            )]),
            InMemoryBoundaryRuntimeStore::default(),
            Dispatcher::default(),
            FixedClock::new(1_100),
            policy(),
        );
        runtime.project_once().unwrap();
        assert_eq!(runtime.dispatch_due_timers_once().unwrap().dispatched, 1);
        runtime.clock().set(2_100);
        assert_eq!(runtime.dispatch_due_timers_once().unwrap().dispatched, 1);
        assert_eq!(runtime.store().pending_timer_count().unwrap(), 0);
        let requests = runtime.dispatcher().requests.lock().unwrap();
        assert_ne!(requests[0].command_id, requests[1].command_id);
        assert!(requests[0].command_id.as_str().ends_with("timer:0"));
        assert!(requests[1].command_id.as_str().ends_with("timer:1"));
    }

    #[test]
    fn message_signal_is_persisted_deduplicated_and_exactly_correlated() {
        let runtime = BoundaryRuntime::new(
            Source(vec![armed_record(
                1,
                "message-boundary",
                BoundaryTrigger::Message {
                    message_ref: "order.cancelled".into(),
                },
            )]),
            InMemoryBoundaryRuntimeStore::default(),
            Dispatcher::default(),
            FixedClock::new(500),
            policy(),
        );
        runtime.project_once().unwrap();
        let signal = BoundarySignal {
            signal_id: "message-1".into(),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            kind: BoundarySignalKind::Message,
            reference: Some("order.cancelled".into()),
            occurred_at_epoch_ms: 200,
            authorization_context_ref: "auth-context/message-1".into(),
        };
        assert_eq!(
            runtime.enqueue_signal(&signal).unwrap(),
            SignalEnqueueOutcome::Enqueued
        );
        assert_eq!(
            runtime.enqueue_signal(&signal).unwrap(),
            SignalEnqueueOutcome::Duplicate
        );
        assert_eq!(runtime.dispatch_correlations_once().unwrap().dispatched, 1);
        let requests = runtime.dispatcher().requests.lock().unwrap();
        assert_eq!(requests[0].source, BoundaryDispatchSource::Message);
        assert_eq!(requests[0].occurred_at_epoch_ms, 200);
        assert_eq!(
            requests[0].authorization_context_ref.as_deref(),
            Some("auth-context/message-1")
        );
    }

    #[test]
    fn unmatched_error_retries_then_dead_letters_at_configured_limit() {
        let runtime = BoundaryRuntime::new(
            Source(Vec::new()),
            InMemoryBoundaryRuntimeStore::default(),
            Dispatcher::default(),
            FixedClock::new(500),
            policy(),
        );
        let signal = BoundarySignal {
            signal_id: "error-1".into(),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            kind: BoundarySignalKind::Error,
            reference: Some("payment.failed".into()),
            occurred_at_epoch_ms: 200,
            authorization_context_ref: "auth-context/error-1".into(),
        };
        runtime.enqueue_signal(&signal).unwrap();
        assert_eq!(runtime.dispatch_correlations_once().unwrap().retried, 1);
        runtime.clock().set(1_500);
        assert_eq!(
            runtime.dispatch_correlations_once().unwrap().dead_lettered,
            1
        );
        assert_eq!(runtime.store().dead_letter_count().unwrap(), 1);
    }

    #[test]
    fn malformed_timer_does_not_advance_projection_checkpoint() {
        let runtime = BoundaryRuntime::new(
            Source(vec![armed_record(
                1,
                "timeout",
                BoundaryTrigger::Timer {
                    kind: BoundaryTimerKind::Duration,
                    expression: "P1M".into(),
                },
            )]),
            InMemoryBoundaryRuntimeStore::default(),
            Dispatcher::default(),
            FixedClock::new(100),
            policy(),
        );
        assert_eq!(
            runtime.project_once(),
            Err(BoundaryRuntimeError::InvalidTimerExpression)
        );
        assert_eq!(runtime.store().projection_checkpoint().unwrap(), 0);
    }
}
