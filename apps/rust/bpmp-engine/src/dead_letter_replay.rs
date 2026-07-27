use bpmp_domain_core::{NodeId, TenantId};
use thiserror::Error;

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct DeadLetterEntry {
    pub tenant_id: TenantId,
    pub instance_id: String,
    pub failed_node_id: NodeId,
    pub original_event_id: String,
    pub payload_reference: String,
    pub attempt_count: u32,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum DeadLetterReplayOutcome {
    Replayed { node_id: NodeId },
    Duplicate { node_id: NodeId },
}

#[allow(clippy::missing_errors_doc)]
pub trait DeadLetterReplayStorePort {
    fn load(&self, dead_letter_id: &str) -> Result<DeadLetterEntry, DeadLetterReplayError>;
    fn replay_result(
        &self,
        tenant_id: &TenantId,
        dead_letter_id: &str,
        replay_id: &str,
    ) -> Result<Option<NodeId>, DeadLetterReplayError>;
    fn commit_replay(
        &self,
        dead_letter_id: &str,
        replay_id: &str,
        node_id: &NodeId,
    ) -> Result<(), DeadLetterReplayError>;
}

#[allow(clippy::missing_errors_doc)]
pub trait DeadLetterReplayExecutorPort {
    fn replay_node(
        &self,
        replay_id: &str,
        entry: &DeadLetterEntry,
    ) -> Result<(), DeadLetterReplayError>;
}

pub struct DeadLetterReplayRuntime<S, E> {
    store: S,
    executor: E,
}

impl<S, E> DeadLetterReplayRuntime<S, E>
where
    S: DeadLetterReplayStorePort,
    E: DeadLetterReplayExecutorPort,
{
    pub const fn new(store: S, executor: E) -> Self {
        Self { store, executor }
    }

    /// Replays exactly the failed node and commits a stable replay identity.
    ///
    /// # Errors
    ///
    /// Fails closed for invalid identity, cross-tenant access, or adapter errors.
    pub fn replay(
        &self,
        tenant_id: &TenantId,
        dead_letter_id: &str,
        replay_id: &str,
    ) -> Result<DeadLetterReplayOutcome, DeadLetterReplayError> {
        if dead_letter_id.trim().is_empty() || replay_id.trim().is_empty() {
            return Err(DeadLetterReplayError::InvalidIdentity);
        }
        if let Some(node_id) = self
            .store
            .replay_result(tenant_id, dead_letter_id, replay_id)?
        {
            return Ok(DeadLetterReplayOutcome::Duplicate { node_id });
        }
        let entry = self.store.load(dead_letter_id)?;
        if &entry.tenant_id != tenant_id {
            return Err(DeadLetterReplayError::TenantDenied);
        }
        self.executor.replay_node(replay_id, &entry)?;
        self.store
            .commit_replay(dead_letter_id, replay_id, &entry.failed_node_id)?;
        Ok(DeadLetterReplayOutcome::Replayed {
            node_id: entry.failed_node_id,
        })
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum DeadLetterReplayError {
    #[error("dead-letter or replay identity is invalid")]
    InvalidIdentity,
    #[error("dead-letter access is denied for this tenant")]
    TenantDenied,
    #[error("dead-letter record was not found")]
    NotFound,
    #[error("dead-letter replay store failed: {0}")]
    Store(String),
    #[error("dead-letter replay execution failed: {0}")]
    Execution(String),
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::sync::Mutex;

    use proptest::prelude::*;

    use super::*;

    struct Store {
        entry: DeadLetterEntry,
        results: Mutex<BTreeMap<String, NodeId>>,
    }

    impl DeadLetterReplayStorePort for &Store {
        fn load(&self, _: &str) -> Result<DeadLetterEntry, DeadLetterReplayError> {
            Ok(self.entry.clone())
        }

        fn replay_result(
            &self,
            tenant_id: &TenantId,
            _: &str,
            replay_id: &str,
        ) -> Result<Option<NodeId>, DeadLetterReplayError> {
            if tenant_id != &self.entry.tenant_id {
                return Err(DeadLetterReplayError::TenantDenied);
            }
            Ok(self.results.lock().unwrap().get(replay_id).cloned())
        }

        fn commit_replay(
            &self,
            _: &str,
            replay_id: &str,
            node_id: &NodeId,
        ) -> Result<(), DeadLetterReplayError> {
            self.results
                .lock()
                .unwrap()
                .insert(replay_id.into(), node_id.clone());
            Ok(())
        }
    }

    struct Executor(Mutex<Vec<NodeId>>);
    impl DeadLetterReplayExecutorPort for &Executor {
        fn replay_node(
            &self,
            _: &str,
            entry: &DeadLetterEntry,
        ) -> Result<(), DeadLetterReplayError> {
            self.0.lock().unwrap().push(entry.failed_node_id.clone());
            Ok(())
        }
    }

    fn fixture(node: u16) -> (Store, Executor) {
        (
            Store {
                entry: DeadLetterEntry {
                    tenant_id: TenantId::new("tenant-a").unwrap(),
                    instance_id: "instance-a".into(),
                    failed_node_id: NodeId::new(format!("node-{node}")).unwrap(),
                    original_event_id: "event-a".into(),
                    payload_reference: "payload-a".into(),
                    attempt_count: 3,
                },
                results: Mutex::new(BTreeMap::new()),
            },
            Executor(Mutex::new(Vec::new())),
        )
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 44: Dead-letter replay is node-local and idempotent
        #[test]
        fn replay_only_executes_failed_node_once(node in 1_u16..1_000) {
            let (store, executor) = fixture(node);
            let runtime = DeadLetterReplayRuntime::new(&store, &executor);
            let tenant = TenantId::new("tenant-a").unwrap();
            let first = runtime.replay(&tenant, "dl-1", "replay-1").unwrap();
            let second = runtime.replay(&tenant, "dl-1", "replay-1").unwrap();
            let first_replayed = matches!(first, DeadLetterReplayOutcome::Replayed { .. });
            let second_duplicate = matches!(second, DeadLetterReplayOutcome::Duplicate { .. });
            prop_assert!(first_replayed);
            prop_assert!(second_duplicate);
            let executed = executor.0.lock().unwrap().clone();
            prop_assert_eq!(executed, vec![NodeId::new(format!("node-{node}")).unwrap()]);
        }

        // Feature: rust-bpm-platform, Property 46: Cross-tenant engine access is denied without mutation
        #[test]
        fn cross_tenant_replay_is_denied_without_mutation(node in 1_u16..1_000) {
            let (store, executor) = fixture(node);
            let runtime = DeadLetterReplayRuntime::new(&store, &executor);
            let other = TenantId::new("tenant-b").unwrap();
            prop_assert_eq!(
                runtime.replay(&other, "dl-1", "replay-1").unwrap_err(),
                DeadLetterReplayError::TenantDenied
            );
            prop_assert!(executor.0.lock().unwrap().is_empty());
            prop_assert!(store.results.lock().unwrap().is_empty());
        }
    }
}
