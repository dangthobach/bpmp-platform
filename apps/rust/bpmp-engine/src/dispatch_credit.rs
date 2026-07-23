use std::collections::{BTreeMap, BTreeSet};

use thiserror::Error;

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct DispatchLimits {
    pub max_workers: u32,
    pub max_credits_per_worker: u32,
    pub max_identifier_bytes: u32,
}

impl DispatchLimits {
    /// Validates all resource bounds.
    ///
    /// # Errors
    ///
    /// Returns [`CreditError::InvalidLimits`] when any bound is zero.
    pub fn validate(&self) -> Result<(), CreditError> {
        if self.max_workers == 0
            || self.max_credits_per_worker == 0
            || self.max_identifier_bytes == 0
        {
            return Err(CreditError::InvalidLimits);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
struct WorkerCredits {
    granted: u32,
    inflight: BTreeSet<String>,
}

/// Deterministic credit ledger. External dispatch is allowed only after a
/// successful reservation and completion is fed back as an explicit input.
pub struct CreditController {
    limits: DispatchLimits,
    workers: BTreeMap<String, WorkerCredits>,
}

impl CreditController {
    /// Creates an empty bounded controller.
    ///
    /// # Errors
    ///
    /// Returns [`CreditError::InvalidLimits`] for invalid limits.
    pub fn new(limits: DispatchLimits) -> Result<Self, CreditError> {
        limits.validate()?;
        Ok(Self {
            limits,
            workers: BTreeMap::new(),
        })
    }

    /// Replaces the worker's advertised credit.
    ///
    /// # Errors
    ///
    /// Returns a typed bound or inflight conflict error.
    pub fn grant(&mut self, worker_id: &str, credits: u32) -> Result<(), CreditError> {
        self.validate_identifier(worker_id)?;
        if credits > self.limits.max_credits_per_worker {
            return Err(CreditError::CreditLimitExceeded);
        }
        if !self.workers.contains_key(worker_id)
            && self.workers.len() >= self.limits.max_workers as usize
        {
            return Err(CreditError::WorkerLimitExceeded);
        }
        let worker = self
            .workers
            .entry(worker_id.to_owned())
            .or_insert_with(|| WorkerCredits {
                granted: 0,
                inflight: BTreeSet::new(),
            });
        if worker.inflight.len() > credits as usize {
            return Err(CreditError::CreditBelowInflight);
        }
        worker.granted = credits;
        Ok(())
    }

    /// Reserves one credit before external dispatch.
    ///
    /// # Errors
    ///
    /// Returns a typed worker, duplicate, identifier or exhaustion error.
    pub fn reserve(&mut self, worker_id: &str, task_id: &str) -> Result<(), CreditError> {
        self.validate_identifier(worker_id)?;
        self.validate_identifier(task_id)?;
        let worker = self
            .workers
            .get_mut(worker_id)
            .ok_or(CreditError::UnknownWorker)?;
        if worker.inflight.contains(task_id) {
            return Err(CreditError::DuplicateTask);
        }
        if worker.inflight.len() >= worker.granted as usize {
            return Err(CreditError::CreditExhausted);
        }
        worker.inflight.insert(task_id.to_owned());
        Ok(())
    }

    /// Releases one acknowledged reservation.
    ///
    /// # Errors
    ///
    /// Returns a typed error when the worker or task is unknown.
    pub fn complete(&mut self, worker_id: &str, task_id: &str) -> Result<(), CreditError> {
        let worker = self
            .workers
            .get_mut(worker_id)
            .ok_or(CreditError::UnknownWorker)?;
        if !worker.inflight.remove(task_id) {
            return Err(CreditError::UnknownTask);
        }
        Ok(())
    }

    #[must_use]
    pub fn disconnect(&mut self, worker_id: &str) -> Vec<String> {
        self.workers
            .remove(worker_id)
            .map_or_else(Vec::new, |worker| worker.inflight.into_iter().collect())
    }

    #[must_use]
    pub fn invariant_holds(&self) -> bool {
        self.workers.values().all(|worker| {
            worker.inflight.len() <= worker.granted as usize
                && worker.granted <= self.limits.max_credits_per_worker
        })
    }

    fn validate_identifier(&self, value: &str) -> Result<(), CreditError> {
        if value.trim().is_empty() || value.len() > self.limits.max_identifier_bytes as usize {
            return Err(CreditError::InvalidIdentifier);
        }
        Ok(())
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum CreditError {
    #[error("dispatch limits must be positive")]
    InvalidLimits,
    #[error("worker or task identifier is invalid")]
    InvalidIdentifier,
    #[error("worker limit exceeded")]
    WorkerLimitExceeded,
    #[error("worker credit limit exceeded")]
    CreditLimitExceeded,
    #[error("advertised credit is below current inflight count")]
    CreditBelowInflight,
    #[error("worker is unknown")]
    UnknownWorker,
    #[error("worker has no available credit")]
    CreditExhausted,
    #[error("task is already inflight for this worker")]
    DuplicateTask,
    #[error("task reservation is unknown")]
    UnknownTask,
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::*;

    #[derive(Debug, Clone)]
    enum Operation {
        Grant(u8, u8),
        Reserve(u8, u8),
        Complete(u8, u8),
        Disconnect(u8),
    }

    fn operation() -> impl Strategy<Value = Operation> {
        prop_oneof![
            (0_u8..4, 0_u8..8).prop_map(|(worker, credits)| Operation::Grant(worker, credits)),
            (0_u8..4, any::<u8>()).prop_map(|(worker, task)| Operation::Reserve(worker, task)),
            (0_u8..4, any::<u8>()).prop_map(|(worker, task)| Operation::Complete(worker, task)),
            (0_u8..4).prop_map(Operation::Disconnect),
        ]
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        // Feature: rust-bpm-platform, Property 16: credit backpressure invariant
        #[test]
        fn arbitrary_dispatch_schedule_never_exceeds_advertised_credit(
            operations in prop::collection::vec(operation(), 0..256)
        ) {
            let mut controller = CreditController::new(DispatchLimits {
                max_workers: 4,
                max_credits_per_worker: 8,
                max_identifier_bytes: 32,
            }).unwrap();
            for operation in operations {
                match operation {
                    Operation::Grant(worker, credits) => {
                        let _ = controller.grant(&format!("worker-{worker}"), u32::from(credits));
                    }
                    Operation::Reserve(worker, task) => {
                        let _ = controller.reserve(&format!("worker-{worker}"), &format!("task-{task}"));
                    }
                    Operation::Complete(worker, task) => {
                        let _ = controller.complete(&format!("worker-{worker}"), &format!("task-{task}"));
                    }
                    Operation::Disconnect(worker) => {
                        let _ = controller.disconnect(&format!("worker-{worker}"));
                    }
                }
                prop_assert!(controller.invariant_holds());
            }
        }
    }

    #[test]
    fn zero_credit_rejects_dispatch() {
        let mut controller = CreditController::new(DispatchLimits {
            max_workers: 1,
            max_credits_per_worker: 1,
            max_identifier_bytes: 32,
        })
        .unwrap();
        controller.grant("worker", 0).unwrap();
        assert_eq!(
            controller.reserve("worker", "task"),
            Err(CreditError::CreditExhausted)
        );
    }
}
