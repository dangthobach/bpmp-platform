use bpmp_governance_domain::{CompensationLedgerEntry, CompensationStatus};
use thiserror::Error;

#[allow(clippy::missing_errors_doc)]
pub trait CompensationStorePort {
    fn pending_entries(
        &self,
        tenant_id: &str,
        instance_id: &str,
        limit: usize,
    ) -> Result<Vec<CompensationLedgerEntry>, CompensationRuntimeError>;

    /// Atomically marks the entry compensated using its append-only ledger identity.
    fn mark_compensated(
        &self,
        entry: &CompensationLedgerEntry,
        completed_at_epoch_ms: u64,
    ) -> Result<(), CompensationRuntimeError>;
}

#[allow(clippy::missing_errors_doc)]
pub trait CompensationExecutorPort {
    fn compensate(&self, entry: &CompensationLedgerEntry) -> Result<(), CompensationRuntimeError>;
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum CompensationRunOutcome {
    Completed,
    Compensated { ledger_entry_id: String },
}

pub struct CompensationRuntime<S, E> {
    store: S,
    executor: E,
    max_pending_entries: usize,
}

impl<S, E> CompensationRuntime<S, E>
where
    S: CompensationStorePort,
    E: CompensationExecutorPort,
{
    /// Creates a bounded compensation worker.
    ///
    /// # Errors
    ///
    /// Returns an error when the pending-entry bound is zero.
    pub fn new(
        store: S,
        executor: E,
        max_pending_entries: usize,
    ) -> Result<Self, CompensationRuntimeError> {
        if max_pending_entries == 0 {
            return Err(CompensationRuntimeError::InvalidConfiguration);
        }
        Ok(Self {
            store,
            executor,
            max_pending_entries,
        })
    }

    /// Compensates the latest durable side effect that is still pending.
    ///
    /// A crash before `mark_compensated` retries the same idempotency key. A
    /// crash after it resumes at the next lower effect sequence.
    ///
    /// # Errors
    ///
    /// Returns a typed configuration, scope, storage, or execution error.
    pub fn run_next(
        &self,
        tenant_id: &str,
        instance_id: &str,
        completed_at_epoch_ms: u64,
    ) -> Result<CompensationRunOutcome, CompensationRuntimeError> {
        if tenant_id.trim().is_empty() || instance_id.trim().is_empty() {
            return Err(CompensationRuntimeError::InvalidScope);
        }
        let mut entries =
            self.store
                .pending_entries(tenant_id, instance_id, self.max_pending_entries)?;
        if entries.len() > self.max_pending_entries {
            return Err(CompensationRuntimeError::PendingLimitExceeded);
        }
        entries.retain(|entry| entry.status == CompensationStatus::Pending);
        entries.sort_unstable_by(|left, right| {
            (
                right.effect_sequence,
                right.ledger_sequence,
                right.ledger_entry_id.as_str(),
            )
                .cmp(&(
                    left.effect_sequence,
                    left.ledger_sequence,
                    left.ledger_entry_id.as_str(),
                ))
        });
        let Some(entry) = entries.first() else {
            return Ok(CompensationRunOutcome::Completed);
        };
        if entry.tenant_id != tenant_id || entry.instance_id != instance_id {
            return Err(CompensationRuntimeError::ScopeMismatch);
        }
        self.executor.compensate(entry)?;
        self.store.mark_compensated(entry, completed_at_epoch_ms)?;
        Ok(CompensationRunOutcome::Compensated {
            ledger_entry_id: entry.ledger_entry_id.clone(),
        })
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum CompensationRuntimeError {
    #[error("compensation runtime configuration is invalid")]
    InvalidConfiguration,
    #[error("compensation scope is invalid")]
    InvalidScope,
    #[error("compensation ledger entry does not match the requested scope")]
    ScopeMismatch,
    #[error("pending compensation result exceeds the configured bound")]
    PendingLimitExceeded,
    #[error("compensation store failed: {0}")]
    Store(String),
    #[error("compensation handler failed: {0}")]
    Execution(String),
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;

    use proptest::prelude::*;

    use super::*;

    struct Store(Mutex<Vec<CompensationLedgerEntry>>);

    impl CompensationStorePort for Store {
        fn pending_entries(
            &self,
            _: &str,
            _: &str,
            limit: usize,
        ) -> Result<Vec<CompensationLedgerEntry>, CompensationRuntimeError> {
            Ok(self.0.lock().unwrap().iter().take(limit).cloned().collect())
        }

        fn mark_compensated(
            &self,
            entry: &CompensationLedgerEntry,
            _: u64,
        ) -> Result<(), CompensationRuntimeError> {
            let mut entries = self.0.lock().unwrap();
            let current = entries
                .iter_mut()
                .find(|candidate| candidate.ledger_entry_id == entry.ledger_entry_id)
                .ok_or_else(|| CompensationRuntimeError::Store("missing entry".into()))?;
            current.status = CompensationStatus::Compensated;
            Ok(())
        }
    }

    struct Executor(Mutex<Vec<u64>>);

    impl CompensationExecutorPort for Executor {
        fn compensate(
            &self,
            entry: &CompensationLedgerEntry,
        ) -> Result<(), CompensationRuntimeError> {
            self.0.lock().unwrap().push(entry.effect_sequence);
            Ok(())
        }
    }

    fn entry(sequence: u64) -> CompensationLedgerEntry {
        CompensationLedgerEntry {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-a".into(),
            saga_ref: "saga-a".into(),
            ledger_entry_id: format!("entry-{sequence}"),
            effect_sequence: sequence,
            ledger_sequence: sequence,
            side_effect_type: "payment".into(),
            target_system: "ledger".into(),
            handler_ref: "refund".into(),
            opaque_operation_ref: format!("operation-{sequence}"),
            idempotency_key: format!("compensate-{sequence}"),
            status: CompensationStatus::Pending,
            updated_at_epoch_ms: 1,
        }
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 7: Saga compensation executes in reverse order and resumes
        #[test]
        fn compensation_is_reverse_order_and_resume_safe(count in 1_u64..32) {
            let store = Store(Mutex::new((1..=count).map(entry).collect()));
            let executor = Executor(Mutex::new(Vec::new()));
            let runtime = CompensationRuntime::new(&store, &executor, 64).unwrap();

            for _ in 0..count {
                runtime.run_next("tenant-a", "instance-a", 10).unwrap();
            }
            prop_assert_eq!(
                executor.0.lock().unwrap().clone(),
                (1..=count).rev().collect::<Vec<_>>()
            );
            prop_assert_eq!(
                runtime.run_next("tenant-a", "instance-a", 10).unwrap(),
                CompensationRunOutcome::Completed
            );
        }
    }

    impl<T: CompensationStorePort + ?Sized> CompensationStorePort for &T {
        fn pending_entries(
            &self,
            tenant_id: &str,
            instance_id: &str,
            limit: usize,
        ) -> Result<Vec<CompensationLedgerEntry>, CompensationRuntimeError> {
            (**self).pending_entries(tenant_id, instance_id, limit)
        }

        fn mark_compensated(
            &self,
            entry: &CompensationLedgerEntry,
            completed_at_epoch_ms: u64,
        ) -> Result<(), CompensationRuntimeError> {
            (**self).mark_compensated(entry, completed_at_epoch_ms)
        }
    }

    impl<T: CompensationExecutorPort + ?Sized> CompensationExecutorPort for &T {
        fn compensate(
            &self,
            entry: &CompensationLedgerEntry,
        ) -> Result<(), CompensationRuntimeError> {
            (**self).compensate(entry)
        }
    }
}
