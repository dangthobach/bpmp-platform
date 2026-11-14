use std::collections::BTreeMap;
use std::sync::Arc;

use bpmp_contracts::authorization::v1::ActorProofType;
use bpmp_contracts::engine::v1::command_envelope::Command as WireCommand;
use bpmp_contracts::engine::v1::engine_command_service_server::{
    EngineCommandService, EngineCommandServiceServer,
};
use bpmp_contracts::engine::v1::{CommandEnvelope, CommandReceipt};
use bpmp_domain_core::{
    CaseId, CaseModelId, Command, CommandId, CorrelationId, IdempotencyKey, InstanceId, KeyScope,
    NodeId, TenantId, WorkflowDefinition, WorkflowType, WorkflowValue, WorkflowVersion, rehydrate,
};
use thiserror::Error;
use tonic::{Request, Response, Status};

use crate::{
    ActorProofKind, AuthorizationProviderPort, AuthorizedCommand, ConfigurationProviderPort,
    Engine, EngineError, HandleOutcome, ResolvedBranchScope, RuntimeSafePointGate,
    WorkflowStorePort, configured_branch_id,
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

    /// Returns whether a workflow version is active for new instances.
    /// `None` preserves legacy static deployments without an activation record.
    fn is_active_version(
        &self,
        _tenant_id: &TenantId,
        _workflow_type: &WorkflowType,
        _workflow_version: &WorkflowVersion,
    ) -> Result<Option<bool>, String> {
        Ok(None)
    }
}

pub trait EngineCommandHandlerPort: Send + Sync + 'static {
    /// Validates and executes one versioned engine command envelope.
    ///
    /// # Errors
    ///
    /// Returns [`TransportError`] before mutation on invalid scope or failed execution.
    fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError>;
}

/// Resolves branch ancestry from the organization owner using the command-bound
/// workload proof. Implementations must fail closed on transport or validation errors.
pub trait BranchScopeResolverPort: Send + Sync + 'static {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        branch_id: &str,
        command_id: &CommandId,
        workload_proof: &[u8],
    ) -> Result<ResolvedBranchScope, String>;
}

pub struct SafePointCommandHandler<H> {
    inner: H,
    gate: RuntimeSafePointGate,
}

impl<H> SafePointCommandHandler<H> {
    pub const fn new(inner: H, gate: RuntimeSafePointGate) -> Self {
        Self { inner, gate }
    }
}

impl<H> EngineCommandHandlerPort for SafePointCommandHandler<H>
where
    H: EngineCommandHandlerPort,
{
    fn handle(&self, envelope: CommandEnvelope) -> Result<CommandReceipt, TransportError> {
        let _permit = self
            .gate
            .enter_work()
            .map_err(|error| TransportError::RuntimeSafePoint(error.to_string()))?;
        self.inner.handle(envelope)
    }
}

pub struct AuthoritativeCommandHandler<C, S, A, D> {
    engine: Engine<C, S, A>,
    definitions: D,
    branch_scope_resolver: Option<Arc<dyn BranchScopeResolverPort>>,
}

impl<C, S, A, D> AuthoritativeCommandHandler<C, S, A, D> {
    pub const fn new(engine: Engine<C, S, A>, definitions: D) -> Self {
        Self {
            engine,
            definitions,
            branch_scope_resolver: None,
        }
    }

    pub fn new_with_branch_scope_resolver<R>(
        engine: Engine<C, S, A>,
        definitions: D,
        resolver: R,
    ) -> Self
    where
        R: BranchScopeResolverPort,
    {
        Self {
            engine,
            definitions,
            branch_scope_resolver: Some(Arc::new(resolver)),
        }
    }

    pub const fn engine(&self) -> &Engine<C, S, A> {
        &self.engine
    }
}

impl<T: CommandDefinitionProviderPort + ?Sized> CommandDefinitionProviderPort for Arc<T> {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, String> {
        (**self).resolve(tenant_id, workflow_type, workflow_version)
    }

    fn is_active_version(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<Option<bool>, String> {
        (**self).is_active_version(tenant_id, workflow_type, workflow_version)
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
        if matches!(&envelope.command, Some(WireCommand::StartWorkflow(_)))
            && self
                .definitions
                .is_active_version(
                    &scope.tenant_id,
                    &scope.workflow_type,
                    &scope.workflow_version,
                )
                .map_err(TransportError::Definition)?
                == Some(false)
        {
            return Err(TransportError::InactiveWorkflowVersion);
        }
        let command_id = envelope.command_id.clone();
        let mut request = map_authorized_command(&envelope, &definition, scope)?;
        self.engine
            .verify_command_proofs(&definition, &request)
            .map_err(TransportError::Engine)?;
        let branch_id = if matches!(
            &request.command,
            bpmp_domain_core::Command::StartWorkflow { .. }
        ) {
            configured_branch_id(&definition).map_err(TransportError::Engine)?
        } else {
            let loaded = self
                .engine
                .store()
                .load(&request.tenant_id, &request.instance_id)
                .map_err(|error| TransportError::BranchScope(error.to_string()))?;
            let events = loaded
                .events
                .iter()
                .map(|event| event.event.clone())
                .collect::<Vec<_>>();
            rehydrate(loaded.snapshot.map(|snapshot| snapshot.state), &events).resource_branch_id
        };
        if let Some(branch_id) = branch_id {
            let resolver = self
                .branch_scope_resolver
                .as_ref()
                .ok_or(TransportError::BranchScopeResolverUnavailable)?;
            let resolved = resolver
                .resolve(
                    &request.tenant_id,
                    &branch_id,
                    &request.command_id,
                    &request.workload_proof,
                )
                .map_err(TransportError::BranchScope)?;
            request.resolved_branch_scope = Some(resolved);
        }
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
        let handler = Arc::clone(&self.handler);
        tokio::task::spawn_blocking(move || handler.handle(request.into_inner()))
            .await
            .map_err(|error| Status::internal(format!("command worker failed: {error}")))?
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
    let (command, variables) = map_command(
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
        variables,
        command,
        resolved_branch_scope: None,
    })
}

fn map_command(
    command: &WireCommand,
    tenant_id: &TenantId,
    occurred_at_epoch_ms: u64,
) -> Result<(Command, BTreeMap<String, WorkflowValue>), TransportError> {
    let mut variables = BTreeMap::new();
    let command = match command {
        WireCommand::ActivateCase(value) => Command::ActivateCase {
            case_id: CaseId::new(value.case_id.clone()).map_err(invalid("case_id"))?,
            case_model_id: CaseModelId::new(value.case_model_id.clone())
                .map_err(invalid("case_model_id"))?,
            occurred_at_epoch_ms,
        },
        WireCommand::EvaluateCaseSentries(value) => {
            variables = map_case_facts(&value.facts)?;
            Command::EvaluateCaseSentries {
                case_id: CaseId::new(value.case_id.clone()).map_err(invalid("case_id"))?,
                occurred_at_epoch_ms,
            }
        }
        WireCommand::StartWorkflow(value) => {
            variables = map_workflow_variables(&value.initial_variables)?;
            Command::StartWorkflow {
                tenant_id: tenant_id.clone(),
                occurred_at_epoch_ms,
            }
        }
        WireCommand::CompleteServiceTask(value) => Command::CompleteServiceTask {
            node_id: node_id(&value.node_id)?,
            outputs: map_workflow_variables(&value.outputs)?,
            occurred_at_epoch_ms,
        },
        WireCommand::CompleteUserTask(value) => Command::CompleteUserTask {
            node_id: node_id(&value.node_id)?,
            decision: value.decision.clone(),
            form_version_id: value.form_version_id.clone(),
            outputs: map_workflow_variables(&value.outputs)?,
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
    };
    Ok((command, variables))
}

fn command_resource<'a>(
    definition: &'a WorkflowDefinition,
    command: &'a Command,
) -> (&'a str, &'static str) {
    match command {
        Command::ActivateCase { case_id, .. } => (case_id.as_str(), "ACTIVATE_CASE"),
        Command::EvaluateCaseSentries { case_id, .. } => {
            (case_id.as_str(), "EVALUATE_CASE_SENTRIES")
        }
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

fn workflow_variable_from_wire(
    variable: &bpmp_contracts::engine::v1::WorkflowVariable,
) -> Result<(String, WorkflowValue), TransportError> {
    use bpmp_contracts::engine::v1::workflow_variable::Value;
    if variable.name.trim().is_empty() {
        return Err(TransportError::InvalidField("case_fact.name"));
    }
    let value = match variable
        .value
        .as_ref()
        .ok_or(TransportError::InvalidField("case_fact.value"))?
    {
        Value::BooleanValue(value) => WorkflowValue::Boolean(*value),
        Value::IntegerValue(value) => WorkflowValue::Integer(*value),
        Value::StringValue(value) => WorkflowValue::String(value.clone()),
        Value::ListValue(value) => workflow_value_list_from_wire(value, 0)?,
    };
    Ok((variable.name.clone(), value))
}

fn workflow_value_list_from_wire(
    value: &bpmp_contracts::engine::v1::WorkflowValueList,
    depth: usize,
) -> Result<WorkflowValue, TransportError> {
    use bpmp_contracts::engine::v1::workflow_value_item::Value;
    if depth > 16 || value.items.len() > 256 {
        return Err(TransportError::InvalidField("workflow_variable.list_value"));
    }
    let items = value
        .items
        .iter()
        .map(|item| {
            match item
                .value
                .as_ref()
                .ok_or(TransportError::InvalidField("workflow_variable.list_item"))?
            {
                Value::BooleanValue(value) => Ok(WorkflowValue::Boolean(*value)),
                Value::IntegerValue(value) => Ok(WorkflowValue::Integer(*value)),
                Value::StringValue(value) => Ok(WorkflowValue::String(value.clone())),
                Value::ListValue(value) => workflow_value_list_from_wire(value, depth + 1),
            }
        })
        .collect::<Result<Vec<_>, TransportError>>()?;
    Ok(WorkflowValue::List(items))
}

fn map_case_facts(
    facts: &[bpmp_contracts::engine::v1::WorkflowVariable],
) -> Result<BTreeMap<String, WorkflowValue>, TransportError> {
    let mut mapped = BTreeMap::new();
    for fact in facts {
        let (name, value) = workflow_variable_from_wire(fact)?;
        if mapped.insert(name, value).is_some() {
            return Err(TransportError::DuplicateCaseFact);
        }
    }
    Ok(mapped)
}

fn map_workflow_variables(
    variables: &[bpmp_contracts::engine::v1::WorkflowVariable],
) -> Result<BTreeMap<String, WorkflowValue>, TransportError> {
    if variables.len() > 128 {
        return Err(TransportError::InvalidField("workflow_variables"));
    }
    let mut mapped = BTreeMap::new();
    for variable in variables {
        let (name, value) = workflow_variable_from_wire(variable)?;
        if mapped.insert(name, value).is_some() {
            return Err(TransportError::InvalidField("workflow_variable.name"));
        }
    }
    Ok(mapped)
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
    #[error("CMMN sentry evaluation contains a duplicate fact name")]
    DuplicateCaseFact,
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
    #[error("workflow version is not active for new instances")]
    InactiveWorkflowVersion,
    #[error("branch scope resolver is required for branch-bound workflows")]
    BranchScopeResolverUnavailable,
    #[error("branch scope could not be resolved: {0}")]
    BranchScope(String),
    #[error("gRPC transport message limits must be greater than zero")]
    InvalidTransportConfiguration,
    #[error("runtime safe-point gate failed: {0}")]
    RuntimeSafePoint(String),
    #[error(transparent)]
    Engine(EngineError),
}

impl From<TransportError> for Status {
    fn from(error: TransportError) -> Self {
        match error {
            TransportError::Engine(EngineError::Authorization(source)) => {
                tracing::warn!(
                    error = %source,
                    "authoritative workflow command authorization failed"
                );
                Self::permission_denied("engine authorization denied the command")
            }
            TransportError::Definition(_)
            | TransportError::RuntimeSafePoint(_)
            | TransportError::BranchScope(_) => Self::unavailable(error.to_string()),
            TransportError::Engine(_)
            | TransportError::DefinitionScopeMismatch
            | TransportError::InactiveWorkflowVersion
            | TransportError::BranchScopeResolverUnavailable => {
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
    use bpmp_contracts::engine::v1::{
        CompleteUserTask, EvaluateCaseSentries, WorkflowVariable, command_envelope,
        workflow_variable,
    };
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

    #[test]
    fn cmmn_wire_command_maps_typed_facts_and_rejects_duplicates() {
        let fact = WorkflowVariable {
            name: "documents".into(),
            value: Some(workflow_variable::Value::BooleanValue(true)),
        };
        let command = command_envelope::Command::EvaluateCaseSentries(EvaluateCaseSentries {
            case_id: "case-1".into(),
            facts: vec![fact.clone()],
        });
        let (mapped, facts) =
            map_command(&command, &TenantId::new("tenant-a").unwrap(), 10).unwrap();
        assert!(matches!(mapped, Command::EvaluateCaseSentries { .. }));
        assert_eq!(facts["documents"], WorkflowValue::Boolean(true));

        let duplicate = command_envelope::Command::EvaluateCaseSentries(EvaluateCaseSentries {
            case_id: "case-1".into(),
            facts: vec![fact.clone(), fact],
        });
        assert!(matches!(
            map_command(&duplicate, &TenantId::new("tenant-a").unwrap(), 10),
            Err(TransportError::DuplicateCaseFact)
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
                    outputs: Vec::new(),
                    form_version_id: String::new(),
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
