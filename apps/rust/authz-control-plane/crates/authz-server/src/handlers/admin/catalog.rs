use crate::{
    error::ApiError,
    middleware::{request_id::RequestId, service_auth::ServicePrincipal},
    state::AppState,
};
use authz_core::AuthzError;
use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    Extension, Json,
};
use bpmp_authz_contracts::{
    authorization::v1::{
        AuthorizationPolicyBundle, AuthorizationPolicyEffect, AuthorizationResourceDefinition,
        AuthorizationResourceGrant,
    },
    AuthorizationArtifactLimits, AuthorizationBundleCodec,
};
use prost14::Message;
use serde::Deserialize;
use serde_json::Value;
use sha2::{Digest, Sha256};
use uuid::Uuid;

#[derive(Deserialize)]
pub struct ListCatalogVersionsQuery {
    pub tenant_id: Uuid,
}

#[derive(Deserialize)]
pub struct ListCatalogAuditQuery {
    pub tenant_id: Uuid,
    pub limit: Option<i64>,
}

#[derive(Deserialize)]
pub struct ListCatalogAcknowledgementsQuery {
    pub tenant_id: Uuid,
    pub catalog_version_id: Option<Uuid>,
    pub limit: Option<i64>,
}

#[derive(Deserialize)]
pub struct CreateCatalogDraftRequest {
    pub tenant_id: Uuid,
    pub snapshot: Value,
    pub reason: String,
}

#[derive(Deserialize)]
pub struct UpdateCatalogDraftRequest {
    pub tenant_id: Uuid,
    pub expected_draft_revision: i64,
    pub snapshot: Value,
    pub reason: String,
}

#[derive(Deserialize)]
pub struct ValidateCatalogQuery {
    pub tenant_id: Uuid,
}

#[derive(Deserialize)]
pub struct PublishCatalogRequest {
    pub tenant_id: Uuid,
    pub expected_version_num: i64,
    pub reason: String,
}

#[derive(Deserialize)]
pub struct RegisterOwnerManifestRequest {
    pub owner_id: String,
    pub manifest_version: String,
    pub entries: Vec<Value>,
}

#[derive(Deserialize)]
struct CatalogSnapshot {
    schema_version: u32,
    entries: Vec<CatalogEntry>,
    #[serde(default)]
    policy_grants: Vec<ResourcePolicyGrant>,
}

#[derive(Clone, Deserialize)]
struct CatalogEntry {
    owner_id: String,
    manifest_version: String,
    manifest_digest: String,
    resource_type: String,
    action: String,
    capability: String,
    #[serde(default)]
    enabled: bool,
    #[serde(default)]
    supported_scope_types: Vec<String>,
    #[serde(default)]
    readable_fields: Vec<String>,
    #[serde(default)]
    writable_fields: Vec<String>,
}

#[derive(Deserialize)]
struct ResourcePolicyGrant {
    grant_id: String,
    owner_id: String,
    #[serde(default)]
    actor_ids: Vec<String>,
    #[serde(default)]
    roles: Vec<String>,
    #[serde(default)]
    required_capabilities: Vec<String>,
    resource_type: String,
    action: String,
    effect: String,
    #[serde(default)]
    branch_scope_ids: Vec<String>,
    #[serde(default)]
    branch_includes_descendants: bool,
    #[serde(default)]
    queue_scope_ids: Vec<String>,
    #[serde(default)]
    own_scope: bool,
    #[serde(default)]
    resource_scope_ids: Vec<String>,
    #[serde(default)]
    tenant_scope: bool,
    #[serde(default)]
    readable_fields: Vec<String>,
    #[serde(default)]
    writable_fields: Vec<String>,
}

pub async fn list_versions(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<ListCatalogVersionsQuery>,
) -> Result<Json<Vec<Value>>, ApiError> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.read")?;
    let rows = sqlx::query(
        r#"SELECT version.id, version.version_num, version.draft_revision, version.state,
                  version.snapshot, version.content_hash, version.signing_key_id,
                  version.created_by, version.created_at, version.validated_by, version.validated_at,
                  version.published_by, version.published_at,
                  latest.bundle_sequence, latest.bundle_expires_at_epoch_ms
           FROM authorization_catalog_version version
           LEFT JOIN LATERAL (
               SELECT bundle_sequence, bundle_expires_at_epoch_ms
               FROM authorization_policy_bundle_outbox
               WHERE catalog_version_id = version.id
               ORDER BY bundle_sequence DESC LIMIT 1
           ) latest ON TRUE
           WHERE version.tenant_id = $1
           ORDER BY version.version_num DESC
           LIMIT 100"#,
    )
    .bind(query.tenant_id)
    .fetch_all(&state.pool)
    .await?;

    let versions = rows
        .into_iter()
        .map(|row| {
            use sqlx::Row;
            serde_json::json!({
                "id": row.get::<Uuid, _>("id"),
                "version_num": row.get::<i64, _>("version_num"),
                "draft_revision": row.get::<i64, _>("draft_revision"),
                "state": row.get::<String, _>("state"),
                "snapshot": row.get::<Value, _>("snapshot"),
                "content_hash": row.try_get::<Option<Vec<u8>>, _>("content_hash").ok().flatten(),
                "signing_key_id": row.get::<Option<String>, _>("signing_key_id"),
                "created_by": row.get::<String, _>("created_by"),
                "created_at": row.get::<chrono::DateTime<chrono::Utc>, _>("created_at"),
                "validated_by": row.get::<Option<String>, _>("validated_by"),
                "validated_at": row.get::<Option<chrono::DateTime<chrono::Utc>>, _>("validated_at"),
                "published_by": row.get::<Option<String>, _>("published_by"),
                "published_at": row.get::<Option<chrono::DateTime<chrono::Utc>>, _>("published_at"),
                "bundle_sequence": row.try_get::<Option<i64>, _>("bundle_sequence").ok().flatten(),
                "bundle_expires_at_epoch_ms": row.try_get::<Option<i64>, _>("bundle_expires_at_epoch_ms").ok().flatten(),
            })
        })
        .collect();
    Ok(Json(versions))
}

pub async fn list_audit(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<ListCatalogAuditQuery>,
) -> Result<Json<Vec<Value>>, ApiError> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.read")?;
    let limit = query.limit.unwrap_or(100);
    if !(1..=200).contains(&limit) {
        return Err(invalid_snapshot("audit limit must be between 1 and 200"));
    }
    let rows = sqlx::query(
        r#"SELECT audit.catalog_version_id, version.version_num, audit.actor_ref,
                  audit.request_id, audit.action, audit.reason, audit.occurred_at
           FROM authorization_catalog_audit audit
           JOIN authorization_catalog_version version ON version.id = audit.catalog_version_id
           WHERE audit.tenant_id = $1
           ORDER BY audit.occurred_at DESC, audit.id DESC
           LIMIT $2"#,
    )
    .bind(query.tenant_id)
    .bind(limit)
    .fetch_all(&state.pool)
    .await?;
    use sqlx::Row;
    Ok(Json(
        rows.into_iter()
            .map(|row| {
                serde_json::json!({
                    "catalog_version_id": row.get::<Uuid, _>("catalog_version_id"),
                    "version_num": row.get::<i64, _>("version_num"),
                    "actor_ref": row.get::<String, _>("actor_ref"),
                    "request_id": row.get::<String, _>("request_id"),
                    "action": row.get::<String, _>("action"),
                    "reason": row.get::<Option<String>, _>("reason"),
                    "occurred_at": row.get::<chrono::DateTime<chrono::Utc>, _>("occurred_at"),
                })
            })
            .collect(),
    ))
}

pub async fn list_acknowledgements(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<ListCatalogAcknowledgementsQuery>,
) -> Result<Json<Vec<Value>>, ApiError> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.read")?;
    let limit = query.limit.unwrap_or(100);
    if !(1..=500).contains(&limit) {
        return Err(invalid_snapshot(
            "acknowledgement limit must be between 1 and 500",
        ));
    }
    let rows = sqlx::query(
        r#"SELECT ack.catalog_version_id, version.version_num, outbox.bundle_sequence,
                  ack.consumer_id, ack.consumer_type, encode(ack.bundle_content_hash, 'hex') AS bundle_content_hash,
                  ack.acknowledged_at_epoch_ms
           FROM authorization_policy_bundle_acknowledgement ack
           JOIN authorization_catalog_version version ON version.id = ack.catalog_version_id
           JOIN authorization_policy_bundle_outbox outbox
             ON outbox.tenant_id = ack.tenant_id AND outbox.bundle_sequence = ack.bundle_sequence
           WHERE ack.tenant_id = $1 AND ($2::uuid IS NULL OR ack.catalog_version_id = $2)
           ORDER BY ack.acknowledged_at_epoch_ms DESC, ack.consumer_id
           LIMIT $3"#,
    )
    .bind(query.tenant_id)
    .bind(query.catalog_version_id)
    .bind(limit)
    .fetch_all(&state.pool)
    .await?;
    use sqlx::Row;
    Ok(Json(
        rows.into_iter()
            .map(|row| {
                serde_json::json!({
                    "catalog_version_id": row.get::<Uuid, _>("catalog_version_id"),
                    "version_num": row.get::<i64, _>("version_num"),
                    "bundle_sequence": row.get::<i64, _>("bundle_sequence"),
                    "consumer_id": row.get::<String, _>("consumer_id"),
                    "consumer_type": row.get::<String, _>("consumer_type"),
                    "bundle_content_hash": row.get::<String, _>("bundle_content_hash"),
                    "acknowledged_at_epoch_ms": row.get::<i64, _>("acknowledged_at_epoch_ms"),
                })
            })
            .collect(),
    ))
}

pub async fn list_owner_entries(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<ListCatalogVersionsQuery>,
) -> Result<Json<Vec<Value>>, ApiError> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.read")?;
    let manifests = sqlx::query(
        "SELECT owner_id, manifest_version, manifest_digest, entries FROM authorization_owner_manifest WHERE state = 'ACTIVE' ORDER BY owner_id",
    )
    .fetch_all(&state.pool)
    .await?;
    use sqlx::Row;
    let mut result = Vec::new();
    for manifest in manifests {
        let owner_id: String = manifest.get("owner_id");
        let manifest_version: String = manifest.get("manifest_version");
        let manifest_digest: Vec<u8> = manifest.get("manifest_digest");
        let entries: Value = manifest.get("entries");
        for entry in entries.as_array().into_iter().flatten() {
            let mut entry = entry.clone();
            if let Some(object) = entry.as_object_mut() {
                object.insert("owner_id".into(), Value::String(owner_id.clone()));
                object.insert(
                    "manifest_version".into(),
                    Value::String(manifest_version.clone()),
                );
                object.insert(
                    "manifest_digest".into(),
                    Value::String(
                        manifest_digest
                            .iter()
                            .map(|byte| format!("{byte:02x}"))
                            .collect(),
                    ),
                );
            }
            result.push(entry);
        }
    }
    Ok(Json(result))
}

pub async fn list_roles(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<ListCatalogVersionsQuery>,
) -> Result<Json<Vec<Value>>, ApiError> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.read")?;
    let rows =
        sqlx::query("SELECT code, name FROM role WHERE tenant_id = $1 ORDER BY code LIMIT 1000")
            .bind(query.tenant_id)
            .fetch_all(&state.pool)
            .await?;
    use sqlx::Row;
    Ok(Json(
        rows.into_iter()
            .map(|row| {
                serde_json::json!({
                    "code": row.get::<String, _>("code"),
                    "name": row.get::<String, _>("name"),
                })
            })
            .collect(),
    ))
}

pub async fn create_draft(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Json(payload): Json<CreateCatalogDraftRequest>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    authorize_tenant(&principal, payload.tenant_id, "authz.catalog.write")?;
    validate_change_reason(&payload.reason)?;
    validate_snapshot(&payload.snapshot)?;

    let mut transaction = state.pool.begin().await?;
    sqlx::query("SELECT id FROM tenant WHERE id = $1 FOR UPDATE")
        .bind(payload.tenant_id)
        .fetch_one(&mut *transaction)
        .await?;
    let version_num: i64 = sqlx::query_scalar(
        "SELECT COALESCE(MAX(version_num), 0) + 1 FROM authorization_catalog_version WHERE tenant_id = $1",
    )
    .bind(payload.tenant_id)
    .fetch_one(&mut *transaction)
    .await?;
    let id = Uuid::new_v4();
    sqlx::query(
        r#"INSERT INTO authorization_catalog_version
               (id, tenant_id, version_num, state, snapshot, created_by)
           VALUES ($1, $2, $3, 'DRAFT', $4, $5)"#,
    )
    .bind(id)
    .bind(payload.tenant_id)
    .bind(version_num)
    .bind(&payload.snapshot)
    .bind(&principal.subject)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        r#"INSERT INTO authorization_catalog_audit
               (tenant_id, catalog_version_id, actor_ref, request_id, action, reason)
           VALUES ($1, $2, $3, $4, 'CREATE_DRAFT', $5)"#,
    )
    .bind(payload.tenant_id)
    .bind(id)
    .bind(&principal.subject)
    .bind(&request_id.0)
    .bind(&payload.reason)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;

    Ok((
        StatusCode::CREATED,
        Json(
            serde_json::json!({ "id": id, "version_num": version_num, "draft_revision": 1, "state": "DRAFT" }),
        ),
    ))
}

pub async fn update_draft(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Path(version_id): Path<Uuid>,
    Json(payload): Json<UpdateCatalogDraftRequest>,
) -> Result<Json<Value>, ApiError> {
    authorize_tenant(&principal, payload.tenant_id, "authz.catalog.write")?;
    validate_change_reason(&payload.reason)?;
    validate_snapshot(&payload.snapshot)?;

    let mut transaction = state.pool.begin().await?;
    let row = sqlx::query(
        "SELECT version_num, draft_revision, state FROM authorization_catalog_version WHERE id = $1 AND tenant_id = $2 FOR UPDATE",
    )
    .bind(version_id)
    .bind(payload.tenant_id)
    .fetch_optional(&mut *transaction)
    .await?
    .ok_or_else(|| invalid_snapshot("catalog draft not found"))?;
    use sqlx::Row;
    let version_num: i64 = row.get("version_num");
    let draft_revision: i64 = row.get("draft_revision");
    let status: String = row.get("state");
    if draft_revision != payload.expected_draft_revision {
        return Err(AuthzError::VersionConflict {
            entity: "authorization catalog version",
            entity_id: version_id,
            expected_version: payload.expected_draft_revision,
            actual_version: draft_revision,
        }
        .into());
    }
    if status != "DRAFT" {
        return Err(invalid_snapshot("only a draft catalog can be edited"));
    }
    validate_owner_entries(&mut transaction, &payload.snapshot).await?;
    validate_policy_grants(&payload.snapshot)?;
    sqlx::query(
        "UPDATE authorization_catalog_version SET snapshot = $1, draft_revision = draft_revision + 1 WHERE id = $2 AND tenant_id = $3 AND state = 'DRAFT'",
    )
    .bind(&payload.snapshot)
    .bind(version_id)
    .bind(payload.tenant_id)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO authorization_catalog_audit (tenant_id, catalog_version_id, actor_ref, request_id, action, reason) VALUES ($1, $2, $3, $4, 'UPDATE_DRAFT', $5)",
    )
    .bind(payload.tenant_id)
    .bind(version_id)
    .bind(&principal.subject)
    .bind(&request_id.0)
    .bind(&payload.reason)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok(Json(
        serde_json::json!({ "id": version_id, "version_num": version_num, "draft_revision": draft_revision + 1, "state": "DRAFT" }),
    ))
}

pub async fn register_owner_manifest(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Json(payload): Json<RegisterOwnerManifestRequest>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    if principal.subject != payload.owner_id
        || !state
            .owner_manifest_key_ids
            .get(&payload.owner_id)
            .is_some_and(|key_ids| key_ids.contains(&principal.signing_key_id))
        || !principal
            .capabilities
            .iter()
            .any(|capability| capability == "authz.catalog.owner-register")
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: "authorization_owner_manifest_registration".into(),
            reason: "owner manifest must be registered by its authenticated owner workload".into(),
        }
        .into());
    }
    validate_owner_manifest(&payload)?;
    let entries_json = serde_json::to_value(&payload.entries)
        .map_err(|_| invalid_snapshot("owner manifest cannot be serialized"))?;
    let canonical = serde_json::to_vec(&entries_json)
        .map_err(|_| invalid_snapshot("owner manifest cannot be serialized"))?;
    let digest = Sha256::digest(canonical);
    let digest_hex = digest
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let mut transaction = state.pool.begin().await?;
    sqlx::query("SELECT pg_advisory_xact_lock(hashtext($1))")
        .bind(&payload.owner_id)
        .execute(&mut *transaction)
        .await?;
    let active = sqlx::query(
        "SELECT id, manifest_version, manifest_digest FROM authorization_owner_manifest WHERE owner_id = $1 AND state = 'ACTIVE' FOR UPDATE",
    )
    .bind(&payload.owner_id)
    .fetch_optional(&mut *transaction)
    .await?;
    if let Some(active) = active {
        use sqlx::Row;
        let active_id: Uuid = active.get("id");
        let active_version: String = active.get("manifest_version");
        let active_digest: Vec<u8> = active.get("manifest_digest");
        if active_version == payload.manifest_version {
            if active_digest.as_slice() != digest.as_slice() {
                return Err(invalid_snapshot(
                    "owner manifest content changed without a manifest version change",
                ));
            }
            transaction.commit().await?;
            return Ok((
                StatusCode::OK,
                Json(serde_json::json!({
                    "id": active_id,
                    "owner_id": payload.owner_id,
                    "manifest_version": payload.manifest_version,
                    "manifest_digest": digest_hex,
                    "state": "ACTIVE",
                })),
            ));
        }
    }
    let prior_version = sqlx::query(
        "SELECT id FROM authorization_owner_manifest WHERE owner_id = $1 AND manifest_version = $2",
    )
    .bind(&payload.owner_id)
    .bind(&payload.manifest_version)
    .fetch_optional(&mut *transaction)
    .await?;
    if prior_version.is_some() {
        return Err(invalid_snapshot(
            "owner manifest versions are immutable and cannot be reused after retirement",
        ));
    }
    sqlx::query("UPDATE authorization_owner_manifest SET state = 'RETIRED' WHERE owner_id = $1 AND state = 'ACTIVE'")
        .bind(&payload.owner_id)
        .execute(&mut *transaction)
        .await?;
    let manifest_id = Uuid::new_v4();
    sqlx::query(
        "INSERT INTO authorization_owner_manifest (id, owner_id, manifest_version, manifest_digest, entries, state, registered_by) VALUES ($1, $2, $3, $4, $5, 'ACTIVE', $6)",
    )
    .bind(manifest_id)
    .bind(&payload.owner_id)
    .bind(&payload.manifest_version)
    .bind(digest.as_slice())
    .bind(&entries_json)
    .bind(&principal.subject)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO authorization_owner_manifest_audit (manifest_id, owner_id, actor_ref, request_id, action) VALUES ($1, $2, $3, $4, 'REGISTER')",
    )
    .bind(manifest_id)
    .bind(&payload.owner_id)
    .bind(actor_ref(&principal))
    .bind(&request_id.0)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok((
        StatusCode::CREATED,
        Json(serde_json::json!({
            "id": manifest_id,
            "owner_id": payload.owner_id,
            "manifest_version": payload.manifest_version,
            "manifest_digest": digest_hex,
            "state": "ACTIVE",
        })),
    ))
}

pub async fn validate_version(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Path(version_id): Path<Uuid>,
    Query(query): Query<ValidateCatalogQuery>,
) -> Result<Json<Value>, ApiError> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.write")?;
    let mut transaction = state.pool.begin().await?;
    let row = sqlx::query(
        "SELECT version_num, state, snapshot FROM authorization_catalog_version WHERE id = $1 AND tenant_id = $2 FOR UPDATE",
    )
    .bind(version_id)
    .bind(query.tenant_id)
    .fetch_optional(&mut *transaction)
    .await?
    .ok_or_else(|| invalid_snapshot("catalog version not found"))?;
    use sqlx::Row;
    let state_name: String = row.get("state");
    if state_name != "DRAFT" && state_name != "VALIDATED" {
        return Err(invalid_snapshot("only a draft catalog can be validated"));
    }
    let snapshot: Value = row.get("snapshot");
    validate_snapshot(&snapshot)?;
    validate_owner_entries(&mut transaction, &snapshot).await?;
    validate_policy_grants(&snapshot)?;
    validate_role_references(&mut transaction, query.tenant_id, &snapshot).await?;
    validate_branch_scope_ids(&mut transaction, query.tenant_id, &snapshot).await?;
    sqlx::query(
        "UPDATE authorization_catalog_version SET state = 'VALIDATED', validated_by = $1, validated_at = now() WHERE id = $2",
    )
    .bind(&principal.subject)
    .bind(version_id)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO authorization_catalog_audit (tenant_id, catalog_version_id, actor_ref, request_id, action) VALUES ($1, $2, $3, $4, 'VALIDATE')",
    )
    .bind(query.tenant_id)
    .bind(version_id)
    .bind(actor_ref(&principal))
    .bind(&request_id.0)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok(Json(
        serde_json::json!({"id": version_id, "state": "VALIDATED"}),
    ))
}

pub async fn publish_version(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Path(version_id): Path<Uuid>,
    Json(payload): Json<PublishCatalogRequest>,
) -> Result<Json<Value>, ApiError> {
    authorize_tenant(&principal, payload.tenant_id, "authz.catalog.publish")?;
    if payload.reason.trim().is_empty() || payload.reason.len() > 1000 {
        return Err(invalid_snapshot("publish reason must be 1-1000 bytes"));
    }
    let signing =
        state
            .policy_bundle_signing
            .as_ref()
            .ok_or_else(|| AuthzError::InvalidRequest {
                reason: "policy bundle signing is not configured".into(),
            })?;

    let mut transaction = state.pool.begin().await?;
    sqlx::query("SELECT id FROM tenant WHERE id = $1 FOR UPDATE")
        .bind(payload.tenant_id)
        .fetch_one(&mut *transaction)
        .await?;
    use sqlx::Row;
    let row = sqlx::query(
        "SELECT version_num, state, snapshot FROM authorization_catalog_version WHERE id = $1 AND tenant_id = $2 FOR UPDATE",
    )
    .bind(version_id)
    .bind(payload.tenant_id)
    .fetch_optional(&mut *transaction)
    .await?
    .ok_or_else(|| invalid_snapshot("catalog version not found"))?;
    let version_num: i64 = row.get("version_num");
    let state_name: String = row.get("state");
    let snapshot: Value = row.get("snapshot");
    if state_name != "VALIDATED" || version_num != payload.expected_version_num {
        return Err(AuthzError::VersionConflict {
            entity: "authorization catalog",
            entity_id: version_id,
            expected_version: payload.expected_version_num,
            actual_version: version_num,
        }
        .into());
    }
    let current_sequence: Option<i64> = sqlx::query_scalar(
        "SELECT MAX(bundle_sequence) FROM authorization_policy_bundle_outbox WHERE tenant_id = $1",
    )
    .bind(payload.tenant_id)
    .fetch_one(&mut *transaction)
    .await?;
    // Sequence 1 is reserved for the legacy bootstrap artifact. Policy bundle
    // sequence is independent from catalog version because validity renewals
    // emit new signed bundles without changing the policy snapshot.
    let bundle_sequence = u64::try_from(current_sequence.unwrap_or(1))
        .map_err(|_| invalid_snapshot("bundle sequence is invalid"))?
        .checked_add(1)
        .ok_or_else(|| invalid_snapshot("bundle sequence overflow"))?;
    let bundle_sequence_db =
        i64::try_from(bundle_sequence).map_err(|_| invalid_snapshot("bundle sequence overflow"))?;
    validate_owner_entries(&mut transaction, &snapshot).await?;
    validate_policy_grants(&snapshot)?;
    validate_role_references(&mut transaction, payload.tenant_id, &snapshot).await?;
    validate_branch_scope_ids(&mut transaction, payload.tenant_id, &snapshot).await?;
    let now = chrono::Utc::now().timestamp_millis().max(1) as u64;
    let expires_at = now.saturating_add(crate::policy_bundle_publisher::POLICY_BUNDLE_TTL_MS);
    let bundle = AuthorizationPolicyBundle {
        schema_version: bpmp_authz_contracts::AUTHORIZATION_BUNDLE_SCHEMA_VERSION,
        tenant_id: payload.tenant_id.to_string(),
        bundle_sequence,
        policy_version: format!("access-v{version_num}"),
        revoke_epoch: 0,
        valid_from_epoch_ms: now.saturating_sub(5_000),
        expires_at_epoch_ms: expires_at,
        grants: Vec::new(),
        catalog_version: version_num as u64,
        resource_grants: resource_grants(&snapshot)?,
        resource_catalog: resource_catalog(&snapshot)?,
        actor_revoke_epochs: Vec::new(),
        signing_key_id: String::new(),
        content_hash: Vec::new(),
        signature: Vec::new(),
    };
    let limits = AuthorizationArtifactLimits::new(state.policy_bundle_max_bytes, 10_000)
        .map_err(|error| invalid_snapshot(&error.to_string()))?;
    let bundle_bytes =
        AuthorizationBundleCodec::seal(bundle, &signing.key_id, signing.signer.as_ref(), limits)
            .map_err(|error| invalid_snapshot(&error.to_string()))?;
    let signed_bundle = AuthorizationPolicyBundle::decode(bundle_bytes.as_slice())
        .map_err(|error| invalid_snapshot(&error.to_string()))?;

    sqlx::query("UPDATE authorization_catalog_version SET state = 'RETIRED' WHERE tenant_id = $1 AND state = 'PUBLISHED'")
        .bind(payload.tenant_id)
        .execute(&mut *transaction)
        .await?;
    sqlx::query(
        r#"UPDATE authorization_catalog_version
           SET state = 'PUBLISHED', published_by = $1, published_at = now(),
               content_hash = $2, signing_key_id = $3, signature = $4, bundle_bytes = $5
           WHERE id = $6 AND tenant_id = $7"#,
    )
    .bind(actor_ref(&principal))
    .bind(signed_bundle.content_hash)
    .bind(&signed_bundle.signing_key_id)
    .bind(signed_bundle.signature)
    .bind(&bundle_bytes)
    .bind(version_id)
    .bind(payload.tenant_id)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO authorization_policy_bundle_outbox (tenant_id, catalog_version_id, bundle_sequence, bundle_bytes, bundle_expires_at_epoch_ms) VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(payload.tenant_id)
    .bind(version_id)
    .bind(bundle_sequence_db)
    .bind(&bundle_bytes)
    .bind(i64::try_from(expires_at).map_err(|_| invalid_snapshot("bundle expiry timestamp overflow"))?)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO authorization_catalog_audit (tenant_id, catalog_version_id, actor_ref, request_id, action, reason) VALUES ($1, $2, $3, $4, 'PUBLISH', $5)",
    )
    .bind(payload.tenant_id)
    .bind(version_id)
    .bind(actor_ref(&principal))
    .bind(&request_id.0)
    .bind(payload.reason.trim())
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;

    Ok(Json(serde_json::json!({
        "id": version_id,
        "version_num": version_num,
        "state": "PUBLISHED",
        "policy_version": format!("access-v{version_num}"),
        "bundle_sequence": bundle_sequence,
        "catalog_version": version_num,
        "signing_key_id": signing.key_id,
    })))
}

pub async fn download_bundle(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Path(version_id): Path<Uuid>,
    Query(query): Query<ValidateCatalogQuery>,
) -> Result<
    (
        StatusCode,
        [(axum::http::header::HeaderName, &'static str); 2],
        Vec<u8>,
    ),
    ApiError,
> {
    authorize_tenant(&principal, query.tenant_id, "authz.catalog.read")?;
    let bundle = sqlx::query_scalar::<_, Option<Vec<u8>>>(
        r#"SELECT outbox.bundle_bytes
           FROM authorization_catalog_version version
           JOIN authorization_policy_bundle_outbox outbox
             ON outbox.tenant_id = version.tenant_id AND outbox.catalog_version_id = version.id
           WHERE version.id = $1 AND version.tenant_id = $2 AND version.state = 'PUBLISHED'
           ORDER BY outbox.bundle_sequence DESC
           LIMIT 1"#,
    )
    .bind(version_id)
    .bind(query.tenant_id)
    .fetch_optional(&state.pool)
    .await?
    .flatten()
    .ok_or_else(|| invalid_snapshot("published authorization bundle not found"))?;
    Ok((
        StatusCode::OK,
        [
            (axum::http::header::CONTENT_TYPE, "application/octet-stream"),
            (axum::http::header::CACHE_CONTROL, "no-store"),
        ],
        bundle,
    ))
}

async fn validate_owner_entries(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    snapshot: &Value,
) -> Result<(), ApiError> {
    let parsed: CatalogSnapshot = serde_json::from_value(snapshot.clone())
        .map_err(|_| invalid_snapshot("catalog snapshot schema is invalid"))?;
    for entry in parsed.entries {
        let manifest = sqlx::query(
            "SELECT manifest_digest, entries FROM authorization_owner_manifest WHERE owner_id = $1 AND manifest_version = $2 AND state = 'ACTIVE'",
        )
        .bind(&entry.owner_id)
        .bind(&entry.manifest_version)
        .fetch_optional(&mut **transaction)
        .await?;
        let Some(manifest) = manifest else {
            return Err(invalid_snapshot(
                "catalog entry references an unregistered or inactive owner manifest",
            ));
        };
        use sqlx::Row;
        let manifest_digest: Vec<u8> = manifest.get("manifest_digest");
        let digest_hex = manifest_digest
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        if entry.manifest_digest != digest_hex {
            return Err(invalid_snapshot(
                "catalog entry manifest digest is stale or invalid",
            ));
        }
        let owner_entries: Value = manifest.get("entries");
        let supported = owner_entries.as_array().is_some_and(|values| {
            values.iter().any(|owner_entry| {
                owner_entry.get("resource_type").and_then(Value::as_str)
                    == Some(entry.resource_type.as_str())
                    && owner_entry.get("action").and_then(Value::as_str)
                        == Some(entry.action.as_str())
                    && owner_entry.get("capability").and_then(Value::as_str)
                        == Some(entry.capability.as_str())
            })
        });
        if !supported {
            return Err(invalid_snapshot(
                "catalog entry is not declared by its active owner manifest",
            ));
        }
        let Some(owner_shape) = owner_entries.as_array().and_then(|values| {
            values.iter().find(|owner_entry| {
                owner_entry.get("resource_type").and_then(Value::as_str)
                    == Some(entry.resource_type.as_str())
                    && owner_entry.get("action").and_then(Value::as_str)
                        == Some(entry.action.as_str())
                    && owner_entry.get("capability").and_then(Value::as_str)
                        == Some(entry.capability.as_str())
            })
        }) else {
            return Err(invalid_snapshot(
                "active owner manifest entry disappeared during validation",
            ));
        };
        for (field, requested) in [
            ("supported_scope_types", &entry.supported_scope_types),
            ("readable_fields", &entry.readable_fields),
            ("writable_fields", &entry.writable_fields),
        ] {
            let owner_values = owner_shape
                .get(field)
                .and_then(Value::as_array)
                .cloned()
                .unwrap_or_default();
            if requested.iter().any(|value| {
                !owner_values
                    .iter()
                    .any(|allowed| allowed.as_str() == Some(value))
            }) {
                return Err(invalid_snapshot(
                    "tenant catalog cannot exceed the owner manifest scopes or fields",
                ));
            }
        }
    }
    Ok(())
}

fn validate_policy_grants(snapshot: &Value) -> Result<(), ApiError> {
    let snapshot: CatalogSnapshot = serde_json::from_value(snapshot.clone())
        .map_err(|_| invalid_snapshot("catalog snapshot schema is invalid"))?;
    if snapshot.schema_version != 1 {
        return Err(invalid_snapshot(
            "unsupported catalog snapshot schema_version",
        ));
    }
    let entries = snapshot
        .entries
        .iter()
        .filter(|entry| entry.enabled)
        .collect::<Vec<_>>();
    let mut grant_ids = std::collections::HashSet::new();
    for grant in snapshot.policy_grants {
        if grant.grant_id.trim().is_empty() || !grant_ids.insert(grant.grant_id.clone()) {
            return Err(invalid_snapshot(
                "policy grant IDs must be non-empty and unique",
            ));
        }
        if !matches!(grant.effect.as_str(), "ALLOW" | "DENY") {
            return Err(invalid_snapshot("policy effect must be ALLOW or DENY"));
        }
        let matches = entries
            .iter()
            .filter(|entry| {
                entry.owner_id == grant.owner_id
                    && entry.resource_type == grant.resource_type
                    && entry.action == grant.action
            })
            .collect::<Vec<_>>();
        if matches.is_empty() {
            return Err(invalid_snapshot(
                "policy grant references an action that is not enabled in the catalog",
            ));
        }
        if !grant
            .required_capabilities
            .iter()
            .all(|required| matches.iter().any(|entry| &entry.capability == required))
        {
            return Err(invalid_snapshot(
                "policy grant requires a capability absent from the enabled catalog action",
            ));
        }
        if !matches
            .iter()
            .any(|entry| grant.required_capabilities.contains(&entry.capability))
        {
            return Err(invalid_snapshot(
                "policy grant must require the catalog capability for its action",
            ));
        }
        let supported_scopes = matches
            .iter()
            .flat_map(|entry| entry.supported_scope_types.iter())
            .collect::<Vec<_>>();
        let narrower_scope = !grant.branch_scope_ids.is_empty()
            || !grant.queue_scope_ids.is_empty()
            || grant.own_scope
            || !grant.resource_scope_ids.is_empty();
        if grant.tenant_scope == narrower_scope {
            return Err(invalid_snapshot(
                "each policy grant must explicitly choose tenant scope or at least one narrower scope",
            ));
        }
        if (!grant.branch_scope_ids.is_empty()
            && !supported_scopes
                .iter()
                .any(|scope| scope.as_str() == "branch"))
            || (!grant.queue_scope_ids.is_empty()
                && !supported_scopes
                    .iter()
                    .any(|scope| scope.as_str() == "queue"))
            || (grant.own_scope && !supported_scopes.iter().any(|scope| scope.as_str() == "own"))
            || (!grant.resource_scope_ids.is_empty()
                && !supported_scopes
                    .iter()
                    .any(|scope| scope.as_str() == "resource"))
        {
            return Err(invalid_snapshot(
                "policy grant uses a scope type unsupported by the catalog action",
            ));
        }
        let readable = matches
            .iter()
            .flat_map(|entry| entry.readable_fields.iter())
            .collect::<std::collections::HashSet<_>>();
        let writable = matches
            .iter()
            .flat_map(|entry| entry.writable_fields.iter())
            .collect::<std::collections::HashSet<_>>();
        if grant
            .readable_fields
            .iter()
            .any(|field| !readable.contains(field))
            || grant
                .writable_fields
                .iter()
                .any(|field| !writable.contains(field))
        {
            return Err(invalid_snapshot(
                "policy grant field set exceeds the owner catalog declaration",
            ));
        }
        if (grant.actor_ids.is_empty() && grant.roles.is_empty())
            || grant
                .actor_ids
                .iter()
                .chain(&grant.roles)
                .any(|value| value.trim().is_empty())
        {
            return Err(invalid_snapshot(
                "policy grants must name an actor or role and subject references must not be empty",
            ));
        }
    }
    Ok(())
}

async fn validate_branch_scope_ids(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    tenant_id: Uuid,
    snapshot: &Value,
) -> Result<(), ApiError> {
    use std::collections::BTreeSet;

    let snapshot: CatalogSnapshot = serde_json::from_value(snapshot.clone())
        .map_err(|_| invalid_snapshot("catalog snapshot schema is invalid"))?;
    let branch_ids = snapshot
        .policy_grants
        .iter()
        .flat_map(|grant| grant.branch_scope_ids.iter())
        .map(|value| {
            Uuid::parse_str(value).map_err(|_| invalid_snapshot("branch scope IDs must be UUIDs"))
        })
        .collect::<Result<BTreeSet<_>, _>>()?
        .into_iter()
        .collect::<Vec<_>>();
    if branch_ids.is_empty() {
        return Ok(());
    }

    sqlx::query("SELECT set_config('app.tenant_id', $1, true)")
        .bind(tenant_id.to_string())
        .execute(&mut **transaction)
        .await?;
    let active_branch_ids: Vec<Uuid> = sqlx::query_scalar(
        "SELECT id FROM authz_app.org_node WHERE tenant_id = $1 AND kind = 'BRANCH' AND is_active AND id = ANY($2)",
    )
    .bind(tenant_id)
    .bind(&branch_ids)
    .fetch_all(&mut **transaction)
    .await?;
    if active_branch_ids.len() != branch_ids.len() {
        return Err(invalid_snapshot(
            "branch scope references a branch that is inactive, missing, or belongs to another tenant",
        ));
    }
    Ok(())
}

async fn validate_role_references(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    tenant_id: Uuid,
    snapshot: &Value,
) -> Result<(), ApiError> {
    use std::collections::BTreeSet;

    let snapshot: CatalogSnapshot = serde_json::from_value(snapshot.clone())
        .map_err(|_| invalid_snapshot("catalog snapshot schema is invalid"))?;
    let role_codes = snapshot
        .policy_grants
        .iter()
        .flat_map(|grant| grant.roles.iter().cloned())
        .collect::<BTreeSet<_>>()
        .into_iter()
        .collect::<Vec<_>>();
    if role_codes.is_empty() {
        return Ok(());
    }
    let registered: Vec<String> =
        sqlx::query_scalar("SELECT code FROM role WHERE tenant_id = $1 AND code = ANY($2)")
            .bind(tenant_id)
            .bind(&role_codes)
            .fetch_all(&mut **transaction)
            .await?;
    if registered.len() != role_codes.len() {
        return Err(invalid_snapshot(
            "policy grant references a role code that is not registered for this tenant",
        ));
    }
    Ok(())
}

fn resource_grants(snapshot: &Value) -> Result<Vec<AuthorizationResourceGrant>, ApiError> {
    let snapshot: CatalogSnapshot = serde_json::from_value(snapshot.clone())
        .map_err(|_| invalid_snapshot("catalog snapshot schema is invalid"))?;
    snapshot
        .policy_grants
        .into_iter()
        .map(|grant| {
            let effect = match grant.effect.as_str() {
                "ALLOW" => AuthorizationPolicyEffect::Allow,
                "DENY" => AuthorizationPolicyEffect::Deny,
                _ => return Err(invalid_snapshot("policy effect must be ALLOW or DENY")),
            };
            Ok(AuthorizationResourceGrant {
                grant_id: grant.grant_id,
                owner_id: grant.owner_id,
                actor_ids: grant.actor_ids,
                roles: grant.roles,
                required_capabilities: grant.required_capabilities,
                resource_type: grant.resource_type,
                action: grant.action,
                effect: effect.into(),
                priority: 0,
                branch_scope_ids: grant.branch_scope_ids,
                branch_includes_descendants: grant.branch_includes_descendants,
                queue_scope_ids: grant.queue_scope_ids,
                own_scope: grant.own_scope,
                resource_scope_ids: grant.resource_scope_ids,
                tenant_scope: grant.tenant_scope,
                readable_fields: grant.readable_fields,
                writable_fields: grant.writable_fields,
            })
        })
        .collect()
}

fn resource_catalog(snapshot: &Value) -> Result<Vec<AuthorizationResourceDefinition>, ApiError> {
    let snapshot: CatalogSnapshot = serde_json::from_value(snapshot.clone())
        .map_err(|_| invalid_snapshot("catalog snapshot schema is invalid"))?;
    snapshot
        .entries
        .into_iter()
        .filter(|entry| entry.enabled)
        .map(|entry| {
            let manifest_digest = decode_digest(&entry.manifest_digest)?;
            Ok(AuthorizationResourceDefinition {
                owner_id: entry.owner_id,
                manifest_version: entry.manifest_version,
                manifest_digest,
                resource_type: entry.resource_type,
                action: entry.action,
                capability: entry.capability,
                supported_scope_types: entry.supported_scope_types,
                readable_fields: entry.readable_fields,
                writable_fields: entry.writable_fields,
            })
        })
        .collect()
}

fn decode_digest(value: &str) -> Result<Vec<u8>, ApiError> {
    if value.len() != 64 {
        return Err(invalid_snapshot(
            "owner manifest digest must be 32-byte hexadecimal",
        ));
    }
    (0..value.len())
        .step_by(2)
        .map(|index| {
            u8::from_str_radix(&value[index..index + 2], 16)
                .map_err(|_| invalid_snapshot("owner manifest digest must be hexadecimal"))
        })
        .collect()
}

fn validate_change_reason(reason: &str) -> Result<(), ApiError> {
    if reason.trim().is_empty() || reason.len() > 1000 {
        return Err(invalid_snapshot("change reason must be 1-1000 bytes"));
    }
    Ok(())
}

fn actor_ref(principal: &ServicePrincipal) -> &str {
    principal.actor_id.as_deref().unwrap_or(&principal.subject)
}

fn authorize_tenant(
    principal: &ServicePrincipal,
    tenant_id: Uuid,
    required_capability: &str,
) -> Result<(), ApiError> {
    let tenant_claim = principal
        .tenant_id
        .as_deref()
        .and_then(|value| Uuid::parse_str(value).ok());
    if tenant_claim != Some(tenant_id)
        || !principal
            .capabilities
            .iter()
            .any(|capability| capability == required_capability)
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: "authorization_catalog_access".into(),
            reason: "principal is not authorized for this tenant catalog".into(),
        }
        .into());
    }
    Ok(())
}

fn validate_snapshot(snapshot: &Value) -> Result<(), ApiError> {
    if snapshot.get("schema_version").and_then(Value::as_u64) != Some(1) {
        return Err(invalid_snapshot(
            "catalog snapshot schema_version must equal 1",
        ));
    }
    let entries = snapshot
        .get("entries")
        .and_then(Value::as_array)
        .ok_or_else(|| invalid_snapshot("entries must be an array"))?;
    if entries.is_empty() || entries.len() > 5000 {
        return Err(invalid_snapshot(
            "entries must contain between 1 and 5000 items",
        ));
    }
    let mut codes = std::collections::HashSet::new();
    for entry in entries {
        let owner = entry.get("owner_id").and_then(Value::as_str).unwrap_or("");
        let resource = entry
            .get("resource_type")
            .and_then(Value::as_str)
            .unwrap_or("");
        let action = entry.get("action").and_then(Value::as_str).unwrap_or("");
        let capability = entry
            .get("capability")
            .and_then(Value::as_str)
            .unwrap_or("");
        if [owner, resource, action, capability]
            .iter()
            .any(|value| value.trim().is_empty() || value.len() > 200)
        {
            return Err(invalid_snapshot(
                "every entry requires bounded owner_id, resource_type, action, and capability codes",
            ));
        }
        if !codes.insert((owner, resource, action, capability)) {
            return Err(invalid_snapshot(
                "catalog entries must have unique owner/resource/action/capability tuples",
            ));
        }
    }
    Ok(())
}

fn validate_owner_manifest(payload: &RegisterOwnerManifestRequest) -> Result<(), ApiError> {
    if payload.owner_id.trim().is_empty()
        || payload.owner_id.len() > 200
        || payload.manifest_version.trim().is_empty()
        || payload.manifest_version.len() > 100
        || payload.entries.is_empty()
        || payload.entries.len() > 5000
    {
        return Err(invalid_snapshot(
            "owner manifest identity, version, or entries are invalid",
        ));
    }
    let mut unique = std::collections::HashSet::new();
    for entry in &payload.entries {
        let resource = entry
            .get("resource_type")
            .and_then(Value::as_str)
            .unwrap_or("");
        let action = entry.get("action").and_then(Value::as_str).unwrap_or("");
        let capability = entry
            .get("capability")
            .and_then(Value::as_str)
            .unwrap_or("");
        if [resource, action, capability]
            .iter()
            .any(|value| value.trim().is_empty() || value.len() > 200)
        {
            return Err(invalid_snapshot(
                "owner manifest entries require bounded resource_type, action, and capability",
            ));
        }
        if !unique.insert((resource, action, capability)) {
            return Err(invalid_snapshot(
                "owner manifest resource/action/capability tuples must be unique",
            ));
        }
        for field in [
            "supported_scope_types",
            "readable_fields",
            "writable_fields",
        ] {
            if entry.get(field).and_then(Value::as_array).is_none() {
                return Err(invalid_snapshot(
                    "owner manifest entries require scope and field arrays",
                ));
            }
        }
    }
    Ok(())
}

fn invalid_snapshot(reason: &str) -> ApiError {
    AuthzError::InvalidRequest {
        reason: reason.to_owned(),
    }
    .into()
}
