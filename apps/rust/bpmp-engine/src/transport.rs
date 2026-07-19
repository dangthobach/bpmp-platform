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
    /// Resolves the exact tenant/workflow definition requested by the command.
    ///
    /// # Errors
    ///
    /// Returns an adapter-safe description when the definition is unavailable.
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, String>;
}

pub trait EngineCommandHandlerPort: Send + Sync + 'static {
    /// Validates and executes one versioned engine command envelope.
    ///
    /// # Errors
    ///
    /// Returns [`TransportError`] before mutation on invalid scope or failed execution.
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
            .resolve(
                &scope.tenant_id,
                &scope.workflow_type,
                &scope.workflow_version,
            )
            .map_err(TransportError::Definition)?;
        if definition.tenant_id != scope.tenant_id
            || definition.workflow_type != scope.workflow_type
            || definition.workflow_version != scope.workflow_version
        {
            return Err(TransportError::DefinitionScopeMismatch);
        }
        let command_id = envelope.command_id.clone();
        let request = map_authorized_command(&envelope, &definition, scope)?;
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

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct GrpcTransportConfig {
    pub max_decoding_message_bytes: usize,
    pub max_encoding_message_bytes: usize,
}

impl GrpcTransportConfig {
    /// Creates explicit inbound and outbound gRPC payload bounds.
    ///
    /// # Errors
    ///
    /// Rejects zero-sized limits.
    pub const fn new(
        max_decoding_message_bytes: usize,
        max_encoding_message_bytes: usize,
    ) -> Result<Self, TransportError> {
        if max_decoding_message_bytes == 0 || max_encoding_message_bytes == 0 {
            Err(TransportError::InvalidTransportConfiguration)
        } else {
            Ok(Self {
                max_decoding_message_bytes,
                max_encoding_message_bytes,
            })
        }
    }
}

impl<H> GrpcEngineCommandService<H> {
    pub fn new(handler: H) -> Self {
        Self {
            handler: Arc::new(handler),
        }
    }

    pub fn into_server(self, config: GrpcTransportConfig) -> EngineCommandServiceServer<Self>
    where
        H: EngineCommandHandlerPort,
    {
        EngineCommandServiceServer::new(self)
            .max_decoding_message_size(config.max_decoding_message_bytes)
            .max_encoding_message_size(config.max_encoding_message_bytes)
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
    envelope: &CommandEnvelope,
    definition: &WorkflowDefinition,
    scope: CommandScope,
) -> Result<AuthorizedCommand, TransportError> {
    if envelope.occurred_at_epoch_ms == 0 {
        return Err(TransportError::InvalidField("occurred_at_epoch_ms"));
    }
    let instance_id =
        InstanceId::new(envelope.instance_id.clone()).map_err(invalid("instance_id"))?;
    let command_id = CommandId::new(envelope.command_id.clone()).map_err(invalid("command_id"))?;
    let idempotency_key = IdempotencyKey::new(envelope.idempotency_key.clone())
        .map_err(invalid("idempotency_key"))?;
    let correlation_id =
        CorrelationId::new(envelope.correlation_id.clone()).map_err(invalid("correlation_id"))?;
    let encryption_key_scope = KeyScope::new(envelope.encryption_key_scope.clone())
        .map_err(invalid("encryption_key_scope"))?;
    let command = map_command(
        envelope
            .command
            .as_ref()
            .ok_or(TransportError::MissingCommand)?,
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
        Command::TriggerBoundaryEvent {
            boundary_event_id, ..
        } => (boundary_event_id.as_str(), "TRIGGER_BOUNDARY_EVENT"),
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
    #[error("gRPC transport message limits must be greater than zero")]
    InvalidTransportConfiguration,
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

#[cfg(test)]
mod tests {
    use bpmp_authz_contracts::authorization::v1::{
        ActorProof, ActorProofType, AuthorizationContext, TransitionResource, WorkloadProof,
    };
    use bpmp_contracts::engine::v1::engine_command_service_client::EngineCommandServiceClient;
    use bpmp_contracts::engine::v1::{CompleteUserTask, command_envelope};
    use bpmp_domain_core::{Node, TaskType};
    use tokio::net::TcpListener;
    use tokio::sync::oneshot;
    use tokio_stream::wrappers::TcpListenerStream;
    use tonic::transport::Server;

    use super::*;

    struct ReceiptHandler;

    impl EngineCommandHandlerPort for ReceiptHandler {
        fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError> {
            Ok(CommandReceipt {
                command_id: envelope.command_id,
                committed_sequence: 7,
                duplicate: false,
            })
        }
    }

    #[tokio::test]
    async fn tonic_server_accepts_generated_client_requests() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (shutdown_tx, shutdown_rx) = oneshot::channel();
        let server = tokio::spawn(async move {
            Server::builder()
                .add_service(
                    GrpcEngineCommandService::new(ReceiptHandler)
                        .into_server(GrpcTransportConfig::new(64 * 1024, 64 * 1024).unwrap()),
                )
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async {
                    let _ = shutdown_rx.await;
                })
                .await
        });
        let mut client = EngineCommandServiceClient::connect(format!("http://{address}"))
            .await
            .unwrap();
        let response = client
            .handle_command(valid_envelope())
            .await
            .unwrap()
            .into_inner();
        assert_eq!(response.command_id, "command-1");
        assert_eq!(response.committed_sequence, 7);
        let _ = shutdown_tx.send(());
        server.await.unwrap().unwrap();
    }

    #[test]
    fn wire_scope_mismatch_fails_before_engine_execution() {
        let definition = definition();
        let mut envelope = valid_envelope();
        envelope
            .authorization_context
            .as_mut()
            .unwrap()
            .resource
            .as_mut()
            .unwrap()
            .active_node_id = "other-node".into();
        let scope = parse_scope(&envelope).unwrap();
        assert!(matches!(
            map_authorized_command(&envelope, &definition, scope),
            Err(TransportError::AuthorizationScopeMismatch)
        ));
    }

    fn definition() -> WorkflowDefinition {
        let start = NodeId::new("start").unwrap();
        let review = NodeId::new("review").unwrap();
        let end = NodeId::new("end").unwrap();
        WorkflowDefinition::new(
            TenantId::new("tenant-a").unwrap(),
            WorkflowType::new("approval").unwrap(),
            WorkflowVersion::new("1").unwrap(),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: review.clone(),
                    },
                ),
                (
                    review,
                    Node::UserTask {
                        task_type: TaskType::new("review").unwrap(),
                        assignment_policy_ref: "reviewers".into(),
                        form_key: None,
                        result_variable: "decision".into(),
                        next: end.clone(),
                    },
                ),
                (end, Node::End),
            ],
        )
        .unwrap()
    }

    fn valid_envelope() -> CommandEnvelope {
        CommandEnvelope {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-1".into(),
            command_id: "command-1".into(),
            idempotency_key: "command-1".into(),
            correlation_id: "correlation-1".into(),
            actor_id: "alice".into(),
            workflow_type: "approval".into(),
            workflow_version: "1".into(),
            occurred_at_epoch_ms: 1_000,
            command: Some(command_envelope::Command::CompleteUserTask(
                CompleteUserTask {
                    node_id: "review".into(),
                    decision: "approved".into(),
                },
            )),
            encryption_key_scope: "tenant-a/operational".into(),
            authorization_context: Some(AuthorizationContext {
                tenant_id: "tenant-a".into(),
                command_id: "command-1".into(),
                correlation_id: "correlation-1".into(),
                evaluated_at_epoch_ms: 1_000,
                actor_proof: Some(ActorProof {
                    r#type: ActorProofType::SignedInternalContext.into(),
                    signed_proof: vec![1],
                }),
                workload_proof: Some(WorkloadProof {
                    signed_proof: vec![2],
                }),
                resource: Some(TransitionResource {
                    workflow_type: "approval".into(),
                    workflow_version: "1".into(),
                    instance_id: "instance-1".into(),
                    active_node_id: "review".into(),
                    action: "COMPLETE_USER_TASK".into(),
                    resource_attributes_digest: Vec::new(),
                }),
            }),
        }
    }
}
