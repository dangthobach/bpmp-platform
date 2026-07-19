use std::collections::BTreeMap;
use std::sync::Arc;

use bpmp_contracts::authorization::v1::ActorProofType;
use bpmp_contracts::engine::v1::command_envelope::Command as WireCommand;
use bpmp_contracts::engine::v1::engine_command_service_server::{
    EngineCommandService, EngineCommandServiceServer,
};
use bpmp_contracts::engine::v1::{CommandEnvelope, CommandReceipt};
use bpmp_domain_core::{
    Command, CommandId, CorrelationId, IdempotencyKey, InstanceId, KeyScope, NodeId, TenantId,
    WorkflowDefinition, WorkflowType, WorkflowVersion,
};
use thiserror::Error;
use tonic::{Request, Response, Status};

use crate::{
    ActorProofKind, AuthorizationProviderPort, AuthorizedCommand, ConfigurationProviderPort,
    Engine, EngineError, HandleOutcome, WorkflowStorePort,
};

pub trait CommandDefinitionProviderPort: Send + Sync {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, String>;
}

pub trait EngineCommandHandlerPort: Send + Sync + 'static {
    fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError>;
}

pub struct AuthoritativeCommandHandler<C, S, A, D> {
    engine: Engine<C, S, A>,
    definitions: D,
}

impl<C, S, A, D> AuthoritativeCommandHandler<C, S, A, D> {
    pub const fn new(engine: Engine<C, S, A>, definitions: D) -> Self {
        Self {
            engine,
            definitions,
        }
    }

    pub const fn engine(&self) -> &Engine<C, S, A> {
        &self.engine
    }
}

impl<C, S, A, D> EngineCommandHandlerPort for AuthoritativeCommandHandler<C, S, A, D>
where
    C: ConfigurationProviderPort + 'static,
    S: WorkflowStorePort + 'static,
    A: AuthorizationProviderPort + 'static,
    D: CommandDefinitionProviderPort + 'static,
{
    fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError> {
        let scope = parse_scope(&envelope)?;
        let definition = self
            .definitions
            .resolve(&scope.tenant_id, &scope.workflow_type, &scope.workflow_version)
            .map_err(TransportError::Definition)?;
        if definition.tenant_id != scope.tenant_id
            || definition.workflow_type != scope.workflow_type
            || definition.workflow_version != scope.workflow_version
        {
            return Err(TransportError::DefinitionScopeMismatch);
        }
        let command_id = envelope.command_id.clone();
        let request = map_authorized_command(envelope, &definition, scope)?;
        let outcome = self
            .engine
            .handle(&definition, request)
            .map_err(TransportError::Engine)?;
        let (result, duplicate) = match outcome {
            HandleOutcome::Committed(result) => (result, false),
            HandleOutcome::Duplicate(result) => (result, true),
        };
        Ok(CommandReceipt {
            command_id,
            committed_sequence: result.version,
            duplicate,
        })
    }
}

#[derive(Clone)]
pub struct GrpcEngineCommandService<H> {
    handler: Arc<H>,
}

impl<H> GrpcEngineCommandService<H> {
    pub fn new(handler: H) -> Self {
        Self {
            handler: Arc::new(handler),
        }
    }

    pub fn into_server(self) -> EngineCommandServiceServer<Self>
    where
        H: EngineCommandHandlerPort,
    {
        EngineCommandServiceServer::new(self)
    }
}

#[tonic::async_trait]
impl<H> EngineCommandService for GrpcEngineCommandService<H>
where
    H: EngineCommandHandlerPort,
{
    async fn handle_command(
        &self,
        request: Request<CommandEnvelope>,
    ) -> Result<Response<CommandReceipt>, Status> {
        self.handler
            .handle(request.into_inner())
            .map(Response::new)
            .map_err(Status::from)
    }
}

struct CommandScope {
    tenant_id: TenantId,
    workflow_type: WorkflowType,
    workflow_version: WorkflowVersion,
}

fn parse_scope(envelope: &CommandEnvelope) -> Result<CommandScope, TransportError> {
    Ok(CommandScope {
        tenant_id: TenantId::new(envelope.tenant_id.clone()).map_err(invalid("tenant_id"))?,
        workflow_type: WorkflowType::new(envelope.workflow_type.clone())
            .map_err(invalid("workflow_type"))?,
        workflow_version: WorkflowVersion::new(envelope.workflow_version.clone())
            .map_err(invalid("workflow_version"))?,
    })
}

fn map_authorized_command(
    envelope: CommandEnvelope,
    definition: &WorkflowDefinition,
    scope: CommandScope,
) -> Result<AuthorizedCommand, TransportError> {
    if envelope.occurred_at_epoch_ms == 0 {
        return Err(TransportError::InvalidField("occurred_at_epoch_ms"));
    }
    let instance_id = InstanceId::new(envelope.instance_id.clone()).map_err(invalid("instance_id"))?;
    let command_id = CommandId::new(envelope.command_id.clone()).map_err(invalid("command_id"))?;
    let idempotency_key = IdempotencyKey::new(envelope.idempotency_key.clone())
        .map_err(invalid("idempotency_key"))?;
    let correlation_id = CorrelationId::new(envelope.correlation_id.clone())
        .map_err(invalid("correlation_id"))?;
    let encryption_key_scope = KeyScope::new(envelope.encryption_key_scope.clone())
        .map_err(invalid("encryption_key_scope"))?;
    let command = map_command(
        envelope.command.as_ref().ok_or(TransportError::MissingCommand)?,
        &scope.tenant_id,
        envelope.occurred_at_epoch_ms,
    )?;
    let (active_node_id, action) = command_resource(definition, &command);
    let authorization = envelope
        .authorization_context
        .as_ref()
        .ok_or(TransportError::MissingAuthorizationContext)?;
    let resource = authorization
        .resource
        .as_ref()
        .ok_or(TransportError::MissingAuthorizationResource)?;
    if authorization.tenant_id != envelope.tenant_id
        || authorization.command_id != envelope.command_id
        || authorization.correlation_id != envelope.correlation_id
        || authorization.evaluated_at_epoch_ms != envelope.occurred_at_epoch_ms
        || resource.workflow_type != envelope.workflow_type
        || resource.workflow_version != envelope.workflow_version
        || resource.instance_id != envelope.instance_id
        || resource.active_node_id != active_node_id
        || resource.action != action
    {
        return Err(TransportError::AuthorizationScopeMismatch);
    }
    let actor = authorization
        .actor_proof
        .as_ref()
        .ok_or(TransportError::MissingActorProof)?;
    if actor.signed_proof.is_empty() {
        return Err(TransportError::MissingActorProof);
    }
    let actor_proof_kind = match ActorProofType::try_from(actor.r#type) {
        Ok(ActorProofType::OriginalJwt) => ActorProofKind::OriginalJwt,
        Ok(ActorProofType::SignedInternalContext) => ActorProofKind::SignedInternalContext,
        _ => return Err(TransportError::UnsupportedActorProofType),
    };
    let workload_proof = authorization
        .workload_proof
        .as_ref()
        .filter(|proof| !proof.signed_proof.is_empty())
        .ok_or(TransportError::MissingWorkloadProof)?;

    Ok(AuthorizedCommand {
        tenant_id: scope.tenant_id,
        instance_id,
        command_id,
        idempotency_key,
        correlation_id,
        evaluated_at_epoch_ms: envelope.occurred_at_epoch_ms,
        actor_proof: actor.signed_proof.clone(),
        actor_proof_kind,
        workload_proof: workload_proof.signed_proof.clone(),
        encryption_key_scope,
        variables: BTreeMap::new(),
        command,
    })
}

fn map_command(
    command: &WireCommand,
    tenant_id: &TenantId,
    occurred_at_epoch_ms: u64,
) -> Result<Command, TransportError> {
    Ok(match command {
        WireCommand::StartWorkflow(_) => Command::StartWorkflow {
            tenant_id: tenant_id.clone(),
            occurred_at_epoch_ms,
        },
        WireCommand::CompleteServiceTask(value) => Command::CompleteServiceTask {
            node_id: node_id(&value.node_id)?,
            occurred_at_epoch_ms,
        },
        WireCommand::CompleteUserTask(value) => Command::CompleteUserTask {
            node_id: node_id(&value.node_id)?,
            decision: value.decision.clone(),
            occurred_at_epoch_ms,
        },
        WireCommand::CompleteScriptTask(value) => Command::CompleteScriptTask {
            node_id: node_id(&value.node_id)?,
            occurred_at_epoch_ms,
        },
        WireCommand::CompleteMultiInstanceIteration(value) => {
            Command::CompleteMultiInstanceIteration {
                node_id: node_id(&value.node_id)?,
                iteration: value.iteration,
                occurred_at_epoch_ms,
            }
        }
        WireCommand::TriggerBoundaryEvent(value) => Command::TriggerBoundaryEvent {
            boundary_event_id: node_id(&value.boundary_event_id)?,
            occurred_at_epoch_ms,
        },
    })
}

fn command_resource<'a>(
    definition: &'a WorkflowDefinition,
    command: &'a Command,
) -> (&'a str, &'static str) {
    match command {
        Command::StartWorkflow { .. } => (definition.start_node.as_str(), "START"),
        Command::CompleteServiceTask { node_id, .. } => (node_id.as_str(), "COMPLETE_SERVICE_TASK"),
        Command::CompleteUserTask { node_id, .. } => (node_id.as_str(), "COMPLETE_USER_TASK"),
        Command::CompleteScriptTask { node_id, .. } => (node_id.as_str(), "COMPLETE_SCRIPT_TASK"),
        Command::CompleteMultiInstanceIteration { node_id, .. } => {
            (node_id.as_str(), "COMPLETE_MULTI_INSTANCE_ITERATION")
        }
        Command::TriggerBoundaryEvent { boundary_event_id, .. } => {
            (boundary_event_id.as_str(), "TRIGGER_BOUNDARY_EVENT")
        }
    }
}

fn node_id(value: &str) -> Result<NodeId, TransportError> {
    NodeId::new(value).map_err(invalid("node_id"))
}

fn invalid<E>(field: &'static str) -> impl FnOnce(E) -> TransportError {
    move |_| TransportError::InvalidField(field)
}

#[derive(Debug, Error)]
pub enum TransportError {
    #[error("command field {0} is invalid or empty")]
    InvalidField(&'static str),
    #[error("command payload is missing")]
    MissingCommand,
    #[error("authorization context is missing")]
    MissingAuthorizationContext,
    #[error("authorization resource is missing")]
    MissingAuthorizationResource,
    #[error("actor proof is missing")]
    MissingActorProof,
    #[error("workload proof is missing")]
    MissingWorkloadProof,
    #[error("actor proof type is unsupported")]
    UnsupportedActorProofType,
    #[error("authorization context scope does not match the command envelope")]
    AuthorizationScopeMismatch,
    #[error("workflow definition lookup failed: {0}")]
    Definition(String),
    #[error("resolved workflow definition scope does not match the command")]
    DefinitionScopeMismatch,
    #[error(transparent)]
    Engine(EngineError),
}

impl From<TransportError> for Status {
    fn from(error: TransportError) -> Self {
        match error {
            TransportError::Engine(EngineError::Authorization(_)) => {
                Self::permission_denied("engine authorization denied the command")
            }
            TransportError::Definition(_) => Self::unavailable(error.to_string()),
            TransportError::Engine(_) | TransportError::DefinitionScopeMismatch => {
                Self::failed_precondition(error.to_string())
            }
            _ => Self::invalid_argument(error.to_string()),
        }
    }
}
