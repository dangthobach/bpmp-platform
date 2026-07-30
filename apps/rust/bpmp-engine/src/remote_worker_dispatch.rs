use std::collections::{BTreeMap, BTreeSet};

use thiserror::Error;

use bpmp_domain_core::TenantId;

use crate::{CreditController, CreditError, DispatchLimits};

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RemoteWorkerLimits {
    pub dispatch: DispatchLimits,
    pub max_capabilities_per_worker: usize,
    pub max_protocol_version_bytes: usize,
}

impl RemoteWorkerLimits {
    fn validate(&self) -> Result<(), RemoteDispatchError> {
        self.dispatch.validate()?;
        if self.max_capabilities_per_worker == 0 || self.max_protocol_version_bytes == 0 {
            return Err(RemoteDispatchError::InvalidLimits);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RemoteWorkerRegistration {
    pub session_id: String,
    pub frame_sequence: u64,
    pub worker_id: String,
    pub tenant_id: TenantId,
    pub protocol_version: String,
    pub capabilities: BTreeSet<String>,
    pub initial_credit: u32,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum RemoteTaskAcknowledgement {
    Accepted,
    Completed,
    Failed,
}

#[derive(Debug, Clone, Eq, PartialEq)]
struct RegisteredWorker {
    session_id: String,
    tenant_id: TenantId,
    capabilities: BTreeSet<String>,
    last_frame_sequence: u64,
}

/// Deterministic session and capability registry for the streaming transport.
///
/// Network identity verification, durable leases, clocks, and frame I/O stay in
/// adapters. This registry only accepts explicit inputs and owns no I/O.
pub struct RemoteDispatchRegistry {
    limits: RemoteWorkerLimits,
    credits: CreditController,
    workers: BTreeMap<String, RegisteredWorker>,
    sessions: BTreeMap<String, String>,
}

impl RemoteDispatchRegistry {
    /// Creates an empty bounded registry.
    ///
    /// # Errors
    ///
    /// Returns a typed error when any configured resource limit is zero.
    pub fn new(limits: RemoteWorkerLimits) -> Result<Self, RemoteDispatchError> {
        limits.validate()?;
        Ok(Self {
            credits: CreditController::new(limits.dispatch.clone())?,
            limits,
            workers: BTreeMap::new(),
            sessions: BTreeMap::new(),
        })
    }

    /// Applies new resource bounds between frames.
    ///
    /// # Errors
    ///
    /// Rejects invalid bounds or bounds below active session state.
    pub fn replace_limits(
        &mut self,
        limits: RemoteWorkerLimits,
    ) -> Result<(), RemoteDispatchError> {
        limits.validate()?;
        if self
            .workers
            .values()
            .any(|worker| worker.capabilities.len() > limits.max_capabilities_per_worker)
        {
            return Err(RemoteDispatchError::ActiveStateExceedsNewLimits);
        }
        self.credits.replace_limits(limits.dispatch.clone())?;
        self.limits = limits;
        Ok(())
    }

    /// Registers one authenticated stream and applies its initial credit.
    ///
    /// # Errors
    ///
    /// Rejects duplicate identities, invalid bounds, empty capabilities, and
    /// malformed protocol versions without changing existing sessions.
    pub fn register(
        &mut self,
        registration: RemoteWorkerRegistration,
    ) -> Result<(), RemoteDispatchError> {
        self.validate_registration(&registration)?;
        if self.workers.contains_key(&registration.worker_id)
            || self.sessions.contains_key(&registration.session_id)
        {
            return Err(RemoteDispatchError::DuplicateRegistration);
        }
        self.credits
            .grant(&registration.worker_id, registration.initial_credit)?;
        self.sessions.insert(
            registration.session_id.clone(),
            registration.worker_id.clone(),
        );
        self.workers.insert(
            registration.worker_id,
            RegisteredWorker {
                session_id: registration.session_id,
                tenant_id: registration.tenant_id,
                capabilities: registration.capabilities,
                last_frame_sequence: registration.frame_sequence,
            },
        );
        Ok(())
    }

    /// Replaces a worker's absolute inflight bound at one ordered frame.
    ///
    /// # Errors
    ///
    /// Rejects unknown sessions, replayed frames, or credit below inflight.
    pub fn grant_credit(
        &mut self,
        session_id: &str,
        frame_sequence: u64,
        credit: u32,
    ) -> Result<(), RemoteDispatchError> {
        let worker_id = self.accept_frame(session_id, frame_sequence)?;
        self.credits.grant(&worker_id, credit)?;
        Ok(())
    }

    /// Selects the first compatible worker with credit and reserves before send.
    ///
    /// # Errors
    ///
    /// Returns `NoCapableWorker` or `Backpressured` without reserving a task.
    pub fn reserve_assignment(
        &mut self,
        task_id: &str,
        tenant_id: &TenantId,
        capability: &str,
    ) -> Result<String, RemoteDispatchError> {
        if capability.trim().is_empty()
            || capability.len() > self.limits.dispatch.max_identifier_bytes as usize
        {
            return Err(RemoteDispatchError::InvalidCapability);
        }
        let candidates: Vec<_> = self
            .workers
            .iter()
            .filter(|(_, worker)| {
                &worker.tenant_id == tenant_id && worker.capabilities.contains(capability)
            })
            .map(|(worker_id, _)| worker_id.clone())
            .collect();
        if candidates.is_empty() {
            return Err(RemoteDispatchError::NoCapableWorker);
        }
        for worker_id in candidates {
            match self.credits.reserve(&worker_id, task_id) {
                Ok(()) => return Ok(worker_id),
                Err(CreditError::CreditExhausted) => {}
                Err(error) => return Err(error.into()),
            }
        }
        Err(RemoteDispatchError::Backpressured)
    }

    /// Accepts one ordered heartbeat without changing credit.
    ///
    /// # Errors
    ///
    /// Rejects an unknown session or a replayed/out-of-order frame.
    pub fn heartbeat(
        &mut self,
        session_id: &str,
        frame_sequence: u64,
    ) -> Result<(), RemoteDispatchError> {
        self.accept_frame(session_id, frame_sequence).map(drop)
    }

    /// Releases a reservation when the durable claim could not be committed.
    ///
    /// # Errors
    ///
    /// Rejects unknown workers and assignments that are not currently inflight.
    pub fn cancel_reservation(
        &mut self,
        worker_id: &str,
        task_id: &str,
    ) -> Result<(), RemoteDispatchError> {
        self.credits.complete(worker_id, task_id)?;
        Ok(())
    }

    #[must_use]
    pub fn session_for_worker(&self, worker_id: &str) -> Option<&str> {
        self.workers
            .get(worker_id)
            .map(|worker| worker.session_id.as_str())
    }

    /// Applies an ordered acknowledgement and releases terminal assignments.
    ///
    /// # Errors
    ///
    /// Rejects unknown sessions/tasks and replayed or unordered frames.
    pub fn acknowledge(
        &mut self,
        session_id: &str,
        frame_sequence: u64,
        task_id: &str,
        status: RemoteTaskAcknowledgement,
    ) -> Result<(), RemoteDispatchError> {
        let worker_id = self.accept_frame(session_id, frame_sequence)?;
        if matches!(
            status,
            RemoteTaskAcknowledgement::Completed | RemoteTaskAcknowledgement::Failed
        ) {
            self.credits.complete(&worker_id, task_id)?;
        }
        Ok(())
    }

    /// Removes a disconnected stream and returns inflight task IDs for durable
    /// lease release and idempotent re-dispatch.
    ///
    /// # Errors
    ///
    /// Returns `UnknownSession` without changing the registry.
    pub fn disconnect(&mut self, session_id: &str) -> Result<Vec<String>, RemoteDispatchError> {
        let worker_id = self
            .sessions
            .remove(session_id)
            .ok_or(RemoteDispatchError::UnknownSession)?;
        self.workers.remove(&worker_id);
        Ok(self.credits.disconnect(&worker_id))
    }

    #[must_use]
    pub fn invariant_holds(&self) -> bool {
        self.credits.invariant_holds()
            && self.workers.len() == self.sessions.len()
            && self
                .workers
                .iter()
                .all(|(worker_id, worker)| self.sessions.get(&worker.session_id) == Some(worker_id))
    }

    fn validate_registration(
        &self,
        registration: &RemoteWorkerRegistration,
    ) -> Result<(), RemoteDispatchError> {
        let max_identifier_bytes = self.limits.dispatch.max_identifier_bytes as usize;
        if registration.session_id.trim().is_empty()
            || registration.session_id.len() > max_identifier_bytes
            || registration.frame_sequence == 0
            || registration.worker_id.trim().is_empty()
            || registration.worker_id.len() > max_identifier_bytes
            || registration.protocol_version.trim().is_empty()
            || registration.protocol_version.len() > self.limits.max_protocol_version_bytes
            || registration.capabilities.is_empty()
            || registration.capabilities.len() > self.limits.max_capabilities_per_worker
            || registration.capabilities.iter().any(|capability| {
                capability.trim().is_empty() || capability.len() > max_identifier_bytes
            })
        {
            return Err(RemoteDispatchError::InvalidRegistration);
        }
        Ok(())
    }

    fn accept_frame(
        &mut self,
        session_id: &str,
        frame_sequence: u64,
    ) -> Result<String, RemoteDispatchError> {
        let worker_id = self
            .sessions
            .get(session_id)
            .ok_or(RemoteDispatchError::UnknownSession)?
            .clone();
        let worker = self
            .workers
            .get_mut(&worker_id)
            .ok_or(RemoteDispatchError::UnknownSession)?;
        if frame_sequence == 0 || frame_sequence <= worker.last_frame_sequence {
            return Err(RemoteDispatchError::NonMonotonicFrame);
        }
        worker.last_frame_sequence = frame_sequence;
        Ok(worker_id)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum RemoteDispatchError {
    #[error("remote worker limits are invalid")]
    InvalidLimits,
    #[error("remote worker registration is invalid")]
    InvalidRegistration,
    #[error("remote worker registration duplicates an active identity")]
    DuplicateRegistration,
    #[error("active remote worker state exceeds replacement limits")]
    ActiveStateExceedsNewLimits,
    #[error("remote worker session is unknown")]
    UnknownSession,
    #[error("remote worker frame sequence is replayed or unordered")]
    NonMonotonicFrame,
    #[error("remote task capability is invalid")]
    InvalidCapability,
    #[error("no registered worker has the required capability")]
    NoCapableWorker,
    #[error("all compatible remote workers are backpressured")]
    Backpressured,
    #[error(transparent)]
    Credit(#[from] CreditError),
}

#[cfg(test)]
mod tests {
    use super::*;

    fn limits() -> RemoteWorkerLimits {
        RemoteWorkerLimits {
            dispatch: DispatchLimits {
                max_workers: 4,
                max_credits_per_worker: 8,
                max_identifier_bytes: 64,
            },
            max_capabilities_per_worker: 8,
            max_protocol_version_bytes: 16,
        }
    }

    fn registration(worker_id: &str, session_id: &str, credit: u32) -> RemoteWorkerRegistration {
        RemoteWorkerRegistration {
            session_id: session_id.into(),
            frame_sequence: 1,
            worker_id: worker_id.into(),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            protocol_version: "1".into(),
            capabilities: BTreeSet::from(["payment".into()]),
            initial_credit: credit,
        }
    }

    #[test]
    fn zero_credit_backpressures_until_next_ordered_grant() {
        let mut registry = RemoteDispatchRegistry::new(limits()).unwrap();
        registry
            .register(registration("worker-a", "session-a", 0))
            .unwrap();
        assert_eq!(
            registry.reserve_assignment("task-1", &TenantId::new("tenant-a").unwrap(), "payment"),
            Err(RemoteDispatchError::Backpressured)
        );

        registry.grant_credit("session-a", 2, 1).unwrap();
        assert_eq!(
            registry
                .reserve_assignment("task-1", &TenantId::new("tenant-a").unwrap(), "payment")
                .unwrap(),
            "worker-a"
        );
        assert!(registry.invariant_holds());
    }

    #[test]
    fn disconnect_releases_inflight_for_idempotent_redispatch() {
        let mut registry = RemoteDispatchRegistry::new(limits()).unwrap();
        registry
            .register(registration("worker-a", "session-a", 1))
            .unwrap();
        registry
            .reserve_assignment("task-1", &TenantId::new("tenant-a").unwrap(), "payment")
            .unwrap();

        assert_eq!(
            registry.disconnect("session-a").unwrap(),
            vec!["task-1".to_owned()]
        );
        registry
            .register(registration("worker-b", "session-b", 1))
            .unwrap();
        assert_eq!(
            registry
                .reserve_assignment("task-1", &TenantId::new("tenant-a").unwrap(), "payment")
                .unwrap(),
            "worker-b"
        );
        assert!(registry.invariant_holds());
    }

    #[test]
    fn replayed_ack_frame_does_not_release_another_assignment() {
        let mut registry = RemoteDispatchRegistry::new(limits()).unwrap();
        registry
            .register(registration("worker-a", "session-a", 2))
            .unwrap();
        registry
            .reserve_assignment("task-1", &TenantId::new("tenant-a").unwrap(), "payment")
            .unwrap();
        registry
            .reserve_assignment("task-2", &TenantId::new("tenant-a").unwrap(), "payment")
            .unwrap();
        registry
            .acknowledge(
                "session-a",
                2,
                "task-1",
                RemoteTaskAcknowledgement::Completed,
            )
            .unwrap();

        assert_eq!(
            registry.acknowledge(
                "session-a",
                2,
                "task-2",
                RemoteTaskAcknowledgement::Completed,
            ),
            Err(RemoteDispatchError::NonMonotonicFrame)
        );
        assert_eq!(
            registry.grant_credit("session-a", 3, 0),
            Err(RemoteDispatchError::Credit(
                CreditError::CreditBelowInflight
            ))
        );
        assert!(registry.invariant_holds());
    }
}
