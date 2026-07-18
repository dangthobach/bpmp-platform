use std::collections::BTreeMap;

use bpmp_adapter_policy_bundle::VerifiedAuthorizationStore;
use bpmp_authz_contracts::authorization::v1::{
    AuthorizationPolicyBundle, AuthorizationPolicyEffect, AuthorizationPolicyGrant,
    AuthorizationRevokeEpochUpdate, SignedActorContext, SignedWorkloadContext,
};
use bpmp_authz_contracts::{
    AUTHORIZATION_BUNDLE_SCHEMA_VERSION, AUTHORIZATION_PROOF_SCHEMA_VERSION, ActorProofCodec,
    AuthorizationArtifactLimits, AuthorizationBundleCodec, AuthorizationKeyring,
    AuthorizationProofLimits, AuthorizationRevokeCodec, Ed25519Signer, WorkloadProofCodec,
};
use bpmp_domain_core::{
    Command, CommandId, ConfigId, ConfigVersion, ConfigurationScope, CorrelationId, EnginePolicy,
    IdempotencyKey, InstanceId, KeyScope, LocalWasmPolicy, Node, NodeId, PolicyVersion,
    ResolvedConfigSnapshot, RetryPolicy, ScopeKind, TaskType, TenantId, WorkflowDefinition,
    WorkflowType, WorkflowVersion,
};
use bpmp_engine::memory::{InMemoryConfigurationProvider, InMemoryWorkflowStore};
use bpmp_engine::{
    AuthorizedCommand, ConfigurationLookup, EmbeddedAuthorizationProvider, Engine, HandleOutcome,
    OutboxStorePort, WorkflowStorePort,
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
        grants: vec![AuthorizationPolicyGrant {
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
        }],
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
            max_events_per_decision: 2,
            command_timeout_ms: 1_000,
            optimistic_conflict_retry: RetryPolicy {
                max_attempts: 3,
                initial_backoff_ms: 10,
                max_backoff_ms: 100,
                multiplier_millis: 2_000,
            },
            local_wasm: local_wasm_policy(),
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
        workload_proof,
        encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
        variables: BTreeMap::default(),
        command: Command::StartWorkflow {
            occurred_at_epoch_ms: 42,
        },
    }
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
