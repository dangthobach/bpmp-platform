use std::sync::Arc;

use bpmp_domain_core::{DomainEvent, NodeId, TenantId, WorkflowType, WorkflowVersion};
use thiserror::Error;

use crate::{EventCodec, OutboxError, OutboxStorePort};

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

pub struct LocalTaskRuntime<S, R, E, D> {
    outbox: S,
    state: R,
    executor: E,
    dispatcher: D,
    batch_size: usize,
}

impl<S, R, E, D> LocalTaskRuntime<S, R, E, D>
where
    S: OutboxStorePort,
    R: LocalTaskRuntimeStorePort,
    E: LocalTaskExecutorPort,
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
        let mut checkpoint = self.state.local_task_checkpoint()?;
        let records = self.outbox.read_after(checkpoint, self.batch_size)?;
        if records.len() > self.batch_size {
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
            if let Some(activation) = activation(record.cursor, &envelope)
                && self.executor.execute(&activation)? == LocalTaskExecutionOutcome::Completed
            {
                self.dispatcher.dispatch_completion(&activation)?;
                executed += 1;
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
    use std::sync::Mutex;

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
            Dispatcher::default(),
            8,
        )
        .unwrap();

        let outcome = runtime.run_once().unwrap();
        assert_eq!(outcome.executed, 0);
        assert_eq!(outcome.checkpoint, 1);
    }
}
