use std::collections::BTreeMap;

use bpmp_adapter_policy_bundle::VerifiedAuthorizationStore;
use bpmp_authz_contracts::authorization::v1::{
    ActorProof, ActorProofType, AuthorizationContext, AuthorizationPolicyBundle,
    AuthorizationPolicyEffect, AuthorizationPolicyGrant, AuthorizationRevokeEpochUpdate,
    SignedActorContext, SignedWorkloadContext, TransitionResource, WorkloadProof,
};
use bpmp_authz_contracts::{
    AUTHORIZATION_BUNDLE_SCHEMA_VERSION, AUTHORIZATION_PROOF_SCHEMA_VERSION, ActorProofCodec,
    AuthorizationArtifactLimits, AuthorizationBundleCodec, AuthorizationKeyring,
    AuthorizationProofLimits, AuthorizationRevokeCodec, Ed25519Signer, WorkloadProofCodec,
};
use bpmp_contracts::engine::v1::{CommandEnvelope, CompleteUserTask, command_envelope};
use bpmp_domain_core::{
    BooleanExpression, BoundaryEventDefinition, BoundaryRuntimePolicy, BoundaryTrigger,
    CaseDefinition, CaseId, CaseLifecycle, CaseMilestoneDefinition, CaseModelId,
    CaseSentryDefinition, CaseStageDefinition, Command, CommandId, ComparisonOperator, ConfigId,
    ConfigVersion, ConfigurationScope, CorrelationId, DomainEvent, EnginePolicy, GuardExpression,
    IdempotencyKey, InstanceId, KeyScope, LocalWasmPolicy, Node, NodeId, PlanItemId, PolicyVersion,
    ResolvedConfigSnapshot, RetryPolicy, ScopeKind, SentryId, TaskType, TenantId,
    WorkflowDefinition, WorkflowExecutionContracts, WorkflowType, WorkflowValue, WorkflowVersion,
    rehydrate,
};
use bpmp_engine::memory::{InMemoryConfigurationProvider, InMemoryWorkflowStore};
use bpmp_engine::{
    ActorProofKind, AuthoritativeCommandHandler, AuthorizationError, AuthorizationProviderPort,
    AuthorizationRequest, AuthorizedCommand, AuthorizedPrincipal, BoundaryCommandDispatcherPort,
    BoundaryDispatchCredentials, BoundaryDispatchCredentialsPort, BoundaryDispatchRequest,
    BoundaryDispatchSource, BoundaryRuntimeError, CommandDefinitionProviderPort,
    ConfigurationLookup, EmbeddedAuthorizationProvider, Engine, EngineBoundaryCommandDispatcher,
    EngineCommandHandlerPort, EngineError, HandleOutcome, OutboxStorePort,
    WorkflowDefinitionProviderPort, WorkflowStorePort,
};

const KEY_ID: &str = "test-key";

fn artifact_limits() -> AuthorizationArtifactLimits {
    AuthorizationArtifactLimits::new(64 * 1024, 100).unwrap()
}

fn proof_limits() -> AuthorizationProofLimits {
    AuthorizationProofLimits::new(8 * 1024, 32, 64).unwrap()
}

fn keyring(signer: &Ed25519Signer) -> AuthorizationKeyring {
    let mut keyring = AuthorizationKeyring::new();
    keyring
        .insert(KEY_ID, &signer.verifying_key_bytes())
        .unwrap();
    keyring
}

fn authorization() -> EmbeddedAuthorizationProvider {
    let signer = Ed25519Signer::from_bytes(&[11; 32]);
    let policies = VerifiedAuthorizationStore::new(keyring(&signer), artifact_limits());
    let bundle = AuthorizationPolicyBundle {
        schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
        tenant_id: "tenant-a".into(),
        bundle_sequence: 3,
        policy_version: "policy-v3".into(),
        revoke_epoch: 1,
        valid_from_epoch_ms: 1,
        expires_at_epoch_ms: 1_000,
        grants: vec![
            AuthorizationPolicyGrant {
                grant_id: "allow-start".into(),
                actor_ids: vec!["user-1".into()],
                roles: Vec::new(),
                required_capabilities: vec!["workflow.start".into()],
                workflow_type: "order".into(),
                workflow_version: "1".into(),
                active_node_id: "start".into(),
                action: "START".into(),
                effect: AuthorizationPolicyEffect::Allow.into(),
                priority: 10,
            },
            AuthorizationPolicyGrant {
                grant_id: "allow-complete-user".into(),
                actor_ids: vec!["user-1".into()],
                roles: Vec::new(),
                required_capabilities: vec!["workflow.start".into()],
                workflow_type: "order".into(),
                workflow_version: "1".into(),
                active_node_id: "review".into(),
                action: "COMPLETE_USER_TASK".into(),
                effect: AuthorizationPolicyEffect::Allow.into(),
                priority: 10,
            },
            AuthorizationPolicyGrant {
                grant_id: "allow-boundary-trigger".into(),
                actor_ids: vec!["user-1".into()],
                roles: Vec::new(),
                required_capabilities: vec!["boundary.trigger".into()],
                workflow_type: "order".into(),
                workflow_version: "1".into(),
                active_node_id: "cancel-message".into(),
                action: "TRIGGER_BOUNDARY_EVENT".into(),
                effect: AuthorizationPolicyEffect::Allow.into(),
                priority: 10,
            },
        ],
        actor_revoke_epochs: Vec::new(),
        signing_key_id: String::new(),
        content_hash: Vec::new(),
        signature: Vec::new(),
    };
    let bytes = AuthorizationBundleCodec::seal(bundle, KEY_ID, &signer, artifact_limits()).unwrap();
    policies.install_signed_bundle(&bytes).unwrap();
    EmbeddedAuthorizationProvider::new(keyring(&signer), keyring(&signer), proof_limits(), policies)
}

fn definition() -> WorkflowDefinition {
    let start = NodeId::new("start").unwrap();
    let task = NodeId::new("charge-card").unwrap();
    let end = NodeId::new("end").unwrap();
    WorkflowDefinition::new(
        TenantId::new("tenant-a").unwrap(),
        WorkflowType::new("order").unwrap(),
        WorkflowVersion::new("1").unwrap(),
        start.clone(),
        [
            (start, Node::Start { next: task.clone() }),
            (
                task,
                Node::ServiceTask {
                    task_type: TaskType::new("payment").unwrap(),
                    next: end.clone(),
                },
            ),
            (end, Node::End),
        ],
    )
    .unwrap()
}

fn cmmn_definition() -> WorkflowDefinition {
    let start = NodeId::new("start").unwrap();
    let end = NodeId::new("end").unwrap();
    WorkflowDefinition::new_with_execution_contracts(
        TenantId::new("tenant-a").unwrap(),
        WorkflowType::new("order").unwrap(),
        WorkflowVersion::new("1").unwrap(),
        start.clone(),
        [(start, Node::Start { next: end.clone() }), (end, Node::End)],
        std::iter::empty(),
        WorkflowExecutionContracts {
            case_models: vec![CaseDefinition {
                id: CaseModelId::new("claim").unwrap(),
                name: "Claim".into(),
                stages: vec![CaseStageDefinition {
                    id: PlanItemId::new("assessment").unwrap(),
                    name: "Assessment".into(),
                    entry_sentry_ids: vec![SentryId::new("documents-ready").unwrap()],
                    exit_sentry_ids: vec![SentryId::new("approved-sentry").unwrap()],
                }],
                milestones: vec![CaseMilestoneDefinition {
                    id: PlanItemId::new("approved").unwrap(),
                    name: "Approved".into(),
                    entry_sentry_ids: vec![SentryId::new("approved-sentry").unwrap()],
                }],
                sentries: vec![
                    CaseSentryDefinition {
                        id: SentryId::new("documents-ready").unwrap(),
                        condition: BooleanExpression::Comparison(GuardExpression {
                            variable: "documents".into(),
                            operator: ComparisonOperator::Equal,
                            literal: WorkflowValue::Boolean(true),
                        }),
                    },
                    CaseSentryDefinition {
                        id: SentryId::new("approved-sentry").unwrap(),
                        condition: BooleanExpression::Comparison(GuardExpression {
                            variable: "approved".into(),
                            operator: ComparisonOperator::Equal,
                            literal: WorkflowValue::Boolean(true),
                        }),
                    },
                ],
            }],
            ..WorkflowExecutionContracts::default()
        },
    )
    .unwrap()
}

struct AllowCaseAuthorization;

impl AuthorizationProviderPort for AllowCaseAuthorization {
    fn authorize(
        &self,
        _request: &AuthorizationRequest<'_>,
    ) -> Result<AuthorizedPrincipal, AuthorizationError> {
        Ok(AuthorizedPrincipal {
            actor_id: bpmp_domain_core::ActorId::new("case-worker").unwrap(),
            roles: vec!["case-manager".into()],
            workload_id: "case-api".into(),
            policy_version: PolicyVersion::new("policy-v3").unwrap(),
            bundle_sequence: 1,
            revoke_epoch: 0,
            matched_grant_ids: vec!["allow-case-lifecycle".into()],
        })
    }
}

fn case_request(command_id: &str, idempotency_key: &str, command: Command) -> AuthorizedCommand {
    AuthorizedCommand {
        tenant_id: TenantId::new("tenant-a").unwrap(),
        instance_id: InstanceId::new("case-stream-1").unwrap(),
        command_id: CommandId::new(command_id).unwrap(),
        idempotency_key: IdempotencyKey::new(idempotency_key).unwrap(),
        correlation_id: CorrelationId::new("case-correlation-1").unwrap(),
        evaluated_at_epoch_ms: 42,
        actor_proof: vec![1],
        actor_proof_kind: ActorProofKind::SignedInternalContext,
        workload_proof: vec![2],
        encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
        variables: BTreeMap::new(),
        command,
    }
}

fn boundary_definition() -> WorkflowDefinition {
    let start = NodeId::new("start").unwrap();
    let task = NodeId::new("charge-card").unwrap();
    let normal_end = NodeId::new("end").unwrap();
    let recovery = NodeId::new("cancel-order").unwrap();
    let recovery_end = NodeId::new("cancelled").unwrap();
    WorkflowDefinition::new_with_execution_contracts(
        TenantId::new("tenant-a").unwrap(),
        WorkflowType::new("order").unwrap(),
        WorkflowVersion::new("1").unwrap(),
        start.clone(),
        [
            (start, Node::Start { next: task.clone() }),
            (
                task.clone(),
                Node::ServiceTask {
                    task_type: TaskType::new("payment").unwrap(),
                    next: normal_end.clone(),
                },
            ),
            (normal_end, Node::End),
            (
                recovery.clone(),
                Node::ServiceTask {
                    task_type: TaskType::new("cancel-order").unwrap(),
                    next: recovery_end.clone(),
                },
            ),
            (recovery_end, Node::End),
        ],
        std::iter::empty(),
        WorkflowExecutionContracts {
            boundary_events: vec![(
                task,
                BoundaryEventDefinition {
                    id: NodeId::new("cancel-message").unwrap(),
                    cancel_activity: true,
                    target: recovery,
                    trigger: BoundaryTrigger::Message {
                        message_ref: "order.cancelled".into(),
                    },
                },
            )],
            ..WorkflowExecutionContracts::default()
        },
    )
    .unwrap()
}

fn user_boundary_definition() -> WorkflowDefinition {
    let start = NodeId::new("start").unwrap();
    let review = NodeId::new("review").unwrap();
    let normal_end = NodeId::new("end").unwrap();
    let recovery = NodeId::new("cancel-order").unwrap();
    let recovery_end = NodeId::new("cancelled").unwrap();
    WorkflowDefinition::new_with_execution_contracts(
        TenantId::new("tenant-a").unwrap(),
        WorkflowType::new("order").unwrap(),
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
                review.clone(),
                Node::UserTask {
                    task_type: TaskType::new("review").unwrap(),
                    assignment_policy_ref: "reviewers".into(),
                    form_key: Some("review-form".into()),
                    result_variable: "decision".into(),
                    next: normal_end.clone(),
                },
            ),
            (normal_end, Node::End),
            (
                recovery.clone(),
                Node::ServiceTask {
                    task_type: TaskType::new("cancel-order").unwrap(),
                    next: recovery_end.clone(),
                },
            ),
            (recovery_end, Node::End),
        ],
        std::iter::empty(),
        WorkflowExecutionContracts {
            boundary_events: vec![(
                review,
                BoundaryEventDefinition {
                    id: NodeId::new("cancel-message").unwrap(),
                    cancel_activity: true,
                    target: recovery,
                    trigger: BoundaryTrigger::Message {
                        message_ref: "order.cancelled".into(),
                    },
                },
            )],
            ..WorkflowExecutionContracts::default()
        },
    )
    .unwrap()
}

fn user_task_definition() -> WorkflowDefinition {
    let start = NodeId::new("start").unwrap();
    let review = NodeId::new("review").unwrap();
    let end = NodeId::new("end").unwrap();
    WorkflowDefinition::new(
        TenantId::new("tenant-a").unwrap(),
        WorkflowType::new("order").unwrap(),
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
                    task_type: TaskType::new("approval").unwrap(),
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

struct StaticDefinitionProvider(WorkflowDefinition);

impl WorkflowDefinitionProviderPort for StaticDefinitionProvider {
    fn resolve(
        &self,
        _tenant_id: &TenantId,
        _workflow_type: &WorkflowType,
        _workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, BoundaryRuntimeError> {
        Ok(self.0.clone())
    }
}

impl CommandDefinitionProviderPort for StaticDefinitionProvider {
    fn resolve(
        &self,
        _tenant_id: &TenantId,
        _workflow_type: &WorkflowType,
        _workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, String> {
        Ok(self.0.clone())
    }
}

struct SignedBoundaryCredentials;

impl BoundaryDispatchCredentialsPort for SignedBoundaryCredentials {
    fn resolve(
        &self,
        request: &BoundaryDispatchRequest,
    ) -> Result<BoundaryDispatchCredentials, BoundaryRuntimeError> {
        if request.authorization_context_ref.as_deref() != Some("auth-context/message-1") {
            return Err(BoundaryRuntimeError::Dispatch(
                "unknown authorization context".into(),
            ));
        }
        let signer = Ed25519Signer::from_bytes(&[11; 32]);
        let actor_proof = ActorProofCodec::seal(
            SignedActorContext {
                schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
                tenant_id: request.tenant_id.to_string(),
                actor_id: "user-1".into(),
                roles: vec!["operator".into()],
                capabilities: vec!["boundary.trigger".into()],
                revoke_epoch: 1,
                issued_at_epoch_ms: 1,
                expires_at_epoch_ms: 100,
                audience_workload_id: "boundary-runtime".into(),
                command_id: request.command_id.to_string(),
                signing_key_id: String::new(),
                content_hash: Vec::new(),
                signature: Vec::new(),
            },
            KEY_ID,
            &signer,
            proof_limits(),
        )
        .map_err(|error| BoundaryRuntimeError::Dispatch(error.to_string()))?;
        let workload_proof = WorkloadProofCodec::seal(
            SignedWorkloadContext {
                schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
                tenant_id: request.tenant_id.to_string(),
                workload_id: "boundary-runtime".into(),
                command_id: request.command_id.to_string(),
                issued_at_epoch_ms: 1,
                expires_at_epoch_ms: 100,
                signing_key_id: String::new(),
                content_hash: Vec::new(),
                signature: Vec::new(),
            },
            KEY_ID,
            &signer,
            proof_limits(),
        )
        .map_err(|error| BoundaryRuntimeError::Dispatch(error.to_string()))?;
        Ok(BoundaryDispatchCredentials {
            actor_proof,
            workload_proof,
            encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
        })
    }
}

fn configuration(snapshot_interval_events: u32) -> ResolvedConfigSnapshot {
    ResolvedConfigSnapshot::new(
        ConfigId::new("engine").unwrap(),
        ConfigVersion::new("config-v7").unwrap(),
        PolicyVersion::new("policy-v3").unwrap(),
        1,
        vec![ConfigurationScope::new(ScopeKind::Platform, "default").unwrap()],
        [9; 32],
        EnginePolicy {
            snapshot_interval_events,
            max_events_per_decision: 8,
            max_multi_instance_cardinality: 10_000,
            default_multi_instance_parallelism: 32,
            boundary_runtime: BoundaryRuntimePolicy {
                projection_batch_size: 128,
                dispatch_batch_size: 32,
                max_dispatch_attempts: 5,
                retry_delay_ms: 1_000,
                lease_duration_ms: 30_000,
                max_timer_horizon_ms: 365 * 24 * 60 * 60 * 1_000,
                max_expression_bytes: 1_024,
                worker_id: "test-boundary-worker".into(),
                max_signal_id_bytes: 256,
                max_reference_bytes: 1_024,
                max_subscriptions_per_instance: 256,
            },
            command_timeout_ms: 1_000,
            optimistic_conflict_retry: RetryPolicy {
                max_attempts: 3,
                initial_backoff_ms: 10,
                max_backoff_ms: 100,
                multiplier_millis: 2_000,
            },
            local_wasm: local_wasm_policy(),
            event_payload_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            authorization_audit_key_scope: KeyScope::new("tenant-a/compliance-audit").unwrap(),
        },
    )
    .unwrap()
}

fn local_wasm_policy() -> LocalWasmPolicy {
    LocalWasmPolicy {
        max_module_bytes: 64 * 1024,
        max_input_bytes: 32 * 1024,
        max_output_bytes: 32 * 1024,
        max_memory_bytes: 2 * 64 * 1024,
        max_wasm_stack_bytes: 512 * 1024,
        max_table_elements: 1024,
        max_instances: 1,
        max_tables: 1,
        max_memories: 1,
        fuel: 100_000,
    }
}

fn start_request() -> AuthorizedCommand {
    let signer = Ed25519Signer::from_bytes(&[11; 32]);
    let actor_proof = ActorProofCodec::seal(
        SignedActorContext {
            schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            actor_id: "user-1".into(),
            roles: vec!["operator".into()],
            capabilities: vec!["workflow.start".into()],
            revoke_epoch: 1,
            issued_at_epoch_ms: 1,
            expires_at_epoch_ms: 100,
            audience_workload_id: "api-gateway".into(),
            command_id: "command-1".into(),
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        },
        KEY_ID,
        &signer,
        proof_limits(),
    )
    .unwrap();
    let workload_proof = WorkloadProofCodec::seal(
        SignedWorkloadContext {
            schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            workload_id: "api-gateway".into(),
            command_id: "command-1".into(),
            issued_at_epoch_ms: 1,
            expires_at_epoch_ms: 100,
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        },
        KEY_ID,
        &signer,
        proof_limits(),
    )
    .unwrap();
    AuthorizedCommand {
        tenant_id: TenantId::new("tenant-a").unwrap(),
        instance_id: InstanceId::new("instance-1").unwrap(),
        command_id: CommandId::new("command-1").unwrap(),
        idempotency_key: IdempotencyKey::new("start-order-1").unwrap(),
        correlation_id: CorrelationId::new("trace-1").unwrap(),
        evaluated_at_epoch_ms: 42,
        actor_proof,
        actor_proof_kind: ActorProofKind::SignedInternalContext,
        workload_proof,
        encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
        variables: BTreeMap::default(),
        command: Command::StartWorkflow {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            occurred_at_epoch_ms: 42,
        },
    }
}

#[test]
fn cmmn_sentry_evaluation_commits_through_authoritative_engine_lifecycle() {
    let definition = cmmn_definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(
        provider,
        InMemoryWorkflowStore::default(),
        AllowCaseAuthorization,
    );
    let case_id = CaseId::new("case-1").unwrap();
    let activate = case_request(
        "case-command-1",
        "activate-case-1",
        Command::ActivateCase {
            case_id: case_id.clone(),
            case_model_id: CaseModelId::new("claim").unwrap(),
            occurred_at_epoch_ms: 42,
        },
    );
    assert!(matches!(
        engine.handle(&definition, activate).unwrap(),
        HandleOutcome::Committed(_)
    ));

    let mut evaluate = case_request(
        "case-command-2",
        "evaluate-case-1",
        Command::EvaluateCaseSentries {
            case_id: case_id.clone(),
            occurred_at_epoch_ms: 43,
        },
    );
    evaluate.variables = BTreeMap::from([
        ("approved".into(), WorkflowValue::Boolean(true)),
        ("documents".into(), WorkflowValue::Boolean(true)),
    ]);
    let committed = engine.handle(&definition, evaluate.clone()).unwrap();
    assert!(matches!(committed, HandleOutcome::Committed(_)));
    assert!(matches!(
        engine.handle(&definition, evaluate).unwrap(),
        HandleOutcome::Duplicate(_)
    ));

    let loaded = engine
        .store()
        .load(
            &TenantId::new("tenant-a").unwrap(),
            &InstanceId::new("case-stream-1").unwrap(),
        )
        .unwrap();
    assert!(loaded.events.iter().any(|event| {
        matches!(
            event.event,
            DomainEvent::CaseSentrySatisfied { ref sentry_id, .. }
                if sentry_id.as_str() == "documents-ready"
        )
    }));
    assert!(matches!(
        loaded.events.last().map(|event| &event.event),
        Some(DomainEvent::CaseCompleted { .. })
    ));
    let replayed = rehydrate(
        loaded.snapshot.map(|snapshot| snapshot.state),
        &loaded
            .events
            .iter()
            .map(|event| event.event.clone())
            .collect::<Vec<_>>(),
    );
    assert_eq!(
        replayed.active_cases[&case_id].lifecycle,
        CaseLifecycle::Completed
    );
    assert_eq!(
        engine.store().read_after(0, 32).unwrap().len(),
        usize::try_from(loaded.version).unwrap()
    );
    assert_eq!(engine.store().authorization_audit_count().unwrap(), 2);
}

#[test]
fn commits_configuration_metadata_and_returns_duplicate_result() {
    let definition = definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());

    let first = engine.handle(&definition, start_request()).unwrap();
    let duplicate = engine.handle(&definition, start_request()).unwrap();

    let HandleOutcome::Committed(committed) = first else {
        panic!("first command must commit");
    };
    assert_eq!(
        duplicate,
        HandleOutcome::Duplicate(committed.clone()),
        "retry must return the original result without deciding again"
    );
    assert_eq!(committed.version, 2);
    assert_eq!(committed.config_version.as_str(), "config-v7");
    assert_eq!(committed.policy_version.as_str(), "policy-v3");

    let loaded = engine
        .store()
        .load(
            &TenantId::new("tenant-a").unwrap(),
            &InstanceId::new("instance-1").unwrap(),
        )
        .unwrap();
    assert_eq!(loaded.events.len(), 2);
    assert!(loaded.events.iter().all(|event| {
        event.metadata.schema_version == 1
            && event.metadata.config_version.as_str() == "config-v7"
            && event.metadata.policy_version.as_str() == "policy-v3"
    }));
    let audit = engine
        .store()
        .authorization_audit(
            &TenantId::new("tenant-a").unwrap(),
            &CommandId::new("command-1").unwrap(),
        )
        .unwrap()
        .expect("committed transition must have an authorization audit");
    assert_eq!(audit.actor_id.as_str(), "user-1");
    assert_eq!(audit.roles, ["operator"]);
    assert_eq!(audit.workload_id, "api-gateway");
    assert_eq!(audit.policy_version.as_str(), "policy-v3");
    assert_eq!(audit.config_version.as_str(), "config-v7");
    assert_eq!(
        audit.encryption_key_scope.as_str(),
        "tenant-a/compliance-audit"
    );
    assert_eq!(engine.store().authorization_audit_count().unwrap(), 1);
    let outbox = engine.store().read_after(0, 10).unwrap();
    assert_eq!(outbox.len(), 2);
    assert_eq!(outbox[0].cursor, 1);
    assert_eq!(outbox[1].cursor, 2);
    assert_eq!(outbox[0].event_id, committed.event_ids[0]);
    assert_eq!(outbox[1].event_id, committed.event_ids[1]);
}

#[test]
fn rejects_caller_selected_payload_key_scope_before_loading_or_committing() {
    let definition = definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());
    let mut request = start_request();
    request.encryption_key_scope = KeyScope::new("tenant-b/operational").unwrap();

    assert!(matches!(
        engine.handle(&definition, request),
        Err(EngineError::EncryptionKeyScopeMismatch)
    ));
    assert_eq!(engine.store().read_after(0, 1).unwrap(), Vec::new());
}

#[test]
fn creates_snapshot_at_configured_event_boundary() {
    let definition = definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(2),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());

    engine.handle(&definition, start_request()).unwrap();
    let loaded = engine
        .store()
        .load(
            &TenantId::new("tenant-a").unwrap(),
            &InstanceId::new("instance-1").unwrap(),
        )
        .unwrap();

    let snapshot = loaded.snapshot.expect("sequence two must be snapshotted");
    assert_eq!(snapshot.state.sequence, 2);
    assert!(loaded.events.is_empty());
    assert_eq!(loaded.version, 2);
}

#[test]
fn retry_is_reauthorized_before_idempotency_lookup() {
    let definition = definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());
    engine.handle(&definition, start_request()).unwrap();

    let signer = Ed25519Signer::from_bytes(&[11; 32]);
    let revoke = AuthorizationRevokeEpochUpdate {
        schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
        tenant_id: "tenant-a".into(),
        actor_id: "user-1".into(),
        revoke_epoch: 2,
        bundle_sequence: 3,
        issued_at_epoch_ms: 43,
        signing_key_id: String::new(),
        content_hash: Vec::new(),
        signature: Vec::new(),
    };
    let signed_revoke =
        AuthorizationRevokeCodec::seal(revoke, KEY_ID, &signer, artifact_limits()).unwrap();
    engine
        .authorization()
        .policies()
        .apply_signed_revoke_update(&signed_revoke)
        .unwrap();

    let error = engine.handle(&definition, start_request()).unwrap_err();
    assert!(error.to_string().contains("ACTOR_PROOF_REVOKED"));
    let loaded = engine
        .store()
        .load(
            &TenantId::new("tenant-a").unwrap(),
            &InstanceId::new("instance-1").unwrap(),
        )
        .unwrap();
    assert_eq!(loaded.version, 2, "denied retry must not mutate state");
}

#[test]
fn valid_workload_cannot_replace_missing_actor_proof() {
    let definition = definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());
    let mut request = start_request();
    request.actor_proof.clear();

    let error = engine.handle(&definition, request).unwrap_err();
    assert!(error.to_string().contains("actor proof is invalid"));
    let loaded = engine
        .store()
        .load(
            &TenantId::new("tenant-a").unwrap(),
            &InstanceId::new("instance-1").unwrap(),
        )
        .unwrap();
    assert_eq!(loaded.version, 0, "identity denial must not mutate state");
}

#[test]
fn boundary_dispatcher_reauthorizes_and_commits_idempotently_through_engine() {
    let definition = boundary_definition();
    let tenant_id = TenantId::new("tenant-a").unwrap();
    let instance_id = InstanceId::new("instance-1").unwrap();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: tenant_id.clone(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());
    engine.handle(&definition, start_request()).unwrap();
    let dispatcher = EngineBoundaryCommandDispatcher::new(
        engine,
        StaticDefinitionProvider(definition.clone()),
        SignedBoundaryCredentials,
    );
    let request = BoundaryDispatchRequest {
        tenant_id: tenant_id.clone(),
        instance_id: instance_id.clone(),
        command_id: CommandId::new("message-1:boundary:cancel-message").unwrap(),
        idempotency_key: IdempotencyKey::new("message-1:boundary:cancel-message").unwrap(),
        correlation_id: CorrelationId::new("message-1:boundary:cancel-message").unwrap(),
        command: Command::TriggerBoundaryEvent {
            boundary_event_id: NodeId::new("cancel-message").unwrap(),
            occurred_at_epoch_ms: 50,
        },
        source: BoundaryDispatchSource::Message,
        occurred_at_epoch_ms: 50,
        workflow_type: definition.workflow_type.clone(),
        workflow_version: definition.workflow_version.clone(),
        authorization_context_ref: Some("auth-context/message-1".into()),
    };

    dispatcher.dispatch(&request).unwrap();
    dispatcher.dispatch(&request).unwrap();

    let loaded = dispatcher
        .engine()
        .store()
        .load(&tenant_id, &instance_id)
        .unwrap();
    assert_eq!(
        loaded
            .events
            .iter()
            .filter(|event| matches!(event.event, DomainEvent::BoundaryEventTriggered { .. }))
            .count(),
        1,
        "the deterministic command identity must suppress retry duplicates"
    );
    let audit = dispatcher
        .engine()
        .store()
        .authorization_audit(&tenant_id, &request.command_id)
        .unwrap()
        .expect("boundary dispatch must commit an authorization audit");
    assert_eq!(audit.actor_id.as_str(), "user-1");
    assert_eq!(audit.workload_id, "boundary-runtime");
    assert_eq!(audit.action, "TRIGGER_BOUNDARY_EVENT");
}

#[test]
fn interrupting_user_boundary_commits_cancellation_to_event_log_and_outbox() {
    let definition = user_boundary_definition();
    let tenant_id = TenantId::new("tenant-a").unwrap();
    let instance_id = InstanceId::new("instance-1").unwrap();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: tenant_id.clone(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());
    engine.handle(&definition, start_request()).unwrap();
    let dispatcher = EngineBoundaryCommandDispatcher::new(
        engine,
        StaticDefinitionProvider(definition.clone()),
        SignedBoundaryCredentials,
    );
    let request = BoundaryDispatchRequest {
        tenant_id: tenant_id.clone(),
        instance_id: instance_id.clone(),
        command_id: CommandId::new("message-1:boundary:cancel-message").unwrap(),
        idempotency_key: IdempotencyKey::new("message-1:boundary:cancel-message").unwrap(),
        correlation_id: CorrelationId::new("message-1:boundary:cancel-message").unwrap(),
        command: Command::TriggerBoundaryEvent {
            boundary_event_id: NodeId::new("cancel-message").unwrap(),
            occurred_at_epoch_ms: 50,
        },
        source: BoundaryDispatchSource::Message,
        occurred_at_epoch_ms: 50,
        workflow_type: definition.workflow_type.clone(),
        workflow_version: definition.workflow_version.clone(),
        authorization_context_ref: Some("auth-context/message-1".into()),
    };

    dispatcher.dispatch(&request).unwrap();
    dispatcher.dispatch(&request).unwrap();
    let loaded = dispatcher
        .engine()
        .store()
        .load(&tenant_id, &instance_id)
        .unwrap();
    let cancellations = loaded
        .events
        .iter()
        .filter(|event| matches!(event.event, DomainEvent::UserTaskCancelled { .. }))
        .count();
    assert_eq!(cancellations, 1);
    let outbox = dispatcher.engine().store().read_after(0, 32).unwrap();
    assert_eq!(
        outbox
            .iter()
            .filter(|record| {
                bpmp_engine::EventCodec::decode(&record.payload)
                    .is_ok_and(|event| matches!(event.event, DomainEvent::UserTaskCancelled { .. }))
            })
            .count(),
        1
    );
}

#[test]
fn wire_handler_reauthorizes_commits_and_returns_duplicate_receipt() {
    let definition = user_task_definition();
    let mut provider = InMemoryConfigurationProvider::default();
    provider.insert(
        ConfigurationLookup {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: definition.workflow_type.clone(),
            workflow_version: definition.workflow_version.clone(),
        },
        configuration(100),
    );
    let engine = Engine::new(provider, InMemoryWorkflowStore::default(), authorization());
    engine.handle(&definition, start_request()).unwrap();
    let handler = AuthoritativeCommandHandler::new(engine, StaticDefinitionProvider(definition));
    let envelope = complete_user_envelope();
    let first = handler.handle(envelope.clone()).unwrap();
    let duplicate = handler.handle(envelope).unwrap();
    assert_eq!(first.command_id, "command-2");
    assert_eq!(first.committed_sequence, 4);
    assert!(!first.duplicate);
    assert!(duplicate.duplicate);
    assert_eq!(duplicate.committed_sequence, first.committed_sequence);
}

fn complete_user_envelope() -> CommandEnvelope {
    let signer = Ed25519Signer::from_bytes(&[11; 32]);
    let actor_proof = ActorProofCodec::seal(
        SignedActorContext {
            schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            actor_id: "user-1".into(),
            roles: vec!["operator".into()],
            capabilities: vec!["workflow.start".into()],
            revoke_epoch: 1,
            issued_at_epoch_ms: 1,
            expires_at_epoch_ms: 100,
            audience_workload_id: "human-runtime".into(),
            command_id: "command-2".into(),
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        },
        KEY_ID,
        &signer,
        proof_limits(),
    )
    .unwrap();
    let workload_proof = WorkloadProofCodec::seal(
        SignedWorkloadContext {
            schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            workload_id: "human-runtime".into(),
            command_id: "command-2".into(),
            issued_at_epoch_ms: 1,
            expires_at_epoch_ms: 100,
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        },
        KEY_ID,
        &signer,
        proof_limits(),
    )
    .unwrap();
    CommandEnvelope {
        tenant_id: "tenant-a".into(),
        instance_id: "instance-1".into(),
        command_id: "command-2".into(),
        idempotency_key: "complete-review-1".into(),
        correlation_id: "trace-2".into(),
        actor_id: "user-1".into(),
        workflow_type: "order".into(),
        workflow_version: "1".into(),
        occurred_at_epoch_ms: 50,
        command: Some(command_envelope::Command::CompleteUserTask(
            CompleteUserTask {
                node_id: "review".into(),
                decision: "approved".into(),
            },
        )),
        encryption_key_scope: "tenant-a/operational".into(),
        authorization_context: Some(AuthorizationContext {
            tenant_id: "tenant-a".into(),
            command_id: "command-2".into(),
            correlation_id: "trace-2".into(),
            evaluated_at_epoch_ms: 50,
            actor_proof: Some(ActorProof {
                r#type: ActorProofType::SignedInternalContext.into(),
                signed_proof: actor_proof,
            }),
            workload_proof: Some(WorkloadProof {
                signed_proof: workload_proof,
            }),
            resource: Some(TransitionResource {
                workflow_type: "order".into(),
                workflow_version: "1".into(),
                instance_id: "instance-1".into(),
                active_node_id: "review".into(),
                action: "COMPLETE_USER_TASK".into(),
                resource_attributes_digest: Vec::new(),
            }),
        }),
    }
}
