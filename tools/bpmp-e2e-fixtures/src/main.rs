#![forbid(unsafe_code)]
#![recursion_limit = "256"]

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
const CONFIGURATION_HOT_RELOAD_MIGRATION: &str =
    include_str!("../../../db/configuration-service/migrations/002_kafka_hot_reload.sql");
const CONFIGURATION_TENANT_READINESS_MIGRATION: &str =
    include_str!("../../../db/configuration-service/migrations/003_owner_and_tenant_readiness.sql");
const CONFIGURATION_AUDIT_VERSIONS_MIGRATION: &str =
    include_str!("../../../db/configuration-service/migrations/004_audit_effective_versions.sql");
const CONFIGURATION_OUTBOX_OBSERVABILITY_MIGRATION: &str = include_str!(
    "../../../db/configuration-service/migrations/005_outbox_observability_context.sql"
);
const PROJECTION_MIGRATION: &str =
    include_str!("../../../db/projection-service/migrations/001_projection.sql");
const PROJECTION_QUERY_MIGRATION: &str =
    include_str!("../../../db/projection-service/migrations/002_completed_process_query.sql");
const PROJECTION_REVOKE_MIGRATION: &str =
    include_str!("../../../db/projection-service/migrations/003_query_revoke_kafka.sql");
const PROJECTION_TEXT_TRANSFORMS_MIGRATION: &str =
    include_str!("../../../db/projection-service/migrations/004_query_text_transforms.sql");
const PROJECTION_QUERY_API_MIGRATION: &str =
    include_str!("../../../db/projection-service/migrations/005_query_api.sql");
const PROJECTION_STORAGE_HARDENING_MIGRATION: &str =
    include_str!("../../../db/projection-service/migrations/006_query_storage_hardening.sql");
const GOVERNANCE_MIGRATION: &str =
    include_str!("../../../db/governance-service/migrations/001_governance.sql");

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
    projection_postgres_dsn: String,
    governance_postgres_dsn: String,
    authz_postgres_dsn: String,
    authz_app_postgres_dsn: String,
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
    projection_listen_address: String,
    projection_address: String,
    projection_health_address: String,
    cockpit_gateway_listen_address: String,
    cockpit_gateway_health_address: String,
    cockpit_web_public_origin: String,
    governance_listen_address: String,
    governance_health_address: String,
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
    topic_properties: KafkaTopicProperties,
}
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaTopicProperties {
    partitions: u32,
    replicas: u32,
    min_in_sync_replicas: u32,
    retention_ms: u64,
    max_message_bytes: u32,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaTopics {
    authorization_query_revocations: String,
    authorization_query_revocations_dlq: String,
    engine_committed_events: String,
    configuration_publications: String,
    human_escalations: String,
    tenant_lifecycle: String,
    tenant_configuration_readiness: String,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaConsumerGroups {
    projection_query_revocations: String,
    engine_configuration_reloaders: Vec<String>,
    human_committed_events: String,
    projection_committed_events: String,
    cockpit_committed_events: String,
    api_gateway_configuration_reloader: String,
    human_runtime_configuration_reloader: String,
    projection_configuration_reloader: String,
    governance_configuration_reloader: String,
    configuration_tenant_lifecycle: String,
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
    let tls_certificate_sha256_hex = tls.certificate_sha256_hex.clone();
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
    write(
        &secrets.join("projection-cursor-v1.key"),
        derive_key(manifest, "projection-cursor-v1"),
    )?;
    let revoke_key = SigningKey::from_bytes(&derive_key(manifest, "query-revoke-feed"));
    write(&secrets.join("query-revoke-v1.key"), revoke_key.to_bytes())?;
    write(
        &secrets.join("query-revoke-v1.pub"),
        revoke_key.verifying_key().to_bytes(),
    )?;
    write_json(
        &output.join("authz-revoke-kafka.json"),
        &json!({"topic":manifest.kafka.topics.authorization_query_revocations,"worker_id":"authz-query-revoke-e2e-1","batch_size":64,"lease_ms":10000,"poll_ms":250,"delivery_timeout_ms":2000,"retry_backoff_ms":500,"max_retry_attempts":3,"signing_key_id":"query-revoke-v1","signing_key_path":format!("{mount}/secrets/query-revoke-v1.key")}),
    )?;

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
                "workflow.query.all",
            ],
            revoke_epoch: 0,
            iat: now.saturating_sub(5),
            nbf: now.saturating_sub(5),
            exp: now.saturating_add(7_200),
        },
        &EncodingKey::from_ed_der(jwt_private.as_bytes()),
    )?;
    write(&output.join("actor.jwt"), token.as_bytes())?;
    for (file, actor, capability, epoch) in [
        (
            "query-own.jwt",
            manifest.actor_id.as_str(),
            "workflow.query.own",
            0,
        ),
        (
            "query-other.jwt",
            "00000000-0000-4000-8000-000000000099",
            "workflow.query.own",
            0,
        ),
        (
            "query-denied.jwt",
            manifest.actor_id.as_str(),
            "workflow.complete",
            0,
        ),
        (
            "query-restored.jwt",
            manifest.actor_id.as_str(),
            "workflow.query.own",
            2,
        ),
    ] {
        let query_token = encode(
            &header,
            &JwtClaims {
                iss: &manifest.actor_issuer,
                sub: actor,
                aud: &manifest.actor_audience,
                tenant_id: &manifest.tenant_id,
                roles: Vec::new(),
                capabilities: vec![capability],
                revoke_epoch: epoch,
                iat: now.saturating_sub(5),
                nbf: now.saturating_sub(5),
                exp: now.saturating_add(7_200),
            },
            &EncodingKey::from_ed_der(jwt_private.as_bytes()),
        )?;
        write(&output.join(file), query_token.as_bytes())?;
    }
    let service_token = encode(
        &header,
        &JwtClaims {
            iss: &manifest.actor_issuer,
            sub: "authz-app",
            aud: &manifest.actor_audience,
            tenant_id: &manifest.tenant_id,
            roles: vec!["workload"],
            capabilities: vec![
                "authz.check",
                "authz.filter",
                "authz.explain",
                "authz.revoke",
            ],
            revoke_epoch: 0,
            iat: now.saturating_sub(5),
            nbf: now.saturating_sub(5),
            exp: now.saturating_add(7_200),
        },
        &EncodingKey::from_ed_der(jwt_private.as_bytes()),
    )?;
    write(&secrets.join("authz-app.jwt"), service_token.as_bytes())?;
    let projection_token = encode(
        &header,
        &JwtClaims {
            iss: &manifest.actor_issuer,
            sub: "projection-service",
            aud: &manifest.actor_audience,
            tenant_id: &manifest.tenant_id,
            roles: vec!["workload"],
            capabilities: vec!["authz.filter"],
            revoke_epoch: 0,
            iat: now.saturating_sub(5),
            nbf: now.saturating_sub(5),
            exp: now.saturating_add(7200),
        },
        &EncodingKey::from_ed_der(jwt_private.as_bytes()),
    )?;
    write(
        &secrets.join("projection-pdp.jwt"),
        projection_token.as_bytes(),
    )?;
    for label in ["governance-requester", "governance-approver"] {
        let key = SigningKey::from_bytes(&derive_key(manifest, label));
        write(
            &secrets.join(format!("{label}-private.key")),
            key.to_bytes(),
        )?;
        write(
            &secrets.join(format!("{label}-public.key")),
            key.verifying_key().to_bytes(),
        )?;
    }

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
                &tls_certificate_sha256_hex,
            ),
        )?;
    }
    write_json(
        &output.join("human-runtime.json"),
        &human_config(
            manifest,
            mount,
            &human_workload,
            &internal_actor,
            &tls_certificate_sha256_hex,
        ),
    )?;
    write_json(
        &output.join("api-gateway.json"),
        &gateway_config(manifest, mount, &gateway_workload),
    )?;
    write_json(
        &output.join("configuration-service.json"),
        &configuration_config(manifest, mount, &tls_certificate_sha256_hex),
    )?;
    write_json(
        &output.join("projection-service.json"),
        &projection_config(manifest, mount, &tls_certificate_sha256_hex),
    )?;
    write_json(
        &output.join("cockpit-gateway.json"),
        &cockpit_gateway_config(manifest, mount),
    )?;
    write_json(
        &output.join("cockpit-web-config.json"),
        &cockpit_web_config(manifest),
    )?;
    write(
        &output.join("cockpit-web-nginx.conf"),
        cockpit_web_nginx_config().as_bytes(),
    )?;
    write(
        &output.join("authz-server.env"),
        format!(
            "{}QUERY_REVOKE_KAFKA_CONFIG={mount}/authz-revoke-kafka.json\n",
            authz_server_env(manifest)
        )
        .as_bytes(),
    )?;
    write(
        &output.join("authz-app.env"),
        authz_app_env(manifest).as_bytes(),
    )?;
    write(
        &output.join("authz-seed.sql"),
        authz_seed_sql(manifest).as_bytes(),
    )?;
    write_json(
        &output.join("governance-service.json"),
        &governance_config(manifest, mount, &tls_certificate_sha256_hex),
    )?;
    write(
        &output.join("human-runtime.sql"),
        schema_migration("human_runtime", &seeded_migration(manifest)),
    )?;
    write(
        &output.join("configuration-service.sql"),
        schema_migration("configuration", &seeded_configuration_migration(manifest)?),
    )?;
    write(
        &output.join("projection-schema.sql"),
        b"CREATE SCHEMA IF NOT EXISTS projection;\n",
    )?;
    let projection_migration_dir = output.join("projection-service-migrations");
    fs::create_dir_all(&projection_migration_dir).with_context(|| {
        format!(
            "create projection migration directory {}",
            projection_migration_dir.display()
        )
    })?;
    for (name, migration) in [
        ("001_projection.sql", PROJECTION_MIGRATION),
        (
            "002_completed_process_query.sql",
            PROJECTION_QUERY_MIGRATION,
        ),
        ("003_query_revoke_kafka.sql", PROJECTION_REVOKE_MIGRATION),
        (
            "004_query_text_transforms.sql",
            PROJECTION_TEXT_TRANSFORMS_MIGRATION,
        ),
        ("005_query_api.sql", PROJECTION_QUERY_API_MIGRATION),
        (
            "006_query_storage_hardening.sql",
            PROJECTION_STORAGE_HARDENING_MIGRATION,
        ),
    ] {
        write(&projection_migration_dir.join(name), migration.as_bytes())?;
    }
    write(
        &output.join("governance-service.sql"),
        schema_migration("governance", GOVERNANCE_MIGRATION),
    )?;
    write(
        &output.join("key-lifecycle-nginx.conf"),
        b"events {}\nhttp { server { listen 8080; location = / { return 200 'ok'; } location = /jwks.json { alias /runtime/jwks.json; default_type application/json; } location = /barrier { return 204; } location = /shred { return 204; } } }\n",
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
    certificate_sha256_hex: String,
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
    let certificate_sha256_hex = Sha256::digest(leaf.der().as_ref()).iter().fold(
        String::with_capacity(64),
        |mut output, byte| {
            let _ = write!(output, "{byte:02x}");
            output
        },
    );
    Ok(TlsMaterial {
        ca: ca.pem().into_bytes(),
        certificate: leaf.pem().into_bytes(),
        private_key: leaf_key.serialize_pem().into_bytes(),
        certificate_sha256_hex,
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
                "boundary_runtime": boundary_config("engine-boundary"),
                "workers": {
                    "poll_interval_ms": 100,
                    "outbox_batch_size": 64,
                    "outbox_retry": {
                        "max_attempts": 10,
                        "initial_backoff_ms": 50,
                        "max_backoff_ms": 1000,
                        "multiplier_millis": 2000
                    },
                    "local_task_batch_size": 32,
                    "local_task_retry": {
                        "max_attempts": 3,
                        "initial_backoff_ms": 25,
                        "max_backoff_ms": 250,
                        "multiplier_millis": 2000
                    },
                    "remote": {
                        "dispatch_batch_size": 32,
                        "max_workers": 100_000,
                        "max_credit_per_worker": 64,
                        "max_capabilities_per_worker": 32,
                        "lease_duration_ms": 30000,
                        "heartbeat_timeout_ms": 10000,
                        "max_identifier_bytes": 256,
                        "max_protocol_version_bytes": 32,
                        "stream_channel_capacity": 128,
                        "max_input_bytes": 65536,
                        "max_output_bytes": 65536,
                        "max_attempts": 5,
                        "retry_delay_ms": 1000
                    }
                }
            }
        }
    })
}

fn boundary_config(worker: &str) -> Value {
    json!({"projection_batch_size": 64, "dispatch_batch_size": 32, "max_dispatch_attempts": 5, "retry_delay_ms": 100, "lease_duration_ms": 5000, "max_timer_horizon_ms": 31_536_000_000_u64, "max_expression_bytes": 4096, "worker_id": worker, "max_signal_id_bytes": 256, "max_reference_bytes": 512, "max_subscriptions_per_instance": 128})
}

fn engine_config(
    manifest: &Manifest,
    mount: &str,
    index: usize,
    keys: &[&AuthKey],
    tls_certificate_sha256_hex: &str,
) -> Value {
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
            "internal_dispatch": {"actor_signing_key": path(&keys[2].private_path), "actor_signing_key_id": keys[2].id, "workload_signing_key": path(&keys[3].private_path), "workload_signing_key_id": keys[3].id, "actor_id": "engine-scheduler", "workload_id": "bpmp-engine", "roles": ["system"], "capabilities": ["boundary.trigger", "workflow.complete"], "proof_ttl_ms": 60000},
            "remote_workers": {
                "allowed_protocol_versions": ["1.0"],
                "identities": [{
                    "workload_id": "remote-worker-e2e",
                    "certificate_sha256_hex": tls_certificate_sha256_hex,
                    "tenant_ids": [manifest.tenant_id]
                }],
                "assignment_signing_key": path(&keys[3].private_path),
                "assignment_signing_key_id": keys[3].id,
                "max_registration_proof_ttl_ms": 60_000
            }
        },
        "payload_keys": [
            {"key_scope": format!("{}/operational", manifest.tenant_id), "key_version": "e2e-v1", "key_epoch": 1, "path": path("secrets/payload-operational.key")},
            {"key_scope": format!("{}/audit", manifest.tenant_id), "key_version": "e2e-v1", "key_epoch": 1, "path": path("secrets/payload-audit.key")}
        ],
        "rocksdb": {"max_open_files": 128, "write_buffer_size_bytes": 8_388_608, "max_background_jobs": 2, "max_replay_events": 10_000},
        "raft": {
            "cluster_name": "bpmp-e2e", "node_id": index + 1,
            "peer_listen_addr": manifest.engine_peer_listen_addresses[index],
            "peers": (0..3).map(|peer| json!({"node_id": peer + 1, "raft_address": manifest.engine_peer_addresses[peer], "tls_domain": format!("engine{}", peer + 1), "certificate_sha256_hex": tls_certificate_sha256_hex})).collect::<Vec<_>>(),
            "bootstrap": index == 0, "heartbeat_interval_ms": 100, "election_timeout_min_ms": 300, "election_timeout_max_ms": 600, "rpc_timeout_ms": 2000,
            "authorized_methods": [
                "/bpmp.raft.v1.RaftPeerService/AppendEntries",
                "/bpmp.raft.v1.RaftPeerService/Vote",
                "/bpmp.raft.v1.RaftPeerService/InstallSnapshot",
                "/bpmp.raft.v1.RaftPeerService/ForwardCommand",
                "/bpmp.raft.v1.RaftPeerService/AddLearner",
                "/bpmp.raft.v1.RaftPeerService/ChangeMembership"
            ],
            "max_conditions": 512, "max_mutations": 512, "max_batch_bytes": 4_194_304, "max_snapshot_bytes": 67_108_864,
            "max_inflight_proposals": 16, "max_prepare_attempts": 8,
            "proposal_timeout_ms": 4000,
            "snapshot_directory": format!("/data/engine-{}-snapshots", index + 1),
            "max_snapshot_transfer_bytes": 268_500_992,
            "snapshot_chunk_bytes": 131_072, "peer_max_message_bytes": 33_554_432,
            "append_only_column_families": ["events","dedup","outbox","idempotency","authorization_audit","compensation_ledger","governance_audit","raft_applied_commands"]
        },
        "grpc": {"max_decoding_bytes": 1_048_576, "max_encoding_bytes": 1_048_576, "request_timeout_ms": 5000, "admission_rate_rps": 5000, "admission_burst": 500, "reflection_enabled": true},
        "workers": {"poll_interval_ms": 100, "outbox_batch_size": 64, "outbox_max_attempts": 10, "outbox_initial_retry_ms": 50, "outbox_max_retry_ms": 1000, "outbox_retry_multiplier_millis": 2000, "boundary": boundary_config(&format!("engine-{}-boundary", index + 1)), "local_task_batch_size": 32, "local_task_max_attempts": 3, "local_task_initial_retry_ms": 25, "local_task_max_retry_ms": 250, "local_task_retry_multiplier_millis": 2000},
        "kafka": {
            "brokers": manifest.kafka.brokers,
            "client_id": format!("bpmp-engine-{}", index + 1),
            "security": {"protocol": manifest.kafka.security_protocol, "ca_location": null, "certificate_location": null, "key_location": null},
            "topics": {
                "committed_events": manifest.kafka.topics.engine_committed_events,
                "configuration_publications": manifest.kafka.topics.configuration_publications
            },
            "consumer_groups": {"configuration_reloader": manifest.kafka.consumer_groups.engine_configuration_reloaders[index]},
            "message_timeout_ms": 5000,
            "max_message_bytes": 1_048_576,
            "max_inflight": 1,
            "consumer_poll_timeout_ms": 250,
            "consumer_session_timeout_ms": 6000
        },
        "wasm_modules": []
    })
}

fn human_config(
    manifest: &Manifest,
    mount: &str,
    workload: &AuthKey,
    internal: &AuthKey,
    tls_fingerprint: &str,
) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.human_listen_address, "postgres_dsn": manifest.postgres_dsn,
        "apply_migrations": true, "migration_path": path("human-runtime.sql"),
        "engine_address": manifest.engine_public_addresses[1],
        "tls": {"server_certificate": path("secrets/tls.pem"), "server_private_key": path("secrets/tls-key.pem"), "client_certificate": path("secrets/tls.pem"), "client_private_key": path("secrets/tls-key.pem"), "client_ca": path("secrets/ca.pem"), "engine_ca": path("secrets/ca.pem"), "engine_server_name": "engine2", "configuration_server_name": "configuration-service"},
        "kafka": {"brokers": manifest.kafka.brokers, "committed_event_topic": manifest.kafka.topics.engine_committed_events, "escalation_topic": manifest.kafka.topics.human_escalations, "consumer_group": manifest.kafka.consumer_groups.human_committed_events},
        "identity": {"jwks_path": path("jwks.json"), "internal_keys": {(internal.id.clone()): path(&internal.public_path)}, "issuers": [manifest.actor_issuer], "audiences": [manifest.actor_audience], "allowed_jwt_methods": ["EdDSA"], "workload_id": "human-runtime", "max_proof_bytes": 16384, "max_jwks_keys": 16, "max_roles": 32, "max_capabilities": 64, "clock_skew_ms": 30000},
        "workload": {"id": "human-runtime", "signing_key_id": workload.id, "private_key_path": path(&workload.private_path), "proof_ttl_ms": 60000},
        "grpc": {
            "max_receive_bytes": 1_048_576, "max_send_bytes": 1_048_576,
            "unary_timeout_ms": 5000,
            "authorized_client_certificate_sha256": [tls_fingerprint],
            "authorized_methods": [
                "/bpmp.human.v1.HumanRuntimeService/GetWorkItem",
                "/bpmp.human.v1.HumanRuntimeService/ListWorkItems",
                "/bpmp.human.v1.HumanRuntimeService/CompleteWorkItem",
                "/bpmp.human.v1.HumanRuntimeService/DelegateWorkItem",
                "/bpmp.human.v1.HumanRuntimeService/GetCase",
                "/bpmp.human.v1.HumanRuntimeService/ListAuditRecords",
                "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
                "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"
            ],
            "admission_rate_rps": 2000,
            "admission_burst": 200,
            "reflection_enabled": true
        },
        "health": {"listen_address": manifest.human_health_address, "readiness_timeout_ms": 1000, "max_header_bytes": 32768},
        "telemetry": {"service_name": "human-runtime-e2e", "service_version": "e2e", "endpoint": manifest.otel_endpoint, "insecure": true, "sample_ratio": 0.0, "export_timeout_ms": 1000},
        "escalation": {"worker_id": "human-e2e"},
        "runtime_configuration": {
            "tenant_id": manifest.tenant_id,
            "resolver_address": manifest.configuration_grpc_url.trim_start_matches("https://"),
            "platform_reference": "bpmp",
            "environment_reference": "e2e",
            "resolve_timeout_ms": 5000,
            "kafka": {
                "brokers": manifest.kafka.brokers,
                "client_id": "bpmp-human-runtime-configuration",
                "security_protocol": manifest.kafka.security_protocol,
                "dial_timeout_ms": 2000,
                "request_timeout_ms": 5000,
                "topic": manifest.kafka.topics.configuration_publications,
                "consumer_group": manifest.kafka.consumer_groups.human_runtime_configuration_reloader,
                "batch_size": 64,
                "max_message_bytes": 1_048_576,
                "poll_timeout_ms": 250,
                "session_timeout_ms": 6000
            }
        }
    })
}

fn gateway_config(manifest: &Manifest, mount: &str, workload: &AuthKey) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.gateway_listen_address,
        "engine_address": manifest.engine_public_addresses[1], "human_address": manifest.human_address,
        "projection_address": manifest.projection_address,
        "configuration_url": manifest.configuration_url,
        "public_tls": {"certificate": path("secrets/tls.pem"), "private_key": path("secrets/tls-key.pem")},
        "upstream_tls": {"certificate": path("secrets/tls.pem"), "private_key": path("secrets/tls-key.pem"), "ca": path("secrets/ca.pem"), "engine_server_name": "engine2", "human_server_name": "human-runtime", "projection_server_name": "projection-service", "configuration_server_name": "configuration-service"},
        "identity": {"jwks_path": path("jwks.json"), "issuers": [manifest.actor_issuer], "audiences": [manifest.actor_audience], "algorithms": ["EdDSA"], "max_token_bytes": 16384, "max_jwks_keys": 16, "clock_skew_seconds": 30},
        "workload": {"id": "api-gateway", "signing_key_id": workload.id, "private_key_path": path(&workload.private_path), "proof_ttl_ms": 60000},
        "rate_limit": {"redis_address": manifest.redis_address, "redis_username": "", "redis_password_file": "", "redis_database": 0, "redis_key_prefix": "bpmp:e2e", "operation_timeout_ms": 1000},
        "http": {"read_header_timeout_ms": 2000, "request_timeout_ms": 5000, "read_timeout_ms": 5000, "write_timeout_ms": 5000, "idle_timeout_ms": 10000, "shutdown_timeout_ms": 5000, "max_header_bytes": 32768, "admission_rate_rps": 5000, "admission_burst": 500},
        "grpc": {"max_receive_bytes": 1_048_576, "max_send_bytes": 1_048_576},
        "health": {"readiness_timeout_ms": 1000},
        "telemetry": {"service_name": "api-gateway-e2e", "service_version": "e2e", "endpoint": manifest.otel_endpoint, "insecure": true, "sample_ratio": 0.0, "export_timeout_ms": 1000},
        "api_docs": {
            "enabled": true,
            "openapi_path": "/openapi/v1.json",
            "reference_path": "/docs",
            "scalar_script_url": "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.63.0"
        },
        "runtime_configuration": {
            "resolver_address": manifest.configuration_grpc_url.trim_start_matches("https://"),
            "platform_reference": "bpmp",
            "environment_reference": "e2e",
            "initial_tenant_ids": [manifest.tenant_id],
            "resolve_timeout_ms": 5000,
            "kafka": {
                "brokers": manifest.kafka.brokers,
                "client_id": "bpmp-api-gateway-configuration",
                "security_protocol": manifest.kafka.security_protocol,
                "dial_timeout_ms": 2000,
                "request_timeout_ms": 5000,
                "topic": manifest.kafka.topics.configuration_publications,
                "consumer_group": manifest.kafka.consumer_groups.api_gateway_configuration_reloader,
                "batch_size": 64,
                "max_message_bytes": 1_048_576,
                "poll_timeout_ms": 250,
                "session_timeout_ms": 6000
            }
        }
    })
}

fn projection_config(manifest: &Manifest, mount: &str, tls_fingerprint: &str) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.projection_listen_address,
        "health_address": manifest.projection_health_address,
        "postgres_dsn": manifest.projection_postgres_dsn,
        "apply_migrations": true,
        "migration_path": path("projection-service-migrations"),
        "tls": {
            "server_certificate": path("secrets/tls.pem"),
            "server_private_key": path("secrets/tls-key.pem"),
            "client_certificate": path("secrets/tls.pem"),
            "client_private_key": path("secrets/tls-key.pem"),
            "client_ca": path("secrets/ca.pem"),
            "configuration_ca": path("secrets/ca.pem"),
            "configuration_server_name": "configuration-service"
        },
        "grpc": {
            "max_receive_bytes": 1_048_576, "max_send_bytes": 1_048_576,
            "unary_timeout_ms": 5000,
            "authorized_client_certificate_sha256": [tls_fingerprint],
            "authorized_methods": [
                "/bpmp.projection.v1.ProjectionQueryService/GetWorkflowInstance",
                "/bpmp.projection.v1.ProjectionQueryService/ListWorkflowInstances",
                "/bpmp.projection.v1.ProjectionQueryService/QueryCompletedProcesses",
                "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
                "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"
            ],
            "admission_rate_rps": 2000,
            "admission_burst": 200,
            "reflection_enabled": true
        },
        "health": {"readiness_timeout_ms": 1000, "shutdown_timeout_ms": 5000, "max_header_bytes": 32768},
        "telemetry": {
            "service_name": "projection-service-e2e",
            "service_version": "e2e",
            "endpoint": manifest.otel_endpoint,
            "insecure": true,
            "sample_ratio": 0.0,
            "export_timeout_ms": 1000
        },
        "kafka": {
            "brokers": manifest.kafka.brokers,
            "client_id": "bpmp-projection-committed-events",
            "security_protocol": manifest.kafka.security_protocol,
            "dial_timeout_ms": 2000,
            "request_timeout_ms": 5000,
            "topic": manifest.kafka.topics.engine_committed_events,
            "consumer_group": manifest.kafka.consumer_groups.projection_committed_events,
            "batch_size": 128,
            "max_message_bytes": 1_048_576,
            "poll_timeout_ms": 250,
            "session_timeout_ms": 6000
        },
        "runtime_configuration": {
            "resolver_address": manifest.configuration_grpc_url.trim_start_matches("https://"),
            "platform_reference": "bpmp",
            "environment_reference": "e2e",
            "initial_tenant_ids": [manifest.tenant_id],
            "resolve_timeout_ms": 5000,
            "kafka": {
                "brokers": manifest.kafka.brokers,
                "client_id": "bpmp-projection-configuration",
                "security_protocol": manifest.kafka.security_protocol,
                "dial_timeout_ms": 2000,
                "request_timeout_ms": 5000,
                "topic": manifest.kafka.topics.configuration_publications,
                "consumer_group": manifest.kafka.consumer_groups.projection_configuration_reloader,
                "batch_size": 64,
                "max_message_bytes": 1_048_576,
                "poll_timeout_ms": 250,
                "session_timeout_ms": 6000
            }
        },
        "identity": {
            "jwks_path": path("jwks.json"),
            "issuers": [manifest.actor_issuer],
            "audiences": [manifest.actor_audience],
            "algorithms": ["EdDSA"],
            "max_token_bytes": 16384,
            "max_jwks_keys": 16,
            "max_capabilities": 64,
            "clock_skew_seconds": 30
        },
        "cursor_signing": {
            "keys": [{
                "id": "projection-cursor-v1",
                "secret_path": path("secrets/projection-cursor-v1.key")
            }]
        }
        ,"query_pdp": {"url":"http://authz-server:8080/authz/v1/query", "workload_token_path":path("secrets/projection-pdp.jwt"),"timeout_ms":5000,"max_response_bytes":1048576,"allow_insecure":true},
        "query_revoke": {
            "consumer":{"brokers":manifest.kafka.brokers,"client_id":"bpmp-projection-revoke-e2e","security_protocol":manifest.kafka.security_protocol,"dial_timeout_ms":2000,"request_timeout_ms":3000,"topic":manifest.kafka.topics.authorization_query_revocations,"consumer_group":manifest.kafka.consumer_groups.projection_query_revocations,"batch_size":64,"max_message_bytes":1048576,"poll_timeout_ms":250,"session_timeout_ms":6000},
            "dlq":{"brokers":manifest.kafka.brokers,"client_id":"bpmp-projection-revoke-dlq-e2e","security_protocol":manifest.kafka.security_protocol,"dial_timeout_ms":2000,"request_timeout_ms":3000,"topic":manifest.kafka.topics.authorization_query_revocations_dlq,"message_timeout_ms":2000,"max_inflight":1,"max_message_bytes":1048576,"required_acks":"ALL","enable_idempotence":true},
            "trust_keys":{"query-revoke-v1":path("secrets/query-revoke-v1.pub")}
        }
    })
}

fn cockpit_gateway_config(manifest: &Manifest, mount: &str) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_address": manifest.cockpit_gateway_listen_address,
        "health_address": manifest.cockpit_gateway_health_address,
        "tls": {
            "certificate": path("secrets/tls.pem"),
            "private_key": path("secrets/tls-key.pem")
        },
        "identity": {
            "jwks_path": path("jwks.json"),
            "issuers": [manifest.actor_issuer],
            "audiences": [manifest.actor_audience],
            "algorithms": ["EdDSA"],
            "max_token_bytes": 16384,
            "max_jwks_keys": 16,
            "clock_skew_seconds": 30
        },
        "http": {
            "read_header_timeout_ms": 5000,
            "request_timeout_ms": 5000,
            "idle_timeout_ms": 60000,
            "shutdown_timeout_ms": 10000,
            "max_header_bytes": 32768,
            "admission_rate_rps": 2000,
            "admission_burst": 200,
            "allowed_origins": [
                manifest.cockpit_web_public_origin,
                "https://localhost:4173"
            ]
        },
        "realtime": {
            "path": "/realtime/v1/events",
            "allowed_signal_names": [
                "workflow.changed",
                "work-item.changed",
                "case.changed",
                "audit.changed"
            ],
            "max_names_per_connection": 4,
            "max_signal_names_bytes": 256,
            "max_connections": 5000,
            "max_subscriptions": 20000,
            "outbound_buffer_size": 64,
            "replay_size_per_stream": 256,
            "max_replay_streams": 20000,
            "heartbeat_interval_ms": 15000
        },
        "health": {"readiness_timeout_ms": 1000, "max_header_bytes": 32768},
        "telemetry": {
            "service_name": "cockpit-gateway-e2e",
            "service_version": "e2e",
            "endpoint": manifest.otel_endpoint,
            "insecure": true,
            "sample_ratio": 0.0,
            "export_timeout_ms": 1000
        },
        "kafka": {
            "brokers": manifest.kafka.brokers,
            "client_id": "bpmp-cockpit-committed-events",
            "security_protocol": manifest.kafka.security_protocol,
            "dial_timeout_ms": 3000,
            "request_timeout_ms": 3000,
            "topic": manifest.kafka.topics.engine_committed_events,
            "consumer_group": manifest.kafka.consumer_groups.cockpit_committed_events,
            "batch_size": 64,
            "max_message_bytes": 1_048_576,
            "poll_timeout_ms": 500,
            "session_timeout_ms": 10000
        }
    })
}

fn cockpit_web_config(manifest: &Manifest) -> Value {
    json!({
        "apiBaseUrl": manifest.cockpit_web_public_origin,
        "organizationApiBaseUrl": manifest.cockpit_web_public_origin,
        "realtimeBaseUrl": manifest.cockpit_web_public_origin,
        "realtimePath": "/realtime/v1/events",
        "realtimeSignalNames": [
            "workflow.changed",
            "work-item.changed",
            "case.changed",
            "audit.changed"
        ],
        "realtimeReconnectInitialMs": 500,
        "realtimeReconnectMaxMs": 10000,
        "defaultPageSize": 50,
        "maxPageSize": 200,
        "batchChunkSize": 25,
        "batchConcurrency": 4,
        "requestTimeoutMs": 15000,
        "staleTimeMs": 5000
    })
}

fn cockpit_web_nginx_config() -> String {
    r#"events {}
http {
  include /etc/nginx/mime.types;
  default_type application/octet-stream;
  resolver 127.0.0.11 valid=10s ipv6=off;
  server {
    listen 8080;
    root /usr/share/nginx/html;

    location = /livez { default_type application/json; return 200 '{"status":"live"}'; }
    location = /readyz { default_type application/json; return 200 '{"status":"ready"}'; }

    location /v1/ {
      set $api_gateway https://api-gateway:8443;
      proxy_pass $api_gateway;
      proxy_ssl_server_name on;
      proxy_ssl_name api-gateway;
      proxy_ssl_trusted_certificate /runtime/secrets/ca.pem;
      proxy_ssl_verify on;
      proxy_set_header Host api-gateway;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /api/v1/ {
      set $authz_app http://authz-app:9090;
      proxy_pass $authz_app;
      proxy_set_header Host authz-app;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /realtime/ {
      set $cockpit_gateway https://cockpit-gateway:7601;
      proxy_pass $cockpit_gateway;
      proxy_ssl_server_name on;
      proxy_ssl_name cockpit-gateway;
      proxy_ssl_trusted_certificate /runtime/secrets/ca.pem;
      proxy_ssl_verify on;
      proxy_buffering off;
      proxy_cache off;
      proxy_read_timeout 1h;
      proxy_set_header Host cockpit-gateway;
    }

    location / {
      try_files $uri $uri/ /index.html;
    }
  }
}
"#
    .to_owned()
}

fn authz_server_env(manifest: &Manifest) -> String {
    format!(
        "SERVICE_NAME=authz-server-e2e\nHOST=0.0.0.0\nPORT=8080\nGRPC_PORT=50051\nSERVER_REQUEST_TIMEOUT_MS=5000\nSERVER_MAX_BODY_BYTES=1048576\nRATE_LIMIT_RPS=1000\nRATE_LIMIT_BURST=2000\nDATABASE_URL={}\nDB_MAX_CONNECTIONS=16\nDB_MIN_CONNECTIONS=1\nJWT_JWKS_URL=http://key-lifecycle:8080/jwks.json\nJWT_AUDIENCE={}\nFAIL_MODE=DENY\nKAFKA_BROKERS={}\nKAFKA_CLIENT_ID=bpmp-authz-control-plane-e2e\nKAFKA_SECURITY_PROTOCOL={}\nTENANT_LIFECYCLE_TOPIC={}\nTENANT_READINESS_TOPIC={}\nTENANT_READINESS_CONSUMER_GROUP=bpmp.authz.tenant-readiness.v1.e2e\nTENANT_LIFECYCLE_WORKER_ID=authz-e2e-1\nTENANT_LIFECYCLE_BATCH_SIZE=64\nTENANT_LIFECYCLE_LEASE_MS=5000\nTENANT_LIFECYCLE_POLL_MS=250\nKAFKA_MAX_MESSAGE_BYTES=1048576\n",
        manifest.authz_postgres_dsn,
        manifest.actor_audience,
        manifest.kafka.brokers.join(","),
        manifest.kafka.security_protocol,
        manifest.kafka.topics.tenant_lifecycle,
        manifest.kafka.topics.tenant_configuration_readiness,
    )
}

fn authz_app_env(manifest: &Manifest) -> String {
    format!(
        "SERVICE_NAME=authz-app-e2e\nHOST=0.0.0.0\nPORT=9090\nSERVER_REQUEST_TIMEOUT_MS=5000\nSERVER_MAX_BODY_BYTES=1048576\nRATE_LIMIT_RPS=1000\nRATE_LIMIT_BURST=2000\nDATABASE_URL={}\nDB_MAX_CONNECTIONS=16\nDB_MIN_CONNECTIONS=1\nAUTHZ_PDP_URL=http://authz-server:8080\nAUTHZ_SERVICE_TOKEN_FILE=/runtime/secrets/authz-app.jwt\nAUTHZ_TIMEOUT_MS=2000\nAUTHZ_CACHE_CAPACITY=10000\nAUTHZ_CACHE_TTL_SECS=5\nJWT_JWKS_URL=http://key-lifecycle:8080/jwks.json\nJWT_AUDIENCE={}\nKAFKA_BROKERS={}\nOUTBOX_POLL_INTERVAL_MS=250\nOUTBOX_BATCH_SIZE=64\n",
        manifest.authz_app_postgres_dsn,
        manifest.actor_audience,
        manifest.kafka.brokers.join(","),
    )
}

fn authz_seed_sql(manifest: &Manifest) -> String {
    format!(
        r#"CREATE SCHEMA IF NOT EXISTS authz_app;

INSERT INTO tenant (id, code, name, is_active)
VALUES ('{tenant}', 'e2e', 'BPMP E2E', true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO user_account (id, tenant_id, username, external_id)
VALUES ('{actor}', '{tenant}', 'e2e-user', '{actor}')
ON CONFLICT (id) DO NOTHING;

UPDATE tenant SET config=config || '{{"query_max_permissions":128}}'::jsonb WHERE id='{tenant}';

INSERT INTO role (id, tenant_id, code, name)
VALUES ('00000000-0000-4000-8000-000000000003', '{tenant}', 'organization-admin', 'Organization administrator')
ON CONFLICT (id) DO NOTHING;

INSERT INTO permission (id, tenant_id, code, resource_type, action, scope) VALUES
('00000000-0000-4000-8000-000000000011', '{tenant}', 'organization:organization:read', 'organization', 'organization:read', 'all'),
('00000000-0000-4000-8000-000000000012', '{tenant}', 'organization:organization:create', 'organization', 'organization:create', 'all'),
('00000000-0000-4000-8000-000000000013', '{tenant}', 'organization:organization:write', 'organization', 'organization:write', 'all'),
('00000000-0000-4000-8000-000000000014', '{tenant}', 'organization:organization:move', 'organization', 'organization:move', 'all')
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permission (role_id, permission_id)
SELECT '00000000-0000-4000-8000-000000000003', id
FROM permission
WHERE tenant_id = '{tenant}' AND resource_type = 'organization'
ON CONFLICT DO NOTHING;

INSERT INTO user_role (user_id, role_id, tenant_id)
VALUES ('{actor}', '00000000-0000-4000-8000-000000000003', '{tenant}')
ON CONFLICT DO NOTHING;

UPDATE user_account SET attributes=jsonb_build_object('actor_id',id::text,'grade',4,'team','group:query-reviewers') WHERE id='{actor}';
INSERT INTO user_account (id,tenant_id,username,external_id,attributes) VALUES
('00000000-0000-4000-8000-000000000099','{tenant}','query-other','00000000-0000-4000-8000-000000000099','{{"actor_id":"00000000-0000-4000-8000-000000000099","grade":4,"team":"group:query-reviewers"}}') ON CONFLICT DO NOTHING;
INSERT INTO permission (id,tenant_id,code,resource_type,action,scope) VALUES
('00000000-0000-4000-8000-000000000015','{tenant}','workflow_instance:query','workflow_instance','query','own') ON CONFLICT DO NOTHING;
INSERT INTO role_permission(role_id,permission_id) VALUES ('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000015') ON CONFLICT DO NOTHING;
INSERT INTO user_role(user_id,role_id,tenant_id) VALUES ('00000000-0000-4000-8000-000000000099','00000000-0000-4000-8000-000000000003','{tenant}') ON CONFLICT DO NOTHING;
INSERT INTO row_filter(permission_id,resource_type,filter_expr) VALUES
('00000000-0000-4000-8000-000000000015','workflow_instance','{{"operator":"LEAF","left":{{"type":"resource_field","key":"initiator_actor_id"}},"op":"eq","right":{{"type":"user_attr","key":"actor_id"}}}}');
INSERT INTO field_filter(permission_id,resource_type,allowed_fields,masked_fields) VALUES
('00000000-0000-4000-8000-000000000015','workflow_instance',ARRAY[]::varchar[],ARRAY['initiator_actor_id','event_actor_id']);
UPDATE role_permission SET conditions='{{"operator":"AND","conditions":[{{"operator":"LEAF","left":{{"type":"user_attr","key":"grade"}},"op":"gte","right":{{"type":"literal","value":3}}}},{{"operator":"LEAF","left":{{"type":"relation","key":"member_of","target":"user.team"}},"op":"exists","right":{{"type":"literal","value":true}}}}]}}' WHERE role_id='00000000-0000-4000-8000-000000000003' AND permission_id='00000000-0000-4000-8000-000000000015';
INSERT INTO relation_type(tenant_id,relation,max_fanout) VALUES ('{tenant}','member_of',100);
INSERT INTO relation_tuple(tenant_id,subject,relation,object) VALUES
('{tenant}','user:{actor}','member_of','group:query-reviewers'),
('{tenant}','user:00000000-0000-4000-8000-000000000099','member_of','group:query-reviewers');
"#,
        tenant = manifest.tenant_id,
        actor = manifest.actor_id,
    )
}

fn governance_config(manifest: &Manifest, mount: &str, tls_fingerprint: &str) -> Value {
    let path = |name: &str| format!("{mount}/{name}");
    json!({
        "listen_addr": manifest.governance_listen_address,
        "health_addr": manifest.governance_health_address,
        "postgres": {
            "dsn": manifest.governance_postgres_dsn,
            "max_connections": 16,
            "acquire_timeout_ms": 3000
        },
        "tls": {
            "server_certificate": path("secrets/tls.pem"),
            "server_private_key": path("secrets/tls-key.pem"),
            "client_ca": path("secrets/ca.pem")
        },
        "engines": manifest.engine_public_addresses.iter().map(|address| json!({
            "endpoint": format!("https://{address}"),
            "tls_domain": address.split(':').next().unwrap_or("localhost"),
            "timeout_ms": 3000,
            "connect_max_attempts": 30,
            "connect_retry_ms": 250
        })).collect::<Vec<_>>(),
        "configuration_resolver": {
            "endpoint": manifest.configuration_grpc_url,
            "tls_domain": "configuration-service",
            "platform_reference": "bpmp",
            "environment_reference": "e2e",
            "timeout_ms": 3000
        },
        "kafka": {
            "brokers": manifest.kafka.brokers,
            "client_id": "bpmp-governance-service-e2e",
            "consumer_group": manifest.kafka.consumer_groups.governance_configuration_reloader,
            "configuration_topic": manifest.kafka.topics.configuration_publications,
            "session_timeout_ms": 6000,
            "max_message_bytes": 1_048_576
        },
        "key_lifecycle": {
            "revocation_barrier_endpoint": "http://key-lifecycle:8080/barrier",
            "kms_endpoint": "http://key-lifecycle:8080/shred"
        },
        "worker": {
            "worker_id": "governance-e2e-1",
            "poll_interval_ms": 100,
            "shred_batch_size": 16,
            "shred_lease_ms": 5000
        },
        "grpc": {
            "max_decoding_bytes": 1_048_576,
            "max_encoding_bytes": 1_048_576,
            "request_timeout_ms": 5000,
            "authorized_client_certificate_sha256": [tls_fingerprint],
            "authorized_methods": [
                "/bpmp.governance.v1.GovernanceApprovalService/CreateApproval",
                "/bpmp.governance.v1.GovernanceApprovalService/RecordApproval",
                "/bpmp.governance.v1.GovernanceApprovalService/SubmitApproval",
                "/bpmp.governance.v1.GovernanceApprovalService/GetApproval",
                "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"
            ],
            "admission_rate_rps": 2000,
            "admission_burst": 200,
            "reflection_enabled": true
        }
    })
}

fn configuration_config(manifest: &Manifest, mount: &str, tls_fingerprint: &str) -> Value {
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
            "max_send_bytes": 1_048_576,
            "unary_timeout_ms": 5000,
            "authorized_client_certificate_sha256": [tls_fingerprint],
            "authorized_methods": [
                "/bpmp.configuration.v1.ConfigurationResolverService/ResolveConfiguration",
                "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
                "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"
            ],
            "admission_rate_rps": 5000,
            "admission_burst": 500,
            "reflection_enabled": true
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
        "tenant_lifecycle": {
            "brokers": manifest.kafka.brokers,
            "client_id": "bpmp-configuration-tenant-lifecycle",
            "security_protocol": manifest.kafka.security_protocol,
            "dial_timeout_ms": 2000,
            "request_timeout_ms": 5000,
            "topic": manifest.kafka.topics.tenant_lifecycle,
            "consumer_group": manifest.kafka.consumer_groups.configuration_tenant_lifecycle,
            "batch_size": 64,
            "max_message_bytes": 1_048_576,
            "poll_timeout_ms": 100,
            "session_timeout_ms": 10000
        },
        "tenant_readiness": {
            "brokers": manifest.kafka.brokers,
            "client_id": "bpmp-configuration-tenant-readiness",
            "security_protocol": manifest.kafka.security_protocol,
            "dial_timeout_ms": 2000,
            "request_timeout_ms": 5000,
            "topic": manifest.kafka.topics.tenant_configuration_readiness,
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
            "max_header_bytes": 32768,
            "read_header_timeout_ms": 2000,
            "request_timeout_ms": 5000,
            "read_timeout_ms": 5000,
            "write_timeout_ms": 5000,
            "idle_timeout_ms": 10000,
            "shutdown_timeout_ms": 5000,
            "readiness_timeout_ms": 1000,
            "admission_rate_rps": 2000,
            "admission_burst": 200
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
    let tenant = sql_literal(&manifest.tenant_id);
    let policies = seeded_configuration_policies(engine, manifest);
    let mut migration = format!(
        "{CONFIGURATION_MIGRATION}\n{CONFIGURATION_HOT_RELOAD_MIGRATION}\n\
         {CONFIGURATION_TENANT_READINESS_MIGRATION}\n\
         {CONFIGURATION_AUDIT_VERSIONS_MIGRATION}\n\
         {CONFIGURATION_OUTBOX_OBSERVABILITY_MIGRATION}\n"
    );
    for (owner, profile_id, version_id, policy) in policies {
        let raw = serde_json::to_vec(&policy)?;
        let values = sql_literal(std::str::from_utf8(&raw)?);
        let mut hash = String::with_capacity(64);
        for byte in Sha256::digest(&raw) {
            write!(&mut hash, "{byte:02x}")?;
        }
        let owner_slug = owner.to_ascii_lowercase().replace('_', "-");
        let policy_version = if owner == "ENGINE" {
            "policy-e2e-v1".to_owned()
        } else {
            format!("policy-{owner_slug}-e2e-v1")
        };
        write!(
            &mut migration,
            "INSERT INTO configuration_profiles(id,tenant_id,owner,name,scope_type,scope_reference,aggregate_version,is_deleted,created_at,created_by,updated_at,updated_by) VALUES\
             ('{profile_id}','{tenant}','{owner}','E2E {owner_slug} policy','TENANT','{tenant}',2,false,now(),'fixture',now(),'fixture');\n\
             INSERT INTO configuration_versions(id,profile_id,tenant_id,ordinal,config_version,policy_version,schema_version,status,values_json,content_hash,reason,created_at,created_by,published_at,published_by) VALUES\
             ('{version_id}','{profile_id}','{tenant}',1,'config-{owner_slug}-e2e-v1','{policy_version}',1,'PUBLISHED','{values}'::jsonb,decode('{hash}','hex'),'fixture bootstrap',now(),'fixture',now(),'fixture');\n\
             UPDATE configuration_profiles SET current_published_version_id='{version_id}' WHERE id='{profile_id}';\n\
             INSERT INTO configuration_active_scopes(tenant_id,owner,scope_type,scope_reference,profile_id,version_id,updated_at) VALUES\
             ('{tenant}','{owner}','TENANT','{tenant}','{profile_id}','{version_id}',now());\n"
        )?;
    }
    Ok(migration)
}

#[allow(clippy::too_many_lines)]
fn seeded_configuration_policies(
    engine: &Value,
    manifest: &Manifest,
) -> Vec<(&'static str, &'static str, &'static str, Value)> {
    let tenant_id = &manifest.tenant_id;
    let requester_key = SigningKey::from_bytes(&derive_key(manifest, "governance-requester"));
    let approver_key = SigningKey::from_bytes(&derive_key(manifest, "governance-approver"));
    vec![
        (
            "ENGINE",
            "00000000-0000-4000-8000-00000000c001",
            "00000000-0000-4000-8000-00000000c002",
            engine.clone(),
        ),
        (
            "API_GATEWAY",
            "00000000-0000-4000-8000-00000000c011",
            "00000000-0000-4000-8000-00000000c012",
            json!({
                "rate_limit_requests": 1000,
                "rate_limit_window_ms": "60000",
                "upstream_timeout_ms": "3000",
                "circuit_breaker_failure_threshold": 5,
                "circuit_breaker_open_ms": "1000",
                "bulkhead_max_concurrency": 128,
                "max_request_body_bytes": "65536",
                "max_upstream_response_bytes": "1048576",
                "batch_chunk_size": 100,
                "batch_concurrency": 4,
                "upstream_retry": {
                    "max_attempts": 3,
                    "initial_backoff_ms": "25",
                    "max_backoff_ms": "250",
                    "multiplier_millis": 2000
                },
                "encryption_key_scope": format!("{tenant_id}/operational")
            }),
        ),
        (
            "HUMAN_RUNTIME",
            "00000000-0000-4000-8000-00000000c021",
            "00000000-0000-4000-8000-00000000c022",
            json!({
                "projection_batch_size": 64,
                "escalation_batch_size": 32,
                "escalation_lease_ms": "5000",
                "escalation_retry_ms": "1000",
                "escalation_poll_ms": "250",
                "engine_command_timeout_ms": "3000",
                "max_assignment_candidates": 100,
                "max_delegation_depth": 8,
                "query_default_page_size": 50,
                "query_max_page_size": 200,
                "engine_retry": {
                    "max_attempts": 5,
                    "initial_backoff_ms": "50",
                    "max_backoff_ms": "1000",
                    "multiplier_millis": 2000
                },
                "engine_circuit_breaker_failure_threshold": 5,
                "engine_circuit_breaker_open_ms": "1000",
                "engine_retryable_codes": ["UNAVAILABLE", "DEADLINE_EXCEEDED"]
            }),
        ),
        (
            "PROJECTION",
            "00000000-0000-4000-8000-00000000c031",
            "00000000-0000-4000-8000-00000000c032",
            json!({
                "consume_batch_size": 64,
                "rebuild_batch_size": 256,
                "query_default_page_size": 50,
                "query_max_page_size": 200,
                "realtime_publish_batch_size": 64,
                "checkpoint_flush_ms": "1000",
                "max_projection_lag_ms": "30000",
                "query_max_filter_depth": 8,
                "query_max_predicates": 32,
                "query_max_sort_fields": 4,
                "query_max_relation_joins": 4,
                "query_max_in_values": 100,
                "query_timeout_ms": "3000",
                "query_max_string_bytes": 512,
                "query_cursor_signing_key_id": "projection-cursor-v1",
                "query_all_capability": "workflow.query.all",
                "query_own_capability": "workflow.query.own",
                "query_max_token_age_ms": "7200000",
                "query_pdp_resource_type":"workflow_instance",
                "query_pdp_action":"query",
                "query_revoke_retry_attempts":3,
                "query_revoke_retry_backoff_ms":"250",
                "query_revoke_operation_timeout_ms":"3000"
            }),
        ),
        (
            "GOVERNANCE",
            "00000000-0000-4000-8000-00000000c041",
            "00000000-0000-4000-8000-00000000c042",
            json!({
                "approval_ttl_ms": "300000",
                "fresh_authentication_max_age_ms": "60000",
                "kms_request_timeout_ms": "3000",
                "kms_retry": {
                    "max_attempts": 5,
                    "initial_backoff_ms": "50",
                    "max_backoff_ms": "1000",
                    "multiplier_millis": 2000
                },
                "key_cache_ttl_ms": "30000",
                "revocation_barrier_timeout_ms": "10000",
                "reconciliation_batch_size": 64,
                "max_pending_compensations": 1000,
                "abort_capability": "governance.abort_and_reconcile",
                "accepted_auth_assurance": ["mfa"],
                "approval_keys": [
                    {
                        "key_id": "governance-requester-v1",
                        "ed25519_public_key": base64::engine::general_purpose::STANDARD
                            .encode(requester_key.verifying_key().to_bytes()),
                        "enabled": true
                    },
                    {
                        "key_id": "governance-approver-v1",
                        "ed25519_public_key": base64::engine::general_purpose::STANDARD
                            .encode(approver_key.verifying_key().to_bytes()),
                        "enabled": true
                    }
                ],
                "required_approver_count": 1
            }),
        ),
        (
            "CONFIGURATION_SERVICE",
            "00000000-0000-4000-8000-00000000c051",
            "00000000-0000-4000-8000-00000000c052",
            json!({
                "outbox_batch_size": 64,
                "outbox_lease_ms": "10000",
                "outbox_poll_ms": "100",
                "outbox_retry": {
                    "max_attempts": 5,
                    "initial_backoff_ms": "100",
                    "max_backoff_ms": "5000",
                    "multiplier_millis": 2000
                },
                "query_default_page_size": 50,
                "query_max_page_size": 200,
                "max_request_body_bytes": "1048576"
            }),
        ),
        (
            "COCKPIT_GATEWAY",
            "00000000-0000-4000-8000-00000000c061",
            "00000000-0000-4000-8000-00000000c062",
            json!({
                "max_names_per_connection": 32,
                "max_signal_names_bytes": 4096,
                "max_connections": 1000,
                "max_subscriptions": 1000,
                "outbound_buffer_size": 256,
                "replay_size_per_stream": 100,
                "max_replay_streams": 1000,
                "heartbeat_interval_ms": "15000",
                "consume_batch_size": 128,
                "allowed_signal_names": ["workflow.updated", "work-item.updated"],
                "allowed_origins": [manifest.cockpit_web_public_origin]
            }),
        ),
        (
            "AUTHZ_CONTROL_PLANE",
            "00000000-0000-4000-8000-00000000c071",
            "00000000-0000-4000-8000-00000000c072",
            json!({
                "request_timeout_ms": "3000",
                "connect_timeout_ms": "1000",
                "http_pool_max_idle_per_host": 32,
                "graph_max_depth": 10,
                "graph_memo_capacity": "10000",
                "graph_memo_ttl_ms": "60000",
                "inactive_user_days": 60,
                "inactive_user_batch_size": 1000,
                "inactive_user_poll_ms": "86400000",
                "inactive_user_retry": {
                    "max_attempts": 3,
                    "initial_backoff_ms": "100",
                    "max_backoff_ms": "1000",
                    "multiplier_millis": 2000
                }
            }),
        ),
    ]
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
    let mut topic_names = std::collections::BTreeSet::new();
    for topic in [
        &manifest.kafka.topics.engine_committed_events,
        &manifest.kafka.topics.configuration_publications,
        &manifest.kafka.topics.human_escalations,
        &manifest.kafka.topics.tenant_lifecycle,
        &manifest.kafka.topics.tenant_configuration_readiness,
        &manifest.kafka.topics.authorization_query_revocations,
        &manifest.kafka.topics.authorization_query_revocations_dlq,
    ] {
        let parts = topic.split('.').collect::<Vec<_>>();
        if parts.len() != 5
            || parts[0] != "bpmp"
            || topic.len() > 249
            || parts.iter().any(|p| {
                p.is_empty()
                    || !p.as_bytes()[0].is_ascii_alphanumeric()
                    || !p
                        .bytes()
                        .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
            })
            || parts[3]
                .strip_prefix('v')
                .and_then(|v| v.parse::<u32>().ok())
                .is_none_or(|v| v == 0)
            || !topic_names.insert(topic)
        {
            anyhow::bail!("invalid or duplicate canonical Kafka topic name");
        }
    }
    for broker in &manifest.kafka.brokers {
        let (host, port) = broker
            .rsplit_once(':')
            .context("Kafka broker requires host:port")?;
        if host.is_empty()
            || !host
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b".-[]:".contains(&b))
            || port.parse::<u16>().ok().is_none_or(|p| p == 0)
        {
            anyhow::bail!("invalid Kafka bootstrap broker");
        }
    }
    let properties = &manifest.kafka.topic_properties;
    if properties.partitions == 0
        || properties.partitions > 1024
        || properties.replicas == 0
        || properties.replicas > 9
        || properties.min_in_sync_replicas == 0
        || properties.min_in_sync_replicas > properties.replicas
        || properties.retention_ms == 0
        || properties.max_message_bytes < 1024
        || properties.max_message_bytes > 100 * 1024 * 1024
        || manifest
            .kafka
            .topics
            .authorization_query_revocations
            .is_empty()
        || manifest
            .kafka
            .topics
            .authorization_query_revocations_dlq
            .is_empty()
        || manifest.kafka.topics.authorization_query_revocations
            == manifest.kafka.topics.authorization_query_revocations_dlq
        || manifest
            .kafka
            .consumer_groups
            .projection_query_revocations
            .is_empty()
    {
        anyhow::bail!("invalid bounded Kafka topic properties or revoke topology");
    }
    if manifest.engine_public_addresses.len() != 3
        || manifest.engine_peer_addresses.len() != 3
        || manifest.engine_peer_listen_addresses.len() != 3
        || manifest.kafka.brokers.is_empty()
        || manifest.tls_dns_names.is_empty()
        || manifest
            .kafka
            .consumer_groups
            .engine_configuration_reloaders
            .len()
            != 3
        || manifest
            .kafka
            .consumer_groups
            .projection_configuration_reloader
            .is_empty()
        || manifest
            .kafka
            .consumer_groups
            .projection_committed_events
            .is_empty()
        || manifest
            .kafka
            .consumer_groups
            .cockpit_committed_events
            .is_empty()
        || manifest
            .kafka
            .consumer_groups
            .governance_configuration_reloader
            .is_empty()
        || manifest
            .kafka
            .consumer_groups
            .configuration_tenant_lifecycle
            .is_empty()
        || manifest.kafka.topics.tenant_lifecycle.is_empty()
        || manifest
            .kafka
            .topics
            .tenant_configuration_readiness
            .is_empty()
        || manifest.governance_postgres_dsn.trim().is_empty()
        || manifest.governance_listen_address.trim().is_empty()
        || manifest.governance_health_address.trim().is_empty()
        || manifest.cockpit_gateway_listen_address.trim().is_empty()
        || manifest.cockpit_gateway_health_address.trim().is_empty()
        || manifest.cockpit_web_public_origin.trim().is_empty()
        || manifest.projection_address.trim().is_empty()
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
        &manifest.kafka.topics.tenant_lifecycle,
        &manifest.kafka.topics.tenant_configuration_readiness,
        &manifest.kafka.topics.authorization_query_revocations,
        &manifest.kafka.topics.authorization_query_revocations_dlq,
    ]
    .into_iter()
    .map(|topic| {
        format!(
            "rpk topic create '{topic}' --brokers '{brokers}' --partitions {} --replicas {} -c retention.ms={} -c max.message.bytes={} -c min.insync.replicas={} || rpk topic describe '{topic}' --brokers '{brokers}'",
            manifest.kafka.topic_properties.partitions,manifest.kafka.topic_properties.replicas,manifest.kafka.topic_properties.retention_ms,manifest.kafka.topic_properties.max_message_bytes,manifest.kafka.topic_properties.min_in_sync_replicas
        )
    })
    .collect::<Vec<_>>()
    .join("\n")
}

fn write(path: &Path, bytes: impl AsRef<[u8]>) -> Result<()> {
    fs::write(path, bytes).with_context(|| format!("write {}", path.display()))
}

fn schema_migration(schema: &str, migration: &str) -> Vec<u8> {
    format!(
        "CREATE SCHEMA IF NOT EXISTS {schema};\nSET search_path TO {schema}, public;\n{migration}",
        schema = quote_ident(schema),
    )
    .into_bytes()
}

fn quote_ident(value: &str) -> String {
    format!("\"{}\"", value.replace('"', "\"\""))
}

fn write_json(path: &Path, value: &Value) -> Result<()> {
    write(path, serde_json::to_vec_pretty(value)?)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn manifest() -> Manifest {
        serde_json::from_str(include_str!("../../../platform/e2e/manifest.json")).unwrap()
    }
    #[test]
    fn canonical_manifest_is_valid() {
        validate_manifest(&manifest()).unwrap();
    }
    #[test]
    fn rejects_duplicate_topics_and_unsafe_shell_names() {
        let mut value = manifest();
        value.kafka.topics.authorization_query_revocations_dlq =
            value.kafka.topics.authorization_query_revocations.clone();
        assert!(validate_manifest(&value).is_err());
        value = manifest();
        value.kafka.topics.authorization_query_revocations = "bpmp.authz.';evil.v1.test".into();
        assert!(validate_manifest(&value).is_err());
    }
    #[test]
    fn rejects_impossible_isr_and_unbounded_payload() {
        let mut value = manifest();
        value.kafka.topic_properties.min_in_sync_replicas =
            value.kafka.topic_properties.replicas + 1;
        assert!(validate_manifest(&value).is_err());
        value = manifest();
        value.kafka.topic_properties.max_message_bytes = u32::MAX;
        assert!(validate_manifest(&value).is_err());
    }
}
