use std::sync::{Arc, RwLock};

use bpmp_domain_core::{
    ConfigVersion, CorrelationId, DomainEvent, NodeId, PolicyVersion, TenantId, WorkflowType,
    WorkflowVersion,
};
use thiserror::Error;

use crate::{EventCodec, OutboxError, OutboxStorePort, RemoteTaskIngressPort, RetryDelayPort};

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum LocalTaskKind {
    Service,
    Script,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum LocalTaskExecutionOutcome {
    Completed,
    NotHandled,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct LocalTaskActivation {
    pub cursor: u64,
    pub event_id: String,
    pub event_sequence: u64,
    pub tenant_id: TenantId,
    pub instance_id: String,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub node_id: NodeId,
    pub kind: LocalTaskKind,
    pub task_type: String,
    pub implementation_ref: String,
    pub implementation_version: String,
    pub occurred_at_epoch_ms: u64,
    pub correlation_id: CorrelationId,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
}

#[allow(clippy::missing_errors_doc)]
pub trait LocalTaskRuntimeStorePort: Send + Sync {
    fn local_task_checkpoint(&self) -> Result<u64, LocalTaskRuntimeError>;
    fn checkpoint_local_task(
        &self,
        expected: u64,
        committed: u64,
    ) -> Result<(), LocalTaskRuntimeError>;
}

impl<T: LocalTaskRuntimeStorePort + ?Sized> LocalTaskRuntimeStorePort for Arc<T> {
    fn local_task_checkpoint(&self) -> Result<u64, LocalTaskRuntimeError> {
        (**self).local_task_checkpoint()
    }

    fn checkpoint_local_task(
        &self,
        expected: u64,
        committed: u64,
    ) -> Result<(), LocalTaskRuntimeError> {
        (**self).checkpoint_local_task(expected, committed)
    }
}

#[allow(clippy::missing_errors_doc)]
pub trait LocalTaskExecutorPort: Send + Sync {
    /// Executes one already-committed activation with deployment-configured bounds.
    ///
    /// Service tasks without a local binding return [`LocalTaskExecutionOutcome::NotHandled`]
    /// so a remote worker adapter can own them without the local checkpoint stalling.
    fn execute(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<LocalTaskExecutionOutcome, LocalTaskRuntimeError>;
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct LocalTaskRetryPolicy {
    pub max_attempts: u32,
    pub initial_backoff_ms: u64,
    pub max_backoff_ms: u64,
    pub multiplier_millis: u32,
}

impl LocalTaskRetryPolicy {
    /// Validates a deployment-supplied local-task retry policy.
    ///
    /// # Errors
    ///
    /// Returns an error for zero attempts/delay/multiplier or an inverted range.
    pub const fn validate(self) -> Result<Self, LocalTaskRuntimeError> {
        if self.max_attempts == 0
            || self.initial_backoff_ms == 0
            || self.max_backoff_ms < self.initial_backoff_ms
            || self.multiplier_millis == 0
        {
            Err(LocalTaskRuntimeError::InvalidConfiguration)
        } else {
            Ok(self)
        }
    }
}

pub struct RetryingLocalTaskExecutor<E, D> {
    inner: E,
    delay: D,
    policy: LocalTaskRetryPolicyHandle,
}

#[derive(Clone)]
pub struct LocalTaskRetryPolicyHandle {
    value: Arc<RwLock<LocalTaskRetryPolicy>>,
}

impl LocalTaskRetryPolicyHandle {
    /// Creates a shared validated policy handle.
    ///
    /// # Errors
    ///
    /// Returns an error when the retry policy is invalid.
    pub fn new(policy: LocalTaskRetryPolicy) -> Result<Self, LocalTaskRuntimeError> {
        Ok(Self {
            value: Arc::new(RwLock::new(policy.validate()?)),
        })
    }

    /// Replaces the policy used by the next local-task batch.
    ///
    /// # Errors
    ///
    /// Returns an error when the policy is invalid or the lock is poisoned.
    pub fn replace(&self, policy: LocalTaskRetryPolicy) -> Result<(), LocalTaskRuntimeError> {
        let policy = policy.validate()?;
        *self
            .value
            .write()
            .map_err(|_| LocalTaskRuntimeError::PolicyUnavailable)? = policy;
        Ok(())
    }

    fn snapshot(&self) -> Result<LocalTaskRetryPolicy, LocalTaskRuntimeError> {
        self.value
            .read()
            .map(|policy| *policy)
            .map_err(|_| LocalTaskRuntimeError::PolicyUnavailable)
    }
}

impl<E, D> RetryingLocalTaskExecutor<E, D> {
    /// Creates a bounded retry decorator.
    ///
    /// # Errors
    ///
    /// Returns an error when the supplied policy is invalid.
    pub fn new(
        inner: E,
        delay: D,
        policy: LocalTaskRetryPolicy,
    ) -> Result<Self, LocalTaskRuntimeError> {
        Self::new_with_handle(inner, delay, LocalTaskRetryPolicyHandle::new(policy)?)
    }

    /// Creates a retrying executor from an existing policy handle.
    ///
    /// # Errors
    ///
    /// Returns an error when the policy snapshot cannot be read.
    pub fn new_with_handle(
        inner: E,
        delay: D,
        policy: LocalTaskRetryPolicyHandle,
    ) -> Result<Self, LocalTaskRuntimeError> {
        policy.snapshot()?;
        Ok(Self {
            inner,
            delay,
            policy,
        })
    }
}

impl<E, D> LocalTaskExecutorPort for RetryingLocalTaskExecutor<E, D>
where
    E: LocalTaskExecutorPort,
    D: RetryDelayPort,
{
    fn execute(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<LocalTaskExecutionOutcome, LocalTaskRuntimeError> {
        let policy = self.policy.snapshot()?;
        let mut backoff_ms = policy.initial_backoff_ms;
        for attempt in 1..=policy.max_attempts {
            match self.inner.execute(activation) {
                Ok(outcome) => return Ok(outcome),
                Err(error) if attempt == policy.max_attempts => return Err(error),
                Err(_) => {
                    self.delay.wait(backoff_ms);
                    backoff_ms = backoff_ms
                        .saturating_mul(u64::from(policy.multiplier_millis))
                        .saturating_div(1_000)
                        .min(policy.max_backoff_ms);
                }
            }
        }
        Err(LocalTaskRuntimeError::InvalidConfiguration)
    }
}

#[allow(clippy::missing_errors_doc)]
pub trait LocalTaskCompletionDispatcherPort: Send + Sync {
    /// Dispatches completion through the authoritative command path.
    fn dispatch_completion(
        &self,
        activation: &LocalTaskActivation,
    ) -> Result<(), LocalTaskRuntimeError>;
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct LocalTaskRunOutcome {
    pub scanned: usize,
    pub executed: usize,
    pub checkpoint: u64,
}

pub struct LocalTaskRuntime<S, R, E, F, D> {
    outbox: S,
    state: R,
    executor: E,
    remote_fallback: F,
    dispatcher: D,
    batch_size: usize,
}

impl<S, R, E, F, D> LocalTaskRuntime<S, R, E, F, D>
where
    S: OutboxStorePort,
    R: LocalTaskRuntimeStorePort,
    E: LocalTaskExecutorPort,
    F: RemoteTaskIngressPort,
    D: LocalTaskCompletionDispatcherPort,
{
    /// Creates a bounded local-task worker.
    ///
    /// # Errors
    ///
    /// Returns [`LocalTaskRuntimeError::InvalidConfiguration`] for a zero batch size.
    pub fn new(
        outbox: S,
        state: R,
        executor: E,
        remote_fallback: F,
        dispatcher: D,
        batch_size: usize,
    ) -> Result<Self, LocalTaskRuntimeError> {
        if batch_size == 0 {
            return Err(LocalTaskRuntimeError::InvalidConfiguration);
        }
        Ok(Self {
            outbox,
            state,
            executor,
            remote_fallback,
            dispatcher,
            batch_size,
        })
    }

    /// Executes and checkpoints one ordered batch.
    ///
    /// A crash after completion but before checkpoint causes a retry with the
    /// same activation-derived completion identity. The engine idempotency
    /// boundary therefore returns the prior committed result.
    ///
    /// # Errors
    ///
    /// Returns a typed storage, decoding, execution, dispatch, or checkpoint error.
    pub fn run_once(&self) -> Result<LocalTaskRunOutcome, LocalTaskRuntimeError> {
        self.run_once_with_batch_size(self.batch_size)
    }

    /// Executes one batch with a size captured at the worker safe point.
    ///
    /// # Errors
    ///
    /// Returns a typed configuration, storage, execution, dispatch, or
    /// checkpoint error.
    pub fn run_once_with_batch_size(
        &self,
        batch_size: usize,
    ) -> Result<LocalTaskRunOutcome, LocalTaskRuntimeError> {
        if batch_size == 0 {
            return Err(LocalTaskRuntimeError::InvalidConfiguration);
        }
        let mut checkpoint = self.state.local_task_checkpoint()?;
        let records = self.outbox.read_after(checkpoint, batch_size)?;
        if records.len() > batch_size {
            return Err(LocalTaskRuntimeError::AdapterBatchLimitExceeded);
        }
        let mut executed = 0;
        for record in &records {
            if record.cursor <= checkpoint {
                return Err(LocalTaskRuntimeError::NonContiguousOutbox);
            }
            let envelope = EventCodec::decode(&record.payload)
                .map_err(|error| LocalTaskRuntimeError::CorruptEvent(error.to_string()))?;
            if envelope.metadata.event_id != record.event_id
                || envelope.metadata.tenant_id.as_str() != record.tenant_id
                || envelope.metadata.instance_id.as_str() != record.instance_id
            {
                return Err(LocalTaskRuntimeError::EventScopeMismatch);
            }
            if let Some(activation) = activation(record.cursor, &envelope) {
                match self.executor.execute(&activation)? {
                    LocalTaskExecutionOutcome::Completed => {
                        self.dispatcher.dispatch_completion(&activation)?;
                        executed += 1;
                    }
                    LocalTaskExecutionOutcome::NotHandled => {
                        self.remote_fallback
                            .enqueue_remote_task(&activation)
                            .map_err(|error| LocalTaskRuntimeError::Dispatch(error.to_string()))?;
                    }
                }
            }
            self.state
                .checkpoint_local_task(checkpoint, record.cursor)?;
            checkpoint = record.cursor;
        }
        Ok(LocalTaskRunOutcome {
            scanned: records.len(),
            executed,
            checkpoint,
        })
    }
}

fn activation(cursor: u64, envelope: &crate::EventEnvelope) -> Option<LocalTaskActivation> {
    let common = |node_id: &NodeId,
                  kind,
                  task_type: String,
                  implementation_ref: String,
                  implementation_version: String| LocalTaskActivation {
        cursor,
        event_id: envelope.metadata.event_id.clone(),
        event_sequence: envelope.metadata.sequence,
        tenant_id: envelope.metadata.tenant_id.clone(),
        instance_id: envelope.metadata.instance_id.as_str().to_owned(),
        workflow_type: envelope.metadata.workflow_type.clone(),
        workflow_version: envelope.metadata.workflow_version.clone(),
        node_id: node_id.clone(),
        kind,
        task_type,
        implementation_ref,
        implementation_version,
        occurred_at_epoch_ms: envelope.metadata.occurred_at_epoch_ms,
        correlation_id: envelope.metadata.correlation_id.clone(),
        config_version: envelope.metadata.config_version.clone(),
        policy_version: envelope.metadata.policy_version.clone(),
    };
    match &envelope.event {
        DomainEvent::ServiceTaskActivated {
            node_id, task_type, ..
        } => Some(common(
            node_id,
            LocalTaskKind::Service,
            task_type.as_str().to_owned(),
            task_type.as_str().to_owned(),
            String::new(),
        )),
        DomainEvent::ScriptTaskActivated {
            node_id,
            task_type,
            implementation_ref,
            implementation_version,
            ..
        } => Some(common(
            node_id,
            LocalTaskKind::Script,
            task_type.as_str().to_owned(),
            implementation_ref.clone(),
            implementation_version.clone(),
        )),
        _ => None,
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum LocalTaskRuntimeError {
    #[error("local task runtime configuration is invalid")]
    InvalidConfiguration,
    #[error("local task runtime policy lock is unavailable")]
    PolicyUnavailable,
    #[error("local task adapter exceeded its configured batch limit")]
    AdapterBatchLimitExceeded,
    #[error("local task outbox records are out of order")]
    NonContiguousOutbox,
    #[error("local task event is corrupt: {0}")]
    CorruptEvent(String),
    #[error("local task event does not match its outbox scope")]
    EventScopeMismatch,
    #[error("local task checkpoint compare-and-swap failed")]
    CheckpointConflict,
    #[error("local task storage failed: {0}")]
    Store(String),
    #[error("local task execution failed: {0}")]
    Execution(String),
    #[error("local task completion dispatch failed: {0}")]
    Dispatch(String),
    #[error(transparent)]
    Outbox(#[from] OutboxError),
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use bpmp_domain_core::{
        ActorId, CommandId, ConfigVersion, CorrelationId, DomainEvent, InstanceId, KeyScope,
        PolicyVersion, TaskType, WorkflowType, WorkflowVersion,
    };

    use crate::{EVENT_SCHEMA_VERSION, EventEnvelope, EventMetadata, OutboxRecord};

    use super::*;

    struct Outbox(Vec<OutboxRecord>);
    impl OutboxStorePort for Outbox {
        fn publisher_checkpoint(&self) -> Result<u64, OutboxError> {
            Ok(0)
        }
        fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError> {
            Ok(self
                .0
                .iter()
                .filter(|record| record.cursor > cursor)
                .take(limit)
                .cloned()
                .collect())
        }
        fn checkpoint(&self, _: u64, _: u64) -> Result<(), OutboxError> {
            Ok(())
        }
    }

    #[derive(Default)]
    struct State(Mutex<u64>);
    impl LocalTaskRuntimeStorePort for State {
        fn local_task_checkpoint(&self) -> Result<u64, LocalTaskRuntimeError> {
            Ok(*self.0.lock().unwrap())
        }
        fn checkpoint_local_task(
            &self,
            expected: u64,
            committed: u64,
        ) -> Result<(), LocalTaskRuntimeError> {
            let mut value = self.0.lock().unwrap();
            if *value != expected {
                return Err(LocalTaskRuntimeError::CheckpointConflict);
            }
            *value = committed;
            Ok(())
        }
    }

    #[derive(Default)]
    struct Executor(Mutex<usize>);
    impl LocalTaskExecutorPort for Executor {
        fn execute(
            &self,
            _: &LocalTaskActivation,
        ) -> Result<LocalTaskExecutionOutcome, LocalTaskRuntimeError> {
            *self.0.lock().unwrap() += 1;
            Ok(LocalTaskExecutionOutcome::Completed)
        }
    }
    #[derive(Default)]
    struct Dispatcher(Mutex<Vec<String>>);
    impl LocalTaskCompletionDispatcherPort for Dispatcher {
        fn dispatch_completion(
            &self,
            activation: &LocalTaskActivation,
        ) -> Result<(), LocalTaskRuntimeError> {
            self.0.lock().unwrap().push(activation.event_id.clone());
            Ok(())
        }
    }

    struct NotHandledExecutor;
    impl LocalTaskExecutorPort for NotHandledExecutor {
        fn execute(
            &self,
            _: &LocalTaskActivation,
        ) -> Result<LocalTaskExecutionOutcome, LocalTaskRuntimeError> {
            Ok(LocalTaskExecutionOutcome::NotHandled)
        }
    }

    #[derive(Default)]
    struct RemoteIngress(Mutex<Vec<String>>);

    impl RemoteTaskIngressPort for RemoteIngress {
        fn enqueue_remote_task(
            &self,
            activation: &LocalTaskActivation,
        ) -> Result<crate::RemoteTaskEnqueueOutcome, crate::RemoteTaskError> {
            self.0.lock().unwrap().push(activation.event_id.clone());
            Ok(crate::RemoteTaskEnqueueOutcome::Enqueued)
        }
    }

    struct FailingExecutor(Mutex<u32>);
    impl LocalTaskExecutorPort for &FailingExecutor {
        fn execute(
            &self,
            _: &LocalTaskActivation,
        ) -> Result<LocalTaskExecutionOutcome, LocalTaskRuntimeError> {
            *self.0.lock().unwrap() += 1;
            Err(LocalTaskRuntimeError::Execution("transient".into()))
        }
    }

    #[derive(Default)]
    struct Delay(Mutex<Vec<u64>>);
    impl RetryDelayPort for &Delay {
        fn wait(&self, delay_ms: u64) {
            self.0.lock().unwrap().push(delay_ms);
        }
    }

    fn activation_fixture() -> LocalTaskActivation {
        LocalTaskActivation {
            cursor: 1,
            event_id: "event-1".into(),
            event_sequence: 1,
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: "instance-a".into(),
            workflow_type: WorkflowType::new("order").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
            node_id: NodeId::new("service").unwrap(),
            kind: LocalTaskKind::Service,
            task_type: "payment".into(),
            implementation_ref: "wasm://payment".into(),
            implementation_version: "sha256:abc".into(),
            occurred_at_epoch_ms: 1,
            correlation_id: CorrelationId::new("correlation-a").unwrap(),
            config_version: ConfigVersion::new("config-a").unwrap(),
            policy_version: PolicyVersion::new("policy-a").unwrap(),
        }
    }

    proptest::proptest! {
        #![proptest_config(proptest::test_runner::Config::with_cases(100))]

        // Feature: rust-bpm-platform, Property 10: Service-task retry stops at the configured attempt bound
        #[test]
        fn service_retry_uses_exact_configured_attempts(max_attempts in 1_u32..20) {
            let executor = FailingExecutor(Mutex::new(0));
            let delay = Delay::default();
            let retrying = RetryingLocalTaskExecutor::new(
                &executor,
                &delay,
                LocalTaskRetryPolicy {
                    max_attempts,
                    initial_backoff_ms: 1,
                    max_backoff_ms: 8,
                    multiplier_millis: 2_000,
                },
            )
            .unwrap();
            let result = retrying.execute(&activation_fixture());
            proptest::prop_assert!(result.is_err());
            proptest::prop_assert_eq!(*executor.0.lock().unwrap(), max_attempts);
            proptest::prop_assert_eq!(delay.0.lock().unwrap().len(), max_attempts.saturating_sub(1) as usize);
        }
    }

    #[test]
    fn committed_activation_executes_and_advances_independent_checkpoint() {
        let envelope = EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 100,
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/workflow").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
            },
            event: DomainEvent::ScriptTaskActivated {
                node_id: NodeId::new("calculate").unwrap(),
                task_type: TaskType::new("calculate").unwrap(),
                implementation_ref: "wasm://risk/calculate".into(),
                implementation_version: "sha256:abc".into(),
                occurred_at_epoch_ms: 100,
            },
        };
        let runtime = LocalTaskRuntime::new(
            Outbox(vec![OutboxRecord {
                cursor: 1,
                tenant_id: "tenant-a".into(),
                instance_id: "instance-1".into(),
                event_id: "event-1".into(),
                payload: EventCodec::encode(&envelope),
            }]),
            State::default(),
            Executor::default(),
            Arc::new(RemoteIngress::default()),
            Dispatcher::default(),
            8,
        )
        .unwrap();
        let outcome = runtime.run_once().unwrap();
        assert_eq!(outcome.executed, 1);
        assert_eq!(outcome.checkpoint, 1);
    }

    #[test]
    fn remote_service_activation_advances_checkpoint_without_local_completion() {
        let envelope = EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-remote".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 100,
                config_version: ConfigVersion::new("config-1").unwrap(),
                policy_version: PolicyVersion::new("policy-1").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/workflow").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
            },
            event: DomainEvent::ServiceTaskActivated {
                node_id: NodeId::new("ship").unwrap(),
                task_type: TaskType::new("remote-shipping").unwrap(),
                occurred_at_epoch_ms: 100,
            },
        };
        let ingress = Arc::new(RemoteIngress::default());
        let runtime = LocalTaskRuntime::new(
            Outbox(vec![OutboxRecord {
                cursor: 1,
                tenant_id: "tenant-a".into(),
                instance_id: "instance-1".into(),
                event_id: "event-remote".into(),
                payload: EventCodec::encode(&envelope),
            }]),
            State::default(),
            NotHandledExecutor,
            ingress.clone(),
            Dispatcher::default(),
            8,
        )
        .unwrap();

        let outcome = runtime.run_once().unwrap();
        assert_eq!(outcome.executed, 0);
        assert_eq!(outcome.checkpoint, 1);
        assert_eq!(
            ingress.0.lock().unwrap().as_slice(),
            ["event-remote".to_owned()]
        );
    }
}
