#![forbid(unsafe_code)]

use std::fmt::Write as _;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use base64::Engine as _;
use bpmn_compiler::{BpmnCompiler, CompilerLimits, SourceDocument};
use bpmp_authz_contracts::authorization::v1::{
    AuthorizationPolicyBundle, AuthorizationPolicyEffect, AuthorizationPolicyGrant,
};
use bpmp_authz_contracts::{
    AUTHORIZATION_BUNDLE_SCHEMA_VERSION, AuthorizationArtifactLimits, AuthorizationBundleCodec,
    Ed25519Signer as AuthorizationSigner,
};
use bpmp_contracts::{Ed25519Signer as WirSigner, WirCodec};
use clap::Parser;
use ed25519_dalek::SigningKey;
use ed25519_dalek::pkcs8::EncodePrivateKey as _;
use jsonwebtoken::{Algorithm, EncodingKey, Header, encode};
use rcgen::{
    BasicConstraints, CertificateParams, CertifiedIssuer, DistinguishedName, DnType, IsCa, KeyPair,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest as _, Sha256};

const MIGRATION: &str = include_str!("../../../db/human-runtime/migrations/001_human_runtime.sql");
const CONFIGURATION_MIGRATION: &str =
    include_str!("../../../db/configuration-service/migrations/001_configuration.sql");

#[derive(Debug, Parser)]
struct Arguments {
    #[arg(long)]
    manifest: PathBuf,
    #[arg(long)]
    output: PathBuf,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct Manifest {
    tenant_id: String,
    workflow_type: String,
    workflow_version: String,
    actor_id: String,
    actor_issuer: String,
    actor_audience: String,
    kafka: KafkaTopology,
    postgres_dsn: String,
    configuration_postgres_dsn: String,
    redis_address: String,
    otel_endpoint: String,
    runtime_mount: String,
    engine_public_addresses: Vec<String>,
    engine_peer_addresses: Vec<String>,
    engine_peer_listen_addresses: Vec<String>,
    engine_public_listen_address: String,
    human_address: String,
    human_listen_address: String,
    human_health_address: String,
    gateway_listen_address: String,
    configuration_url: String,
    configuration_listen_address: String,
    configuration_grpc_url: String,
    configuration_grpc_listen_address: String,
    tls_dns_names: Vec<String>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaTopology {
    brokers: Vec<String>,
    security_protocol: String,
    topics: KafkaTopics,
    consumer_groups: KafkaConsumerGroups,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaTopics {
    engine_committed_events: String,
    configuration_publications: String,
    human_escalations: String,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaConsumerGroups {
    engine_configuration_reloader: String,
    human_committed_events: String,
}

#[derive(Serialize)]
struct JwtClaims<'a> {
    iss: &'a str,
    sub: &'a str,
    aud: &'a str,
    tenant_id: &'a str,
    roles: Vec<&'a str>,
    capabilities: Vec<&'a str>,
    revoke_epoch: u64,
    iat: u64,
    nbf: u64,
    exp: u64,
}

fn main() -> Result<()> {
    let arguments = Arguments::parse();
    let manifest: Manifest =
        serde_json::from_slice(&fs::read(&arguments.manifest)?).context("decode E2E manifest")?;
    validate_manifest(&manifest)?;
    fs::create_dir_all(&arguments.output)?;
    generate(&manifest, &arguments.output)
}

#[allow(clippy::too_many_lines)]
fn generate(manifest: &Manifest, output: &Path) -> Result<()> {
    let now = SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs();
    let mount = manifest.runtime_mount.trim_end_matches('/');
    let secrets = output.join("secrets");
    fs::create_dir_all(&secrets)?;

    let tls = generate_tls(&manifest.tls_dns_names)?;
    write(&secrets.join("ca.pem"), tls.ca)?;
    write(&secrets.join("tls.pem"), tls.certificate)?;
    write(&secrets.join("tls-key.pem"), tls.private_key)?;

    let wir_key = derive_key(manifest, "wir");
    let wir_signer = WirSigner::from_bytes(&wir_key);
    write(&secrets.join("wir-private.key"), wir_key)?;
    write(
        &secrets.join("wir-public.key"),
        wir_signer.verifying_key_bytes(),
    )?;
    let artifact = compile_wir(manifest, &wir_signer)?;
    write(&output.join("approval.wir"), artifact)?;

    let policy_key = derive_key(manifest, "policy");
    let policy_signer = AuthorizationSigner::from_bytes(&policy_key);
    write(&secrets.join("policy-private.key"), policy_key)?;
    write(
        &secrets.join("policy-public.key"),
        policy_signer.verifying_key_bytes(),
    )?;
    write(
        &output.join("policy.bundle"),
        signed_policy(manifest, now, &policy_signer)?,
    )?;

    let gateway_workload = write_auth_key(manifest, output, "gateway-workload")?;
    let human_workload = write_auth_key(manifest, output, "human-workload")?;
    let internal_actor = write_auth_key(manifest, output, "internal-actor")?;
    let internal_workload = write_auth_key(manifest, output, "internal-workload")?;
    for scope in ["operational", "audit"] {
        write(
            &secrets.join(format!("payload-{scope}.key")),
            derive_key(manifest, &format!("payload-{scope}")),
        )?;
    }

    let jwt_key = SigningKey::from_bytes(&derive_key(manifest, "jwt"));
    let jwt_private = jwt_key.to_pkcs8_der()?;
    let jwt_public = jwt_key.verifying_key().to_bytes();
    write_json(
        &output.join("jwks.json"),
        &json!({"keys": [{
            "kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig",
            "kid": "actor-jwt-v1",
            "x": base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(jwt_public)
        }]}),
    )?;
    let mut header = Header::new(Algorithm::EdDSA);
    header.kid = Some("actor-jwt-v1".into());
    let token = encode(
        &header,
        &JwtClaims {
            iss: &manifest.actor_issuer,
            sub: &manifest.actor_id,
            aud: &manifest.actor_audience,
            tenant_id: &manifest.tenant_id,
            roles: vec!["reviewer"],
            capabilities: vec![
                "workflow.start",
                "workflow.complete",
                "configuration.read",
                "configuration.manage",
            ],
            revoke_epoch: 0,
            iat: now.saturating_sub(5),
            nbf: now.saturating_sub(5),
            exp: now.saturating_add(7_200),
        },
        &EncodingKey::from_ed_der(jwt_private.as_bytes()),
    )?;
    write(&output.join("actor.jwt"), token.as_bytes())?;

    write_json(
        &output.join("workflow-config.json"),
        &workflow_configuration(manifest),
    )?;
    for index in 0..3 {
        write_json(
            &output.join(format!("engine-{}.json", index + 1)),
            &engine_config(
                manifest,
                mount,
                index,
                &[
                    &gateway_workload,
                    &human_workload,
                    &internal_actor,
                    &internal_workload,
                ],
            ),
        )?;
    }
    write_json(
        &output.join("human-runtime.json"),
        &human_config(manifest, mount, &human_workload, &internal_actor),
    )?;
    write_json(
        &output.join("api-gateway.json"),
        &gateway_config(manifest, mount, &gateway_workload),
    )?;
    write_json(
        &output.join("configuration-service.json"),
        &configuration_config(manifest, mount),
    )?;
    write(
        &output.join("human-runtime.sql"),
        seeded_migration(manifest).as_bytes(),
    )?;
    write(
        &output.join("configuration-service.sql"),
        seeded_configuration_migration(manifest)?.as_bytes(),
    )?;
    write(
        &output.join("kafka-topics.sh"),
        kafka_topics_script(manifest).as_bytes(),
    )?;
    Ok(())
}

struct TlsMaterial {
    ca: Vec<u8>,
    certificate: Vec<u8>,
    private_key: Vec<u8>,
}

fn generate_tls(dns_names: &[String]) -> Result<TlsMaterial> {
    let mut ca_params = CertificateParams::new(vec!["bpmp-e2e-ca".into()])?;
    ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    let mut ca_name = DistinguishedName::new();
    ca_name.push(DnType::CommonName, "BPMP E2E CA");
    ca_params.distinguished_name = ca_name;
    let ca_key = KeyPair::generate()?;
    let ca = CertifiedIssuer::self_signed(ca_params, ca_key)?;

    let mut leaf_params = CertificateParams::new(dns_names.to_vec())?;
    let mut leaf_name = DistinguishedName::new();
    leaf_name.push(DnType::CommonName, "BPMP E2E Services");
    leaf_params.distinguished_name = leaf_name;
    let leaf_key = KeyPair::generate()?;
    let leaf = leaf_params.signed_by(&leaf_key, &ca)?;
    Ok(TlsMaterial {
        ca: ca.pem().into_bytes(),
        certificate: leaf.pem().into_bytes(),
        private_key: leaf_key.serialize_pem().into_bytes(),
    })
}

fn compile_wir(manifest: &Manifest, signer: &WirSigner) -> Result<Vec<u8>> {
    let bpmn = format!(
        r#"<b:definitions xmlns:b="http://www.omg.org/spec/BPMN/20100524/MODEL"><b:process id="{}"><b:startEvent id="start"/><b:userTask id="review" name="approval-review" assignmentPolicyRef="reviewers" formKey="approval-form" resultVariable="decision"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="review"/><b:sequenceFlow id="f2" sourceRef="review" targetRef="end"/></b:process></b:definitions>"#,
        manifest.workflow_type
    );
    let compiler = BpmnCompiler::new(CompilerLimits::new(128 * 1024, 64)?);
    let wir = compiler
        .compile(
            SourceDocument {
                name: "e2e-approval.bpmn",
                bytes: bpmn.as_bytes(),
            },
            &manifest.tenant_id,
            &manifest.workflow_version,
        )
        .map_err(|diagnostics| anyhow::anyhow!("compile E2E BPMN: {diagnostics:?}"))?;
    WirCodec::seal(wir, signer).map_err(Into::into)
}

fn signed_policy(
    manifest: &Manifest,
    now_seconds: u64,
    signer: &AuthorizationSigner,
) -> Result<Vec<u8>> {
    let grants = [
        ("allow-start", "start", "START"),
        ("allow-review", "review", "COMPLETE_USER_TASK"),
    ]
    .into_iter()
    .map(|(grant_id, node, action)| AuthorizationPolicyGrant {
        grant_id: grant_id.into(),
        actor_ids: vec![manifest.actor_id.clone()],
        roles: Vec::new(),
        required_capabilities: Vec::new(),
        workflow_type: manifest.workflow_type.clone(),
        workflow_version: manifest.workflow_version.clone(),
        active_node_id: node.into(),
        action: action.into(),
        effect: AuthorizationPolicyEffect::Allow.into(),
        priority: 100,
    })
    .collect();
    let bundle = AuthorizationPolicyBundle {
        schema_version: AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
        tenant_id: manifest.tenant_id.clone(),
        bundle_sequence: 1,
        policy_version: "policy-e2e-v1".into(),
        revoke_epoch: 0,
        valid_from_epoch_ms: now_seconds.saturating_sub(60).saturating_mul(1_000),
        expires_at_epoch_ms: now_seconds.saturating_add(86_400).saturating_mul(1_000),
        grants,
        actor_revoke_epochs: Vec::new(),
        signing_key_id: String::new(),
        content_hash: Vec::new(),
        signature: Vec::new(),
    };
    AuthorizationBundleCodec::seal(
        bundle,
        "policy-key-v1",
        signer,
        AuthorizationArtifactLimits::new(64 * 1024, 64)?,
    )
    .map_err(Into::into)
}

struct AuthKey {
    id: String,
    private_path: String,
    public_path: String,
}

fn write_auth_key(manifest: &Manifest, output: &Path, label: &str) -> Result<AuthKey> {
    let key = derive_key(manifest, label);
    let signer = AuthorizationSigner::from_bytes(&key);
    let private_name = format!("{label}-private.key");
    let public_name = format!("{label}-public.key");
    write(&output.join("secrets").join(&private_name), key)?;
    write(
        &output.join("secrets").join(&public_name),
        signer.verifying_key_bytes(),
    )?;
    Ok(AuthKey {
        id: format!("{label}-v1"),
        private_path: format!("secrets/{private_name}"),
        public_path: format!("secrets/{public_name}"),
    })
}

fn workflow_configuration(manifest: &Manifest) -> Value {
    let hash = Sha256::digest(b"bpmp-e2e-config-v1");
    json!({
        "tenant_id": manifest.tenant_id,
        "workflow_type": manifest.workflow_type,
        "workflow_version": manifest.workflow_version,
        "snapshot": {
            "config_id": "e2e-config", "config_version": "config-e2e-v1",
            "policy_version": "policy-e2e-v1", "schema_version": 1,
            "content_hash_base64": base64::engine::general_purpose::STANDARD.encode(hash),
            "scopes": [{"kind": "TENANT", "reference": manifest.tenant_id}],
            "engine": {
                "snapshot_interval_events": 1, "max_events_per_decision": 64,
                "max_multi_instance_cardinality": 1000,
                "default_multi_instance_parallelism": 16, "command_timeout_ms": 5000,
                "optimistic_conflict_retry": {"max_attempts": 3, "initial_backoff_ms": 5, "max_backoff_ms": 100, "multiplier_millis": 2000},
                "local_wasm": {"max_module_bytes": 1_048_576, "max_input_bytes": 65_536, "max_output_bytes": 65_536, "max_memory_bytes": 16_777_216, "max_wasm_stack_bytes": 1_048_576, "max_table_elements": 1024, "max_instances": 4, "max_tables": 4, "max_memories": 2, "fuel": 1_000_000},
                "event_payload_key_scope": format!("{}/operational", manifest.tenant_id),
                "authorization_audit_key_scope": format!("{}/audit", manifest.tenant_id),
                "boundary_runtime": boundary_config("engine-boundary")
            }
        }
    })
}

fn boundary_config(worker: &str) -> Value {
    json!({"projection_batch_size": 64, "dispatch_batch_size": 32, "max_dispatch_attempts": 5, "retry_delay_ms": 100, "lease_duration_ms": 5000, "max_timer_horizon_ms": 31_536_000_000_u64, "max_expression_bytes": 4096, "worker_id": worker, "max_signal_id_bytes": 256, "max_reference_bytes": 512, "max_subscriptions_per_instance": 128})
}

fn engine_config(manifest: &Manifest, mount: &str, index: usize, keys: &[&AuthKey]) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    let verification = |key: &AuthKey| json!({"key_id": key.id, "path": path(&key.public_path)});
    json!({
        "listen_addr": manifest.engine_public_listen_address,
        "data_path": format!("/data/engine-{}", index + 1),
        "tls": {"server_certificate": path("secrets/tls.pem"), "server_private_key": path("secrets/tls-key.pem"), "client_ca": path("secrets/ca.pem")},
        "wir": {"verification_key": path("secrets/wir-public.key"), "artifacts": [path("approval.wir")], "configurations": []},
        "configuration_resolver": {
            "endpoint": manifest.configuration_grpc_url,
            "tls_domain": "configuration-service",
            "platform_reference": "bpmp",
            "environment_reference": "e2e",
            "timeout_ms": 2000,
            "max_attempts": 30,
            "retry_delay_ms": 250,
            "max_decoding_bytes": 1_048_576,
            "max_encoding_bytes": 1_048_576
        },
        "authorization": {
            "actor_keys": [verification(keys[2])],
            "workload_keys": [verification(keys[0]), verification(keys[1]), verification(keys[3])],
            "policy_keys": [{"key_id": "policy-key-v1", "path": path("secrets/policy-public.key")}],
            "policy_bundles": [path("policy.bundle")], "jwks": path("jwks.json"),
            "jwt_issuers": [manifest.actor_issuer], "jwt_audiences": [manifest.actor_audience], "jwt_algorithms": ["EdDSA"],
            "max_proof_bytes": 16384, "max_roles": 32, "max_capabilities": 64, "max_policy_bytes": 65536, "max_policy_grants": 64, "max_jwks_keys": 16, "clock_skew_seconds": 30,
            "internal_dispatch": {"actor_signing_key": path(&keys[2].private_path), "actor_signing_key_id": keys[2].id, "workload_signing_key": path(&keys[3].private_path), "workload_signing_key_id": keys[3].id, "actor_id": "engine-scheduler", "workload_id": "bpmp-engine", "roles": ["system"], "capabilities": ["boundary.trigger"], "proof_ttl_ms": 60000}
        },
        "payload_keys": [
            {"key_scope": format!("{}/operational", manifest.tenant_id), "key_version": "e2e-v1", "key_epoch": 1, "path": path("secrets/payload-operational.key")},
            {"key_scope": format!("{}/audit", manifest.tenant_id), "key_version": "e2e-v1", "key_epoch": 1, "path": path("secrets/payload-audit.key")}
        ],
        "rocksdb": {"max_open_files": 128, "write_buffer_size_bytes": 8_388_608, "max_background_jobs": 2, "max_replay_events": 10_000},
        "raft": {
            "cluster_name": "bpmp-e2e", "node_id": index + 1,
            "peer_listen_addr": manifest.engine_peer_listen_addresses[index],
            "peers": (0..3).map(|peer| json!({"node_id": peer + 1, "raft_address": manifest.engine_peer_addresses[peer], "tls_domain": format!("engine{}", peer + 1)})).collect::<Vec<_>>(),
            "bootstrap": index == 0, "heartbeat_interval_ms": 100, "election_timeout_min_ms": 300, "election_timeout_max_ms": 600, "rpc_timeout_ms": 2000,
            "max_conditions": 512, "max_mutations": 512, "max_batch_bytes": 4_194_304, "max_snapshot_bytes": 67_108_864,
            "append_only_column_families": ["events","dedup","outbox","idempotency","authorization_audit","compensation_ledger","governance_audit","raft_applied_commands"]
        },
        "grpc": {"max_decoding_bytes": 1_048_576, "max_encoding_bytes": 1_048_576},
        "workers": {"poll_interval_ms": 100, "outbox_batch_size": 64, "outbox_max_attempts": 10, "outbox_initial_retry_ms": 50, "outbox_max_retry_ms": 1000, "outbox_retry_multiplier_millis": 2000, "boundary": boundary_config(&format!("engine-{}-boundary", index + 1)), "local_task_batch_size": 32, "local_task_max_attempts": 3, "local_task_initial_retry_ms": 25, "local_task_max_retry_ms": 250, "local_task_retry_multiplier_millis": 2000},
        "kafka": {
            "brokers": manifest.kafka.brokers,
            "client_id": format!("bpmp-engine-{}", index + 1),
            "security": {"protocol": manifest.kafka.security_protocol, "ca_location": null, "certificate_location": null, "key_location": null},
            "topics": {
                "committed_events": manifest.kafka.topics.engine_committed_events,
                "configuration_publications": manifest.kafka.topics.configuration_publications
            },
            "consumer_groups": {"configuration_reloader": manifest.kafka.consumer_groups.engine_configuration_reloader},
            "message_timeout_ms": 5000,
            "max_message_bytes": 1_048_576,
            "max_inflight": 1,
            "consumer_poll_timeout_ms": 250,
            "consumer_session_timeout_ms": 6000
        },
        "wasm_modules": []
    })
}

fn human_config(manifest: &Manifest, mount: &str, workload: &AuthKey, internal: &AuthKey) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.human_listen_address, "postgres_dsn": manifest.postgres_dsn,
        "apply_migrations": true, "migration_path": path("human-runtime.sql"),
        "engine_address": manifest.engine_public_addresses[1],
        "tls": {"server_certificate": path("secrets/tls.pem"), "server_private_key": path("secrets/tls-key.pem"), "client_certificate": path("secrets/tls.pem"), "client_private_key": path("secrets/tls-key.pem"), "client_ca": path("secrets/ca.pem"), "engine_ca": path("secrets/ca.pem"), "engine_server_name": "engine2"},
        "kafka": {"brokers": manifest.kafka.brokers, "committed_event_topic": manifest.kafka.topics.engine_committed_events, "escalation_topic": manifest.kafka.topics.human_escalations, "consumer_group": manifest.kafka.consumer_groups.human_committed_events, "batch_size": 64},
        "identity": {"jwks_path": path("jwks.json"), "internal_keys": {(internal.id.clone()): path(&internal.public_path)}, "issuers": [manifest.actor_issuer], "audiences": [manifest.actor_audience], "allowed_jwt_methods": ["EdDSA"], "workload_id": "human-runtime", "max_proof_bytes": 16384, "max_jwks_keys": 16, "max_roles": 32, "max_capabilities": 64, "clock_skew_ms": 30000},
        "workload": {"id": "human-runtime", "signing_key_id": workload.id, "private_key_path": path(&workload.private_path), "proof_ttl_ms": 60000},
        "grpc": {"max_receive_bytes": 1_048_576, "max_send_bytes": 1_048_576},
        "reliability": {"max_attempts": 5, "initial_backoff_ms": 50, "max_backoff_ms": 1000, "attempt_timeout_ms": 3000, "failure_threshold": 5, "open_duration_ms": 1000, "retryable_codes": ["UNAVAILABLE","DEADLINE_EXCEEDED"]},
        "health": {"listen_address": manifest.human_health_address, "readiness_timeout_ms": 1000},
        "telemetry": {"service_name": "human-runtime-e2e", "service_version": "e2e", "endpoint": manifest.otel_endpoint, "insecure": true, "sample_ratio": 0.0, "export_timeout_ms": 1000},
        "escalation": {"worker_id": "human-e2e", "batch_size": 32, "lease_ms": 5000, "retry_ms": 1000, "poll_ms": 250}
    })
}

fn gateway_config(manifest: &Manifest, mount: &str, workload: &AuthKey) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.gateway_listen_address,
        "engine_address": manifest.engine_public_addresses[1], "human_address": manifest.human_address,
        "configuration_url": manifest.configuration_url,
        "public_tls": {"certificate": path("secrets/tls.pem"), "private_key": path("secrets/tls-key.pem")},
        "upstream_tls": {"certificate": path("secrets/tls.pem"), "private_key": path("secrets/tls-key.pem"), "ca": path("secrets/ca.pem"), "engine_server_name": "engine2", "human_server_name": "human-runtime", "configuration_server_name": "configuration-service"},
        "identity": {"jwks_path": path("jwks.json"), "issuers": [manifest.actor_issuer], "audiences": [manifest.actor_audience], "algorithms": ["EdDSA"], "max_token_bytes": 16384, "max_jwks_keys": 16, "clock_skew_seconds": 30},
        "workload": {"id": "api-gateway", "signing_key_id": workload.id, "private_key_path": path(&workload.private_path), "proof_ttl_ms": 60000},
        "rate_limit": {"requests": 1000, "window_ms": 60000, "redis_address": manifest.redis_address, "redis_username": "", "redis_password_file": "", "redis_database": 0, "redis_key_prefix": "bpmp:e2e", "operation_timeout_ms": 1000},
        "http": {"read_header_timeout_ms": 2000, "read_timeout_ms": 5000, "write_timeout_ms": 5000, "idle_timeout_ms": 10000, "shutdown_timeout_ms": 5000, "max_body_bytes": 65536, "max_upstream_response_bytes": 1_048_576},
        "grpc": {"max_receive_bytes": 1_048_576, "max_send_bytes": 1_048_576},
        "reliability": {"max_attempts": 5, "initial_backoff_ms": 50, "max_backoff_ms": 1000, "attempt_timeout_ms": 3000, "failure_threshold": 5, "open_duration_ms": 1000, "retryable_codes": ["UNAVAILABLE","DEADLINE_EXCEEDED"]},
        "health": {"readiness_timeout_ms": 1000},
        "telemetry": {"service_name": "api-gateway-e2e", "service_version": "e2e", "endpoint": manifest.otel_endpoint, "insecure": true, "sample_ratio": 0.0, "export_timeout_ms": 1000},
        "tenant_key_scopes": {(manifest.tenant_id.clone()): format!("{}/operational", manifest.tenant_id)}
    })
}

fn configuration_config(manifest: &Manifest, mount: &str) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.configuration_listen_address,
        "postgres_dsn": manifest.configuration_postgres_dsn,
        "apply_migrations": true,
        "migration_path": path("configuration-service.sql"),
        "tls": {
            "certificate": path("secrets/tls.pem"),
            "private_key": path("secrets/tls-key.pem"),
            "client_ca": path("secrets/ca.pem")
        },
        "grpc": {
            "listen_address": manifest.configuration_grpc_listen_address,
            "max_receive_bytes": 1_048_576,
            "max_send_bytes": 1_048_576
        },
        "kafka": {
            "brokers": manifest.kafka.brokers,
            "client_id": "bpmp-configuration-publisher",
            "security_protocol": manifest.kafka.security_protocol,
            "dial_timeout_ms": 2000,
            "request_timeout_ms": 5000,
            "topic": manifest.kafka.topics.configuration_publications,
            "message_timeout_ms": 5000,
            "max_inflight": 1,
            "max_message_bytes": 1_048_576,
            "required_acks": "ALL",
            "enable_idempotence": true
        },
        "outbox": {
            "worker_id": "configuration-publisher-e2e",
            "batch_size": 64,
            "lease_duration_ms": 10000,
            "poll_interval_ms": 100,
            "initial_retry_delay_ms": 100,
            "max_retry_delay_ms": 5000,
            "retry_multiplier_millis": 2000
        },
        "identity": {
            "jwks_path": path("jwks.json"),
            "issuers": [manifest.actor_issuer],
            "audiences": [manifest.actor_audience],
            "algorithms": ["EdDSA"],
            "max_token_bytes": 16384,
            "max_jwks_keys": 16,
            "clock_skew_ms": 30000,
            "read_capability": "configuration.read",
            "manage_capability": "configuration.manage"
        },
        "api": {
            "default_page_size": 50,
            "max_page_size": 200,
            "max_body_bytes": 1_048_576,
            "read_header_timeout_ms": 2000,
            "read_timeout_ms": 5000,
            "write_timeout_ms": 5000,
            "idle_timeout_ms": 10000,
            "shutdown_timeout_ms": 5000,
            "readiness_timeout_ms": 1000
        },
        "telemetry": {
            "service_name": "configuration-service-e2e",
            "service_version": "e2e",
            "endpoint": manifest.otel_endpoint,
            "insecure": true,
            "sample_ratio": 0.0,
            "export_timeout_ms": 1000
        }
    })
}

fn seeded_configuration_migration(manifest: &Manifest) -> Result<String> {
    let snapshot = workflow_configuration(manifest);
    let engine = snapshot
        .pointer("/snapshot/engine")
        .context("E2E configuration snapshot has no engine policy")?;
    let values = sql_literal(&serde_json::to_string(engine)?);
    let tenant = sql_literal(&manifest.tenant_id);
    let mut hash = String::with_capacity(64);
    for byte in Sha256::digest(b"bpmp-e2e-config-v1") {
        write!(&mut hash, "{byte:02x}")?;
    }
    Ok(format!(
        "{CONFIGURATION_MIGRATION}\n\
         INSERT INTO configuration_profiles(id,tenant_id,owner,name,scope_type,scope_reference,aggregate_version,is_deleted,created_at,created_by,updated_at,updated_by) VALUES\
         ('00000000-0000-0000-0000-00000000c001','{tenant}','ENGINE','E2E tenant policy','TENANT','{tenant}',2,false,now(),'fixture',now(),'fixture');\n\
         INSERT INTO configuration_versions(id,profile_id,tenant_id,ordinal,config_version,policy_version,schema_version,status,values_json,content_hash,reason,created_at,created_by,published_at,published_by) VALUES\
         ('00000000-0000-0000-0000-00000000c002','00000000-0000-0000-0000-00000000c001','{tenant}',1,'config-e2e-v1','policy-e2e-v1',1,'PUBLISHED','{values}'::jsonb,decode('{hash}','hex'),'fixture bootstrap',now(),'fixture',now(),'fixture');\n\
         UPDATE configuration_profiles SET current_published_version_id='00000000-0000-0000-0000-00000000c002' WHERE id='00000000-0000-0000-0000-00000000c001';\n\
         INSERT INTO configuration_active_scopes(tenant_id,owner,scope_type,scope_reference,profile_id,version_id,updated_at) VALUES\
         ('{tenant}','ENGINE','TENANT','{tenant}','00000000-0000-0000-0000-00000000c001','00000000-0000-0000-0000-00000000c002',now());\n"
    ))
}

fn seeded_migration(manifest: &Manifest) -> String {
    let tenant = sql_literal(&manifest.tenant_id);
    let workflow = sql_literal(&manifest.workflow_type);
    let version = sql_literal(&manifest.workflow_version);
    format!(
        "{MIGRATION}\nINSERT INTO assignment_policies(tenant_id,policy_ref,workflow_type,workflow_version,node_id,assignee_id,sla_duration_ms,escalation_policy_ref,config_version,created_at,created_by,updated_at,updated_by) VALUES('{tenant}','reviewers','{workflow}','{version}','review','{}',60000,'manager','config-e2e-v1',now(),'fixture',now(),'fixture');\nINSERT INTO human_tenant_security_profiles(tenant_id,encryption_key_scope,config_version) VALUES('{tenant}','{tenant}/operational','config-e2e-v1');\n",
        sql_literal(&manifest.actor_id)
    )
}

fn sql_literal(value: &str) -> String {
    value.replace('\'', "''")
}

fn derive_key(manifest: &Manifest, label: &str) -> [u8; 32] {
    Sha256::digest(format!(
        "bpmp-e2e-fixture-v1\0{}\0{}\0{label}",
        manifest.tenant_id, manifest.workflow_version
    ))
    .into()
}

fn validate_manifest(manifest: &Manifest) -> Result<()> {
    if manifest.engine_public_addresses.len() != 3
        || manifest.engine_peer_addresses.len() != 3
        || manifest.engine_peer_listen_addresses.len() != 3
        || manifest.kafka.brokers.is_empty()
        || manifest.tls_dns_names.is_empty()
    {
        anyhow::bail!(
            "E2E manifest must define exactly three engines and non-empty brokers/TLS SANs"
        );
    }
    Ok(())
}

fn kafka_topics_script(manifest: &Manifest) -> String {
    let brokers = manifest.kafka.brokers.join(",");
    [
        &manifest.kafka.topics.engine_committed_events,
        &manifest.kafka.topics.configuration_publications,
        &manifest.kafka.topics.human_escalations,
    ]
    .into_iter()
    .map(|topic| {
        format!(
            "rpk topic create '{topic}' --brokers '{brokers}' --partitions 3 --replicas 1 || rpk topic describe '{topic}' --brokers '{brokers}'"
        )
    })
    .collect::<Vec<_>>()
    .join("\n")
}

fn write(path: &Path, bytes: impl AsRef<[u8]>) -> Result<()> {
    fs::write(path, bytes).with_context(|| format!("write {}", path.display()))
}

fn write_json(path: &Path, value: &Value) -> Result<()> {
    write(path, serde_json::to_vec_pretty(value)?)
}
