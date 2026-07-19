use std::collections::BTreeMap;

use bpmp_domain_core::{
    ActorId, Command, CommandId, ConfigError, ConfigVersion, CorrelationId, DecisionContext,
    DomainError, DomainEvent, IdempotencyKey, InstanceId, InstanceState, KeyScope, PolicyVersion,
    ResolvedConfigSnapshot, TenantId, WorkflowDefinition, WorkflowType, WorkflowValue,
    WorkflowVersion, decide, evolve, rehydrate,
};
use thiserror::Error;

use crate::ports::{
    AuthorizationError, AuthorizationProviderPort, AuthorizationRequest, CommitOutcome,
    CommitRequest, ConfigurationLookup, ConfigurationProviderPort, StoreError, WorkflowStorePort,
};

/// Immutable wire-contract version for `bpmp.engine.v1.EventEnvelope`.
pub const EVENT_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct AuthorizedCommand {
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub command_id: CommandId,
    pub idempotency_key: IdempotencyKey,
    pub correlation_id: CorrelationId,
    pub evaluated_at_epoch_ms: u64,
    pub actor_proof: Vec<u8>,
    pub workload_proof: Vec<u8>,
    pub encryption_key_scope: KeyScope,
    pub variables: BTreeMap<String, WorkflowValue>,
    pub command: Command,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct AuthorizationAudit {
    pub decision_id: String,
    pub tenant_id: TenantId,
    pub actor_id: ActorId,
    pub workload_id: String,
    pub roles: Vec<String>,
    pub action: String,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub instance_id: InstanceId,
    pub active_node_id: String,
    pub policy_version: PolicyVersion,
    pub config_version: ConfigVersion,
    pub bundle_sequence: u64,
    pub revoke_epoch: u64,
    pub occurred_at_epoch_ms: u64,
    pub command_id: CommandId,
    pub correlation_id: CorrelationId,
    pub matched_grant_ids: Vec<String>,
    pub encryption_key_scope: KeyScope,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct EventMetadata {
    pub event_id: String,
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub sequence: u64,
    pub schema_version: u32,
    pub correlation_id: CorrelationId,
    pub causation_command_id: CommandId,
    pub occurred_at_epoch_ms: u64,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
    pub actor_id: ActorId,
    pub encryption_key_scope: KeyScope,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct EventEnvelope {
    pub metadata: EventMetadata,
    pub event: DomainEvent,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct SnapshotEnvelope {
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub state: InstanceState,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
    pub encryption_key_scope: KeyScope,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct CommittedResult {
    pub version: u64,
    pub event_ids: Vec<String>,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum HandleOutcome {
    Committed(CommittedResult),
    Duplicate(CommittedResult),
}

pub struct Engine<C, S, A> {
    configuration: C,
    store: S,
    authorization: A,
}

impl<C, S, A> Engine<C, S, A>
where
    C: ConfigurationProviderPort,
    S: WorkflowStorePort,
    A: AuthorizationProviderPort,
{
    pub const fn new(configuration: C, store: S, authorization: A) -> Self {
        Self {
            configuration,
            store,
            authorization,
        }
    }

    pub const fn store(&self) -> &S {
        &self.store
    }

    pub const fn authorization(&self) -> &A {
        &self.authorization
    }

    /// Re-authorizes a command and commits its resulting events.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError`] when configuration resolution, domain decision,
    /// sequence generation, or the atomic store commit fails.
    pub fn handle(
        &self,
        definition: &WorkflowDefinition,
        request: AuthorizedCommand,
    ) -> Result<HandleOutcome, EngineError> {
        if request.tenant_id != definition.tenant_id {
            return Err(EngineError::DefinitionTenantMismatch);
        }
        let (active_node_id, action) = transition_selector(definition, &request.command);
        let principal = self.authorization.authorize(&AuthorizationRequest {
            tenant_id: &request.tenant_id,
            command_id: &request.command_id,
            evaluated_at_epoch_ms: request.evaluated_at_epoch_ms,
            actor_proof: &request.actor_proof,
            workload_proof: &request.workload_proof,
            workflow_type: &definition.workflow_type,
            workflow_version: &definition.workflow_version,
            active_node_id,
            action,
        })?;
        if let Some(result) = self.store.lookup_idempotency(
            &request.tenant_id,
            &principal.actor_id,
            &request.idempotency_key,
            &request.command_id,
        )? {
            return Ok(HandleOutcome::Duplicate(result));
        }
        let configuration = self.configuration.resolve(&ConfigurationLookup {
            tenant_id: request.tenant_id.clone(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        })?;
        if configuration.policy_version != principal.policy_version {
            return Err(EngineError::PolicyVersionMismatch {
                configured: configuration.policy_version,
                authorized: principal.policy_version,
            });
        }
        let loaded = self.store.load(&request.tenant_id, &request.instance_id)?;
        if loaded.snapshot.as_ref().is_some_and(|snapshot| {
            snapshot.workflow_type != definition.workflow_type
                || snapshot.workflow_version != definition.workflow_version
        }) {
            return Err(EngineError::SnapshotWorkflowMismatch);
        }
        let history: Vec<_> = loaded
            .events
            .iter()
            .map(|envelope| envelope.event.clone())
            .collect();
        let state = rehydrate(
            loaded
                .snapshot
                .as_ref()
                .map(|snapshot| snapshot.state.clone()),
            &history,
        );
        let domain_events = decide(
            definition,
            &state,
            &request.command,
            DecisionContext {
                configuration: &configuration,
                variables: &request.variables,
            },
        )?;
        let envelopes = attach_metadata(
            &request,
            &principal.actor_id,
            &configuration,
            loaded.version,
            domain_events,
        )?;
        let result = committed_result(loaded.version, &envelopes, &configuration)?;
        let snapshot =
            snapshot_at_latest_boundary(definition, &request, &configuration, state, &envelopes);
        let authorization_audit = build_authorization_audit(
            definition,
            &request,
            &configuration,
            &principal,
            active_node_id,
            action,
        );
        let commit = self.store.commit(CommitRequest {
            tenant_id: request.tenant_id,
            instance_id: request.instance_id,
            actor_id: principal.actor_id,
            idempotency_key: request.idempotency_key,
            command_id: request.command_id,
            expected_version: loaded.version,
            events: envelopes,
            snapshot,
            authorization_audit,
            result,
        })?;

        Ok(match commit {
            CommitOutcome::Committed(result) => HandleOutcome::Committed(result),
            CommitOutcome::Duplicate(result) => HandleOutcome::Duplicate(result),
        })
    }
}

fn committed_result(
    current_version: u64,
    events: &[EventEnvelope],
    configuration: &ResolvedConfigSnapshot,
) -> Result<CommittedResult, EngineError> {
    let event_count = u64::try_from(events.len()).map_err(|_| EngineError::SequenceOverflow)?;
    Ok(CommittedResult {
        version: current_version
            .checked_add(event_count)
            .ok_or(EngineError::SequenceOverflow)?,
        event_ids: events
            .iter()
            .map(|event| event.metadata.event_id.clone())
            .collect(),
        config_version: configuration.config_version.clone(),
        policy_version: configuration.policy_version.clone(),
    })
}

fn build_authorization_audit(
    definition: &WorkflowDefinition,
    request: &AuthorizedCommand,
    configuration: &ResolvedConfigSnapshot,
    principal: &crate::AuthorizedPrincipal,
    active_node_id: &str,
    action: &str,
) -> AuthorizationAudit {
    AuthorizationAudit {
        decision_id: format!("allow:{}", request.command_id),
        tenant_id: request.tenant_id.clone(),
        actor_id: principal.actor_id.clone(),
        workload_id: principal.workload_id.clone(),
        roles: principal.roles.clone(),
        action: action.to_owned(),
        workflow_type: definition.workflow_type.clone(),
        workflow_version: definition.workflow_version.clone(),
        instance_id: request.instance_id.clone(),
        active_node_id: active_node_id.to_owned(),
        policy_version: principal.policy_version.clone(),
        config_version: configuration.config_version.clone(),
        bundle_sequence: principal.bundle_sequence,
        revoke_epoch: principal.revoke_epoch,
        occurred_at_epoch_ms: request.evaluated_at_epoch_ms,
        command_id: request.command_id.clone(),
        correlation_id: request.correlation_id.clone(),
        matched_grant_ids: principal.matched_grant_ids.clone(),
        encryption_key_scope: configuration.engine.authorization_audit_key_scope.clone(),
    }
}

fn transition_selector<'a>(
    definition: &'a WorkflowDefinition,
    command: &'a Command,
) -> (&'a str, &'static str) {
    match command {
        Command::StartWorkflow { .. } => (definition.start_node.as_str(), "START"),
        Command::CompleteServiceTask { node_id, .. } => (node_id.as_str(), "COMPLETE_SERVICE_TASK"),
        Command::CompleteMultiInstanceIteration { node_id, .. } => {
            (node_id.as_str(), "COMPLETE_MULTI_INSTANCE_ITERATION")
        }
        Command::TriggerBoundaryEvent {
            boundary_event_id, ..
        } => (boundary_event_id.as_str(), "TRIGGER_BOUNDARY_EVENT"),
    }
}

fn snapshot_at_latest_boundary(
    definition: &WorkflowDefinition,
    request: &AuthorizedCommand,
    configuration: &ResolvedConfigSnapshot,
    mut state: InstanceState,
    events: &[EventEnvelope],
) -> Option<SnapshotEnvelope> {
    let interval = u64::from(configuration.engine.snapshot_interval_events);
    let mut latest = None;
    for envelope in events {
        state = evolve(state, &envelope.event);
        if envelope.metadata.sequence % interval == 0 {
            latest = Some(SnapshotEnvelope {
                tenant_id: request.tenant_id.clone(),
                instance_id: request.instance_id.clone(),
                workflow_type: definition.workflow_type.clone(),
                workflow_version: definition.workflow_version.clone(),
                state: state.clone(),
                config_version: configuration.config_version.clone(),
                policy_version: configuration.policy_version.clone(),
                encryption_key_scope: request.encryption_key_scope.clone(),
            });
        }
    }
    latest
}

fn attach_metadata(
    request: &AuthorizedCommand,
    actor_id: &ActorId,
    configuration: &ResolvedConfigSnapshot,
    current_version: u64,
    events: Vec<DomainEvent>,
) -> Result<Vec<EventEnvelope>, EngineError> {
    events
        .into_iter()
        .enumerate()
        .map(|(ordinal, event)| {
            let offset = u64::try_from(ordinal).map_err(|_| EngineError::SequenceOverflow)?;
            let sequence = current_version
                .checked_add(offset)
                .and_then(|value| value.checked_add(1))
                .ok_or(EngineError::SequenceOverflow)?;
            Ok(EventEnvelope {
                metadata: EventMetadata {
                    event_id: format!("{}:{sequence}", request.command_id),
                    tenant_id: request.tenant_id.clone(),
                    instance_id: request.instance_id.clone(),
                    sequence,
                    schema_version: EVENT_SCHEMA_VERSION,
                    correlation_id: request.correlation_id.clone(),
                    causation_command_id: request.command_id.clone(),
                    occurred_at_epoch_ms: event_time(&event),
                    config_version: configuration.config_version.clone(),
                    policy_version: configuration.policy_version.clone(),
                    actor_id: actor_id.clone(),
                    encryption_key_scope: request.encryption_key_scope.clone(),
                },
                event,
            })
        })
        .collect()
}

const fn event_time(event: &DomainEvent) -> u64 {
    match event {
        DomainEvent::WorkflowStarted {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::ServiceTaskActivated {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::ServiceTaskCompleted {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::DecisionTaskEvaluated {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::GatewaySplitActivated {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::GatewayTokenArrived {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::GatewayJoined {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::BoundaryEventArmed {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::BoundaryEventsDisarmed {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::MultiInstanceStarted {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::MultiInstanceIterationActivated {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::MultiInstanceIterationCompleted {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::MultiInstanceCompleted {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::BoundaryEventTriggered {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::WorkflowBranchCompleted {
            occurred_at_epoch_ms,
            ..
        }
        | DomainEvent::WorkflowCompleted {
            occurred_at_epoch_ms,
        } => *occurred_at_epoch_ms,
    }
}

#[derive(Debug, Error)]
pub enum EngineError {
    #[error(transparent)]
    Authorization(#[from] AuthorizationError),
    #[error(transparent)]
    Configuration(#[from] ConfigError),
    #[error(transparent)]
    Domain(#[from] DomainError),
    #[error(transparent)]
    Store(#[from] StoreError),
    #[error("workflow event sequence overflow")]
    SequenceOverflow,
    #[error("snapshot workflow identity does not match the loaded definition")]
    SnapshotWorkflowMismatch,
    #[error("workflow definition tenant does not match the command tenant")]
    DefinitionTenantMismatch,
    #[error(
        "resolved configuration policy version {configured} differs from authorized policy version {authorized}"
    )]
    PolicyVersionMismatch {
        configured: PolicyVersion,
        authorized: PolicyVersion,
    },
}
