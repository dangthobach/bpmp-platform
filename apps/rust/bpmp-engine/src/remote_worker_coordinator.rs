use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex, RwLock};

use bpmp_contracts::engine::v1::{
    EngineToWorker, RemoteTaskAcknowledgement as WireAcknowledgement,
    RemoteTaskAcknowledgementStatus, RemoteTaskAssignment, RemoteWorkerCredit,
    RemoteWorkerHeartbeat, RemoteWorkerRegistered, RemoteWorkerRegistration as WireRegistration,
    WorkerToEngine, engine_to_worker, worker_to_engine,
};
use bpmp_domain_core::{RemoteWorkerPolicy, TenantId, WorkflowValue};
use prost::Message;
use sha2::{Digest, Sha256};
use tokio::sync::mpsc;
use tonic::Status;

use crate::{
    ClockPort, DispatchLimits, RemoteDispatchError, RemoteDispatchRegistry,
    RemoteTaskAcknowledgement, RemoteTaskClaim, RemoteTaskClaimRequest, RemoteTaskCompletionPort,
    RemoteTaskError, RemoteTaskStorePort, RemoteWorkerLimits,
    RemoteWorkerRegistration as DomainRegistration, RemoteWorkerStreamHandlerPort,
    RemoteWorkerStreamLimits, RemoteWorkerTransportError,
};

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct VerifiedRemoteWorker {
    pub tenant_id: TenantId,
    pub worker_id: String,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RemoteAssignmentTokenRequest {
    pub assignment_id: String,
    pub tenant_id: TenantId,
    pub task_id: String,
    pub worker_id: String,
    pub session_id: String,
    pub lease_until_epoch_ms: u64,
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteWorkerVerifierPort: Send + Sync {
    fn verify(
        &self,
        peer_certificate_der: &[u8],
        session_id: &str,
        registration: &WireRegistration,
        now_epoch_ms: u64,
    ) -> Result<VerifiedRemoteWorker, RemoteWorkerTransportError>;
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteAssignmentTokenPort: Send + Sync {
    fn issue(
        &self,
        request: &RemoteAssignmentTokenRequest,
    ) -> Result<Vec<u8>, RemoteWorkerTransportError>;
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteWorkerPolicyActivationPort: Send + Sync {
    fn activate(&self, policy: RemoteWorkerPolicy) -> Result<(), RemoteWorkerTransportError>;
}

#[derive(Clone)]
pub struct RemoteWorkerPolicyHandle {
    value: Arc<RwLock<RemoteWorkerPolicy>>,
}

impl RemoteWorkerPolicyHandle {
    /// Creates a validated hot-reloadable remote-worker policy.
    ///
    /// # Errors
    ///
    /// Rejects invalid dispatch, lease, retry, or payload bounds.
    pub fn new(policy: RemoteWorkerPolicy) -> Result<Self, RemoteWorkerTransportError> {
        policy
            .validate()
            .map_err(|error| RemoteWorkerTransportError::Session(error.to_string()))?;
        Ok(Self {
            value: Arc::new(RwLock::new(policy)),
        })
    }

    /// Replaces the policy at a command/batch safe point.
    ///
    /// # Errors
    ///
    /// Rejects invalid policy values or a poisoned policy lock.
    pub fn replace(&self, policy: RemoteWorkerPolicy) -> Result<(), RemoteWorkerTransportError> {
        policy
            .validate()
            .map_err(|error| RemoteWorkerTransportError::Session(error.to_string()))?;
        *self
            .value
            .write()
            .map_err(|_| RemoteWorkerTransportError::Session("policy lock poisoned".into()))? =
            policy;
        Ok(())
    }

    fn snapshot(&self) -> Result<RemoteWorkerPolicy, RemoteWorkerTransportError> {
        self.value
            .read()
            .map(|value| value.clone())
            .map_err(|_| RemoteWorkerTransportError::Session("policy lock poisoned".into()))
    }
}

struct Session {
    sender: mpsc::Sender<Result<EngineToWorker, Status>>,
    last_heartbeat_epoch_ms: u64,
    next_outbound_sequence: u64,
}

struct ActiveAssignment {
    claim: RemoteTaskClaim,
    task_ref: String,
}

struct CoordinatorState {
    registry: RemoteDispatchRegistry,
    sessions: BTreeMap<String, Session>,
    assignments: BTreeMap<String, ActiveAssignment>,
}

pub struct RemoteWorkerCoordinator<S, C, V, T, K> {
    store: S,
    completion: C,
    verifier: V,
    tokens: T,
    clock: K,
    policy: RemoteWorkerPolicyHandle,
    state: Mutex<CoordinatorState>,
}

impl<S, C, V, T, K> RemoteWorkerCoordinator<S, C, V, T, K>
where
    S: RemoteTaskStorePort,
    C: RemoteTaskCompletionPort,
    V: RemoteWorkerVerifierPort,
    T: RemoteAssignmentTokenPort,
    K: ClockPort,
{
    /// Creates a bounded coordinator over durable storage and explicit ports.
    ///
    /// # Errors
    ///
    /// Rejects an invalid initial policy or dispatch registry limits.
    pub fn new(
        store: S,
        completion: C,
        verifier: V,
        tokens: T,
        clock: K,
        policy: RemoteWorkerPolicyHandle,
    ) -> Result<Self, RemoteWorkerTransportError> {
        let limits = limits_from_policy(&policy.snapshot()?)?;
        Ok(Self {
            store,
            completion,
            verifier,
            tokens,
            clock,
            policy,
            state: Mutex::new(CoordinatorState {
                registry: RemoteDispatchRegistry::new(limits)
                    .map_err(remote_dispatch_transport_error)?,
                sessions: BTreeMap::new(),
                assignments: BTreeMap::new(),
            }),
        })
    }

    pub const fn policy_handle(&self) -> &RemoteWorkerPolicyHandle {
        &self.policy
    }

    /// Dispatches one bounded ready batch to compatible connected workers.
    ///
    /// # Errors
    ///
    /// Returns a typed storage, authentication, lease, or backpressure error.
    pub fn dispatch_once(&self) -> Result<usize, RemoteWorkerTransportError> {
        let now = self.now()?;
        let policy = self.policy.snapshot()?;
        self.apply_limits(&policy)?;
        let tasks = self
            .store
            .ready_tasks(now, policy.dispatch_batch_size as usize)
            .map_err(remote_task_transport_error)?;
        let mut dispatched = 0;
        for task in tasks {
            let task_ref = task_ref(&task.tenant_id, &task.task_id);
            let reservation = {
                let mut state = self.lock_state()?;
                let worker_id = match state.registry.reserve_assignment(
                    &task_ref,
                    &task.tenant_id,
                    &task.task_type,
                ) {
                    Ok(worker_id) => worker_id,
                    Err(
                        RemoteDispatchError::Backpressured | RemoteDispatchError::NoCapableWorker,
                    ) => {
                        continue;
                    }
                    Err(error) => return Err(remote_dispatch_transport_error(error)),
                };
                let session_id = state
                    .registry
                    .session_for_worker(&worker_id)
                    .ok_or_else(|| {
                        RemoteWorkerTransportError::Session(
                            "reserved worker has no active session".into(),
                        )
                    })?
                    .to_owned();
                (worker_id, session_id)
            };
            if self.claim_and_send(
                &task,
                &task_ref,
                &reservation.0,
                &reservation.1,
                now,
                &policy,
            )? {
                dispatched += 1;
            }
        }
        Ok(dispatched)
    }

    /// Reclaims expired durable leases and disconnected heartbeat sessions.
    ///
    /// # Errors
    ///
    /// Returns a typed clock, storage, or policy error.
    pub fn reap_once(&self) -> Result<usize, RemoteWorkerTransportError> {
        let now = self.now()?;
        let policy = self.policy.snapshot()?;
        let expired = self
            .store
            .expired_claims(now, policy.dispatch_batch_size as usize)
            .map_err(remote_task_transport_error)?;
        let mut reclaimed = 0;
        for claim in expired {
            self.retry_or_dead_letter(&claim, now, &policy)?;
            self.remove_assignment(&claim.assignment_id)?;
            reclaimed += 1;
        }
        let timed_out_sessions = {
            let state = self.lock_state()?;
            state
                .sessions
                .iter()
                .filter(|(_, session)| {
                    session
                        .last_heartbeat_epoch_ms
                        .saturating_add(policy.heartbeat_timeout_ms)
                        <= now
                })
                .map(|(session_id, _)| session_id.clone())
                .collect::<Vec<_>>()
        };
        for session_id in timed_out_sessions {
            self.disconnect_session(&session_id, now, &policy);
        }
        Ok(reclaimed)
    }

    fn claim_and_send(
        &self,
        task: &crate::RemoteTask,
        task_ref: &str,
        worker_id: &str,
        session_id: &str,
        now: u64,
        policy: &RemoteWorkerPolicy,
    ) -> Result<bool, RemoteWorkerTransportError> {
        let lease_until = now
            .checked_add(policy.lease_duration_ms)
            .ok_or_else(|| RemoteWorkerTransportError::Session("lease time overflow".into()))?;
        let assignment_id = assignment_id(&task.tenant_id, &task.task_id, session_id);
        let token = self.tokens.issue(&RemoteAssignmentTokenRequest {
            assignment_id: assignment_id.clone(),
            tenant_id: task.tenant_id.clone(),
            task_id: task.task_id.clone(),
            worker_id: worker_id.to_owned(),
            session_id: session_id.to_owned(),
            lease_until_epoch_ms: lease_until,
        })?;
        let request = RemoteTaskClaimRequest {
            tenant_id: task.tenant_id.clone(),
            task_id: task.task_id.clone(),
            assignment_id: assignment_id.clone(),
            worker_id: worker_id.to_owned(),
            session_id: session_id.to_owned(),
            now_epoch_ms: now,
            lease_until_epoch_ms: lease_until,
            assignment_token_digest: Sha256::digest(&token).to_vec(),
        };
        let Some(claim) = self
            .store
            .claim_remote_task(&request)
            .map_err(remote_task_transport_error)?
        else {
            self.cancel_reservation(worker_id, task_ref)?;
            return Ok(false);
        };
        let frame = RemoteTaskAssignment {
            assignment_id: assignment_id.clone(),
            assignment_token: token,
            tenant_id: task.tenant_id.to_string(),
            instance_id: task.instance_id.to_string(),
            workflow_type: task.workflow_type.to_string(),
            workflow_version: task.workflow_version.to_string(),
            node_id: task.node_id.to_string(),
            task_type: task.task_type.clone(),
            activation_event_id: task.activation_event_id.clone(),
            activation_sequence: task.activation_sequence,
            attempt: claim.attempt,
            lease_expires_at_epoch_ms: claim.lease_until_epoch_ms,
            config_version: task.config_version.to_string(),
            policy_version: task.policy_version.to_string(),
            input_payload: Vec::new(),
            input_content_type: "application/octet-stream".into(),
            correlation_id: task.correlation_id.to_string(),
        };
        let send_result = {
            let mut state = self.lock_state()?;
            let (sender, frame_sequence) = outbound_slot(&mut state, session_id)?;
            state.assignments.insert(
                assignment_id.clone(),
                ActiveAssignment {
                    claim: claim.clone(),
                    task_ref: task_ref.to_owned(),
                },
            );
            sender.try_send(Ok(EngineToWorker {
                session_id: session_id.to_owned(),
                frame_sequence,
                frame: Some(engine_to_worker::Frame::Assignment(frame)),
            }))
        };
        if send_result.is_err() {
            self.retry_or_dead_letter(&claim, now, policy)?;
            self.remove_assignment(&assignment_id)?;
            return Err(RemoteWorkerTransportError::Backpressured);
        }
        Ok(true)
    }

    fn handle_acknowledgement(
        &self,
        session_id: &str,
        frame_sequence: u64,
        acknowledgement: &WireAcknowledgement,
    ) -> Result<(), RemoteWorkerTransportError> {
        let now = self.now()?;
        let policy = self.policy.snapshot()?;
        if acknowledgement.encoded_len() > policy.max_output_bytes as usize {
            return Err(RemoteWorkerTransportError::InvalidFrame(
                "remote task acknowledgement exceeds output bound".into(),
            ));
        }
        let active = {
            let state = self.lock_state()?;
            state
                .assignments
                .get(&acknowledgement.assignment_id)
                .map(|active| ActiveAssignment {
                    claim: active.claim.clone(),
                    task_ref: active.task_ref.clone(),
                })
                .ok_or_else(|| {
                    RemoteWorkerTransportError::InvalidFrame(
                        "remote task assignment is unknown".into(),
                    )
                })?
        };
        if active.claim.session_id != session_id
            || Sha256::digest(&acknowledgement.assignment_token).as_slice()
                != active.claim.assignment_token_digest
        {
            return Err(RemoteWorkerTransportError::Unauthenticated);
        }
        let status =
            RemoteTaskAcknowledgementStatus::try_from(acknowledgement.status).map_err(|_| {
                RemoteWorkerTransportError::InvalidFrame(
                    "remote task acknowledgement status is invalid".into(),
                )
            })?;
        if status == RemoteTaskAcknowledgementStatus::Unspecified {
            return Err(RemoteWorkerTransportError::InvalidFrame(
                "remote task acknowledgement status is unspecified".into(),
            ));
        }
        self.lock_state()?
            .registry
            .acknowledge(
                session_id,
                frame_sequence,
                &active.task_ref,
                RemoteTaskAcknowledgement::Accepted,
            )
            .map_err(remote_dispatch_transport_error)?;
        match status {
            RemoteTaskAcknowledgementStatus::Accepted => {}
            RemoteTaskAcknowledgementStatus::Completed => {
                let outputs = workflow_outputs(&acknowledgement.outputs)?;
                let occurred_at = valid_occurrence(acknowledgement.occurred_at_epoch_ms, now)?;
                self.completion
                    .complete(&active.claim, outputs, occurred_at)
                    .map_err(remote_task_transport_error)?;
                self.store
                    .complete_remote_task(&active.claim)
                    .map_err(remote_task_transport_error)?;
                self.finish_assignment(&acknowledgement.assignment_id, &active)?;
            }
            RemoteTaskAcknowledgementStatus::Failed => {
                if acknowledgement.error_code.trim().is_empty() {
                    return Err(RemoteWorkerTransportError::InvalidFrame(
                        "failed remote task requires an error code".into(),
                    ));
                }
                let dead_letter =
                    !acknowledgement.retryable || active.claim.attempt >= policy.max_attempts;
                let retry_at = now.saturating_add(policy.retry_delay_ms);
                self.store
                    .fail_remote_task(&active.claim, retry_at, dead_letter)
                    .map_err(remote_task_transport_error)?;
                self.finish_assignment(&acknowledgement.assignment_id, &active)?;
            }
            RemoteTaskAcknowledgementStatus::Unspecified => {
                return Err(RemoteWorkerTransportError::InvalidFrame(
                    "remote task acknowledgement status is unspecified".into(),
                ));
            }
        }
        self.dispatch_once()?;
        Ok(())
    }

    fn finish_assignment(
        &self,
        assignment_id: &str,
        active: &ActiveAssignment,
    ) -> Result<(), RemoteWorkerTransportError> {
        let mut state = self.lock_state()?;
        state
            .registry
            .cancel_reservation(&active.claim.worker_id, &active.task_ref)
            .map_err(remote_dispatch_transport_error)?;
        state.assignments.remove(assignment_id);
        Ok(())
    }

    fn retry_or_dead_letter(
        &self,
        claim: &RemoteTaskClaim,
        now: u64,
        policy: &RemoteWorkerPolicy,
    ) -> Result<(), RemoteWorkerTransportError> {
        let dead_letter = claim.attempt >= policy.max_attempts;
        self.store
            .fail_remote_task(
                claim,
                now.saturating_add(policy.retry_delay_ms),
                dead_letter,
            )
            .map_err(remote_task_transport_error)?;
        Ok(())
    }

    fn disconnect_session(&self, session_id: &str, now: u64, policy: &RemoteWorkerPolicy) {
        let claims = {
            let Ok(mut state) = self.state.lock() else {
                return;
            };
            state.sessions.remove(session_id);
            let _ = state.registry.disconnect(session_id);
            let assignment_ids = state
                .assignments
                .iter()
                .filter(|(_, active)| active.claim.session_id == session_id)
                .map(|(assignment_id, _)| assignment_id.clone())
                .collect::<Vec<_>>();
            assignment_ids
                .into_iter()
                .filter_map(|assignment_id| {
                    state
                        .assignments
                        .remove(&assignment_id)
                        .map(|active| active.claim)
                })
                .collect::<Vec<_>>()
        };
        for claim in claims {
            let _ = self.retry_or_dead_letter(&claim, now, policy);
        }
    }

    fn remove_assignment(&self, assignment_id: &str) -> Result<(), RemoteWorkerTransportError> {
        let mut state = self.lock_state()?;
        if let Some(active) = state.assignments.remove(assignment_id) {
            let _ = state
                .registry
                .cancel_reservation(&active.claim.worker_id, &active.task_ref);
        }
        Ok(())
    }

    fn cancel_reservation(
        &self,
        worker_id: &str,
        task_ref: &str,
    ) -> Result<(), RemoteWorkerTransportError> {
        self.lock_state()?
            .registry
            .cancel_reservation(worker_id, task_ref)
            .map_err(remote_dispatch_transport_error)
    }

    fn apply_limits(&self, policy: &RemoteWorkerPolicy) -> Result<(), RemoteWorkerTransportError> {
        self.lock_state()?
            .registry
            .replace_limits(limits_from_policy(policy)?)
            .map_err(remote_dispatch_transport_error)
    }

    fn now(&self) -> Result<u64, RemoteWorkerTransportError> {
        self.clock
            .now_epoch_ms()
            .map_err(|error| RemoteWorkerTransportError::Session(error.to_string()))
    }

    fn lock_state(
        &self,
    ) -> Result<std::sync::MutexGuard<'_, CoordinatorState>, RemoteWorkerTransportError> {
        self.state
            .lock()
            .map_err(|_| RemoteWorkerTransportError::Session("coordinator lock poisoned".into()))
    }
}

impl<S, C, V, T, K> RemoteWorkerStreamHandlerPort for RemoteWorkerCoordinator<S, C, V, T, K>
where
    S: RemoteTaskStorePort + 'static,
    C: RemoteTaskCompletionPort + 'static,
    V: RemoteWorkerVerifierPort + 'static,
    T: RemoteAssignmentTokenPort + 'static,
    K: ClockPort + 'static,
{
    fn stream_limits(&self) -> Result<RemoteWorkerStreamLimits, RemoteWorkerTransportError> {
        let policy = self.policy.snapshot()?;
        let channel_capacity = usize::try_from(policy.stream_channel_capacity)
            .map_err(|_| RemoteWorkerTransportError::InvalidConfiguration)?;
        Ok(RemoteWorkerStreamLimits {
            channel_capacity,
            registration_timeout: std::time::Duration::from_millis(policy.heartbeat_timeout_ms),
        })
    }

    fn register(
        &self,
        peer_certificate_der: &[u8],
        frame: WorkerToEngine,
        outbound: mpsc::Sender<Result<EngineToWorker, Status>>,
    ) -> Result<(), RemoteWorkerTransportError> {
        let now = self.now()?;
        let policy = self.policy.snapshot()?;
        self.apply_limits(&policy)?;
        let Some(worker_to_engine::Frame::Registration(registration)) = frame.frame else {
            return Err(RemoteWorkerTransportError::InvalidFrame(
                "registration frame is missing".into(),
            ));
        };
        let verified =
            self.verifier
                .verify(peer_certificate_der, &frame.session_id, &registration, now)?;
        if verified.worker_id != registration.worker_id
            || verified.tenant_id.as_str() != registration.tenant_id
        {
            return Err(RemoteWorkerTransportError::Unauthenticated);
        }
        let capabilities = registration
            .capabilities
            .into_iter()
            .collect::<BTreeSet<_>>();
        let lease_expires = now
            .checked_add(policy.heartbeat_timeout_ms)
            .ok_or_else(|| RemoteWorkerTransportError::Session("heartbeat time overflow".into()))?;
        let mut state = self.lock_state()?;
        state
            .registry
            .register(DomainRegistration {
                session_id: frame.session_id.clone(),
                frame_sequence: frame.frame_sequence,
                worker_id: verified.worker_id.clone(),
                tenant_id: verified.tenant_id,
                protocol_version: registration.protocol_version,
                capabilities,
                initial_credit: registration.initial_credit,
            })
            .map_err(remote_dispatch_transport_error)?;
        state.sessions.insert(
            frame.session_id.clone(),
            Session {
                sender: outbound.clone(),
                last_heartbeat_epoch_ms: now,
                next_outbound_sequence: 2,
            },
        );
        let session_id = frame.session_id;
        if outbound
            .try_send(Ok(EngineToWorker {
                session_id: session_id.clone(),
                frame_sequence: 1,
                frame: Some(engine_to_worker::Frame::Registered(
                    RemoteWorkerRegistered {
                        worker_id: verified.worker_id,
                        lease_expires_at_epoch_ms: lease_expires,
                    },
                )),
            }))
            .is_err()
        {
            state.sessions.remove(&session_id);
            let _ = state.registry.disconnect(&session_id);
            return Err(RemoteWorkerTransportError::Backpressured);
        }
        Ok(())
    }

    fn handle_frame(&self, frame: WorkerToEngine) -> Result<(), RemoteWorkerTransportError> {
        match frame.frame {
            Some(worker_to_engine::Frame::Credit(RemoteWorkerCredit { available_credit })) => {
                self.lock_state()?
                    .registry
                    .grant_credit(&frame.session_id, frame.frame_sequence, available_credit)
                    .map_err(remote_dispatch_transport_error)?;
                self.dispatch_once()?;
                Ok(())
            }
            Some(worker_to_engine::Frame::Acknowledgement(acknowledgement)) => self
                .handle_acknowledgement(&frame.session_id, frame.frame_sequence, &acknowledgement),
            Some(worker_to_engine::Frame::Heartbeat(RemoteWorkerHeartbeat { .. })) => {
                let now = self.now()?;
                let mut state = self.lock_state()?;
                state
                    .registry
                    .heartbeat(&frame.session_id, frame.frame_sequence)
                    .map_err(remote_dispatch_transport_error)?;
                state
                    .sessions
                    .get_mut(&frame.session_id)
                    .ok_or_else(|| {
                        RemoteWorkerTransportError::Session("session is unknown".into())
                    })?
                    .last_heartbeat_epoch_ms = now;
                drop(state);
                self.dispatch_once()?;
                Ok(())
            }
            Some(worker_to_engine::Frame::Registration(_)) => Err(
                RemoteWorkerTransportError::InvalidFrame("duplicate registration frame".into()),
            ),
            None => Err(RemoteWorkerTransportError::InvalidFrame(
                "remote worker frame payload is missing".into(),
            )),
        }
    }

    fn disconnect(&self, session_id: &str) {
        let Ok(now) = self.now() else {
            return;
        };
        let Ok(policy) = self.policy.snapshot() else {
            return;
        };
        self.disconnect_session(session_id, now, &policy);
    }
}

impl<S, C, V, T, K> RemoteWorkerPolicyActivationPort for RemoteWorkerCoordinator<S, C, V, T, K>
where
    S: RemoteTaskStorePort,
    C: RemoteTaskCompletionPort,
    V: RemoteWorkerVerifierPort,
    T: RemoteAssignmentTokenPort,
    K: ClockPort,
{
    fn activate(&self, policy: RemoteWorkerPolicy) -> Result<(), RemoteWorkerTransportError> {
        self.apply_limits(&policy)?;
        self.policy.replace(policy)
    }
}

fn outbound_slot(
    state: &mut CoordinatorState,
    session_id: &str,
) -> Result<(mpsc::Sender<Result<EngineToWorker, Status>>, u64), RemoteWorkerTransportError> {
    let session = state.sessions.get_mut(session_id).ok_or_else(|| {
        RemoteWorkerTransportError::Session("remote worker session disappeared".into())
    })?;
    let sequence = session.next_outbound_sequence;
    session.next_outbound_sequence = sequence
        .checked_add(1)
        .ok_or_else(|| RemoteWorkerTransportError::Session("frame sequence overflow".into()))?;
    Ok((session.sender.clone(), sequence))
}

fn limits_from_policy(
    policy: &RemoteWorkerPolicy,
) -> Result<RemoteWorkerLimits, RemoteWorkerTransportError> {
    policy
        .validate()
        .map_err(|error| RemoteWorkerTransportError::Session(error.to_string()))?;
    Ok(RemoteWorkerLimits {
        dispatch: DispatchLimits {
            max_workers: policy.max_workers,
            max_credits_per_worker: policy.max_credit_per_worker,
            max_identifier_bytes: policy.max_identifier_bytes,
        },
        max_capabilities_per_worker: policy.max_capabilities_per_worker as usize,
        max_protocol_version_bytes: policy.max_protocol_version_bytes as usize,
    })
}

fn task_ref(tenant_id: &TenantId, task_id: &str) -> String {
    format!("{tenant_id}/{task_id}")
}

fn assignment_id(tenant_id: &TenantId, task_id: &str, session_id: &str) -> String {
    let mut hasher = Sha256::new();
    for value in [tenant_id.as_str(), task_id, session_id] {
        hasher.update((value.len() as u64).to_be_bytes());
        hasher.update(value.as_bytes());
    }
    let digest = hasher.finalize();
    let mut value = String::with_capacity(71);
    value.push_str("remote:");
    for byte in digest {
        use std::fmt::Write as _;
        let _ = write!(value, "{byte:02x}");
    }
    value
}

fn workflow_outputs(
    variables: &[bpmp_contracts::engine::v1::WorkflowVariable],
) -> Result<BTreeMap<String, WorkflowValue>, RemoteWorkerTransportError> {
    let mut outputs = BTreeMap::new();
    for variable in variables {
        if variable.name.trim().is_empty() {
            return Err(RemoteWorkerTransportError::InvalidFrame(
                "remote output name is empty".into(),
            ));
        }
        let value = variable.value.as_ref().ok_or_else(|| {
            RemoteWorkerTransportError::InvalidFrame("remote output value is missing".into())
        })?;
        let value = workflow_value(value)?;
        if outputs.insert(variable.name.clone(), value).is_some() {
            return Err(RemoteWorkerTransportError::InvalidFrame(
                "remote output name is duplicated".into(),
            ));
        }
    }
    Ok(outputs)
}

fn workflow_value(
    value: &bpmp_contracts::engine::v1::workflow_variable::Value,
) -> Result<WorkflowValue, RemoteWorkerTransportError> {
    use bpmp_contracts::engine::v1::workflow_variable::Value;
    Ok(match value {
        Value::BooleanValue(value) => WorkflowValue::Boolean(*value),
        Value::IntegerValue(value) => WorkflowValue::Integer(*value),
        Value::StringValue(value) => WorkflowValue::String(value.clone()),
        Value::ListValue(list) => WorkflowValue::List(
            list.items
                .iter()
                .map(|item| {
                    item.value
                        .as_ref()
                        .ok_or_else(|| {
                            RemoteWorkerTransportError::InvalidFrame(
                                "remote list output contains no value".into(),
                            )
                        })
                        .and_then(workflow_item_value)
                })
                .collect::<Result<_, _>>()?,
        ),
    })
}

fn workflow_item_value(
    value: &bpmp_contracts::engine::v1::workflow_value_item::Value,
) -> Result<WorkflowValue, RemoteWorkerTransportError> {
    use bpmp_contracts::engine::v1::workflow_value_item::Value;
    Ok(match value {
        Value::BooleanValue(value) => WorkflowValue::Boolean(*value),
        Value::IntegerValue(value) => WorkflowValue::Integer(*value),
        Value::StringValue(value) => WorkflowValue::String(value.clone()),
        Value::ListValue(list) => WorkflowValue::List(
            list.items
                .iter()
                .map(|item| {
                    item.value
                        .as_ref()
                        .ok_or_else(|| {
                            RemoteWorkerTransportError::InvalidFrame(
                                "remote nested list output contains no value".into(),
                            )
                        })
                        .and_then(workflow_item_value)
                })
                .collect::<Result<_, _>>()?,
        ),
    })
}

fn valid_occurrence(value: u64, now: u64) -> Result<u64, RemoteWorkerTransportError> {
    if value == 0 || value > now {
        Err(RemoteWorkerTransportError::InvalidFrame(
            "remote acknowledgement timestamp is invalid".into(),
        ))
    } else {
        Ok(value)
    }
}

#[allow(clippy::needless_pass_by_value)]
fn remote_dispatch_transport_error(error: RemoteDispatchError) -> RemoteWorkerTransportError {
    RemoteWorkerTransportError::Session(error.to_string())
}

fn remote_task_transport_error(error: RemoteTaskError) -> RemoteWorkerTransportError {
    match error {
        RemoteTaskError::NotLeader { .. } => RemoteWorkerTransportError::Session(error.to_string()),
        other => RemoteWorkerTransportError::Session(other.to_string()),
    }
}
