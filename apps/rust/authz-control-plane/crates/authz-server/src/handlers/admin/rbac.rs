use crate::{
    error::ApiError,
    middleware::{request_id::RequestId, service_auth::ServicePrincipal},
    state::AppState,
};
use authz_core::ids::{PermissionId, RoleId, TenantId};
use authz_db::repositories::rbac_write::{
    assign_role_to_permission, insert_permission, insert_role,
};
use axum::{
    extract::{Extension, Path, State},
    http::{HeaderMap, StatusCode},
    Json,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::Row;
use uuid::Uuid;

#[derive(Deserialize)]
pub struct CreateRoleRequest {
    pub tenant_id: Uuid,
    /// Short stable code unique within the tenant, e.g. "branch_manager".
    pub code: String,
    pub name: String,
}

pub async fn create_role(
    State(state): State<AppState>,
    Json(payload): Json<CreateRoleRequest>,
) -> Result<(StatusCode, Json<serde_json::Value>), ApiError> {
    let role_id = RoleId(Uuid::new_v4());
    let tenant_id = TenantId(payload.tenant_id);

    insert_role(
        &state.pool,
        role_id,
        tenant_id,
        &payload.code,
        &payload.name,
    )
    .await?;

    Ok((
        StatusCode::CREATED,
        Json(serde_json::json!({ "role_id": role_id.into_uuid() })),
    ))
}

#[derive(Deserialize)]
pub struct CreatePermissionRequest {
    pub tenant_id: Uuid,
    /// Optional role to link the new permission to via role_permission.
    pub role_id: Option<Uuid>,
    /// Short stable code unique within the tenant, e.g. "contract:read".
    pub code: String,
    pub resource_type: String,
    pub action: String,
    /// Coarse-grained scope: "own" | "branch" | "all". Defaults to "all".
    pub scope: Option<String>,
}

pub async fn create_permission(
    State(state): State<AppState>,
    Json(payload): Json<CreatePermissionRequest>,
) -> Result<(StatusCode, Json<serde_json::Value>), ApiError> {
    let permission_id = PermissionId(Uuid::new_v4());
    let tenant_id = TenantId(payload.tenant_id);
    let scope = payload.scope.as_deref().unwrap_or("all");

    insert_permission(
        &state.pool,
        permission_id,
        tenant_id,
        &payload.code,
        &payload.resource_type,
        &payload.action,
        scope,
    )
    .await?;

    // Optionally link the new permission to a role
    if let Some(role_uuid) = payload.role_id {
        assign_role_to_permission(&state.pool, RoleId(role_uuid), permission_id, None).await?;
    }

    Ok((
        StatusCode::CREATED,
        Json(serde_json::json!({ "permission_id": permission_id.into_uuid() })),
    ))
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AssignRoleRequest {
    pub tenant_id: Uuid,
    pub role_id: Uuid,
    pub expected_version: i64,
}

pub async fn assign_role(
    State(state): State<AppState>,
    Path((user_id, role_id)): Path<(Uuid, Uuid)>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    headers: HeaderMap,
    Json(payload): Json<AssignRoleRequest>,
) -> Result<(StatusCode, Json<serde_json::Value>), ApiError> {
    mutate_user_role(
        state, principal, request_id, headers, user_id, role_id, payload, true,
    )
    .await
}

pub async fn revoke_role(
    State(state): State<AppState>,
    Path((user_id, role_id)): Path<(Uuid, Uuid)>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    headers: HeaderMap,
    Json(payload): Json<AssignRoleRequest>,
) -> Result<(StatusCode, Json<serde_json::Value>), ApiError> {
    mutate_user_role(
        state, principal, request_id, headers, user_id, role_id, payload, false,
    )
    .await
}

async fn mutate_user_role(
    state: AppState,
    principal: ServicePrincipal,
    request_id: RequestId,
    headers: HeaderMap,
    user_id: Uuid,
    role_id: Uuid,
    payload: AssignRoleRequest,
    assign: bool,
) -> Result<(StatusCode, Json<serde_json::Value>), ApiError> {
    let operation = if assign { "ASSIGN_ROLE" } else { "REVOKE_ROLE" };
    let capability = if assign {
        "identity.role.assign"
    } else {
        "identity.role.revoke"
    };
    let actor_ref = principal.actor_id.as_deref().unwrap_or(&principal.subject);
    if principal.tenant_id.as_deref() != Some(payload.tenant_id.to_string().as_str())
        || !principal
            .capabilities
            .iter()
            .any(|value| value == capability)
    {
        return Err(authz_core::AuthzError::PolicyDenied {
            policy_name: "identity_role_mutation".into(),
            reason: "principal cannot mutate roles in this tenant".into(),
        }
        .into());
    }
    if payload.role_id != role_id || payload.expected_version < 0 {
        return Err(authz_core::AuthzError::InvalidRequest {
            reason: "role path/body mismatch or invalid expected_version".into(),
        }
        .into());
    }
    let key = headers
        .get("Idempotency-Key")
        .and_then(|v| v.to_str().ok())
        .filter(|v| !v.trim().is_empty() && v.len() <= 200)
        .ok_or_else(|| authz_core::AuthzError::InvalidRequest {
            reason: "Idempotency-Key is required and limited to 200 bytes".into(),
        })?;
    let key_hash = Sha256::digest(key.as_bytes()).to_vec();
    let request_hash = Sha256::digest(
        serde_json::to_vec(&(user_id, role_id, &payload)).map_err(authz_core::AuthzError::from)?,
    )
    .to_vec();
    let actor_id = Uuid::parse_str(actor_ref).ok();
    let mut tx = state.pool.begin().await?;

    sqlx::query("DELETE FROM identity_profile_idempotency WHERE tenant_id=$1 AND actor_ref=$2 AND operation=$3 AND idempotency_key_hash=$4 AND expires_at <= now()")
        .bind(payload.tenant_id).bind(actor_ref).bind(operation).bind(&key_hash).execute(&mut *tx).await?;
    let inserted = sqlx::query("INSERT INTO identity_profile_idempotency (tenant_id,actor_ref,operation,idempotency_key_hash,request_hash) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING")
        .bind(payload.tenant_id).bind(actor_ref).bind(operation).bind(&key_hash).bind(&request_hash).execute(&mut *tx).await?.rows_affected() == 1;
    if !inserted {
        let receipt = sqlx::query("SELECT request_hash,response_body FROM identity_profile_idempotency WHERE tenant_id=$1 AND actor_ref=$2 AND operation=$3 AND idempotency_key_hash=$4 FOR UPDATE")
            .bind(payload.tenant_id).bind(actor_ref).bind(operation).bind(&key_hash).fetch_one(&mut *tx).await?;
        let prior: Vec<u8> = receipt.try_get("request_hash")?;
        if prior != request_hash {
            return Err(authz_core::AuthzError::IdempotencyConflict.into());
        }
        let body: Option<serde_json::Value> = receipt.try_get("response_body")?;
        let Some(body) = body else {
            return Err(authz_core::AuthzError::Internal(
                "identity role idempotency receipt has no response".into(),
            )
            .into());
        };
        tx.commit().await?;
        return Ok((StatusCode::OK, Json(body)));
    }

    let row = sqlx::query("SELECT version FROM user_account WHERE id=$1 AND tenant_id=$2 AND is_active AND NOT is_deleted FOR UPDATE")
        .bind(user_id).bind(payload.tenant_id).fetch_optional(&mut *tx).await?;
    let Some(row) = row else {
        return Err(authz_core::AuthzError::UserNotFound {
            user_id,
            tenant_id: payload.tenant_id,
        }
        .into());
    };
    let actual: i64 = row.try_get("version")?;
    if actual != payload.expected_version {
        return Err(authz_core::AuthzError::VersionConflict {
            entity: "user access profile",
            entity_id: user_id,
            expected_version: payload.expected_version,
            actual_version: actual,
        }
        .into());
    }
    let role_exists: bool =
        sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM role WHERE id=$1 AND tenant_id=$2)")
            .bind(role_id)
            .bind(payload.tenant_id)
            .fetch_one(&mut *tx)
            .await?;
    if !role_exists {
        return Err(authz_core::AuthzError::InvalidRequest {
            reason: "role does not belong to tenant".into(),
        }
        .into());
    }

    if assign {
        sqlx::query("INSERT INTO user_role (user_id,role_id,tenant_id,granted_by) VALUES ($1,$2,$3,$4) ON CONFLICT (user_id,role_id) DO NOTHING")
            .bind(user_id).bind(role_id).bind(payload.tenant_id).bind(actor_id).execute(&mut *tx).await?;
    } else {
        let removed =
            sqlx::query("DELETE FROM user_role WHERE user_id=$1 AND role_id=$2 AND tenant_id=$3")
                .bind(user_id)
                .bind(role_id)
                .bind(payload.tenant_id)
                .execute(&mut *tx)
                .await?
                .rows_affected();
        if removed == 0 {
            return Err(authz_core::AuthzError::InvalidRequest {
                reason: "role is not assigned to this user".into(),
            }
            .into());
        }
    }
    let version = actual.checked_add(1).ok_or_else(|| {
        authz_core::AuthzError::Internal("identity profile version exhausted".into())
    })?;
    let updated = sqlx::query("UPDATE user_account SET version=$1,updated_by=$2,attributes_version=attributes_version+1 WHERE id=$3 AND tenant_id=$4 AND version=$5")
        .bind(version).bind(actor_id).bind(user_id).bind(payload.tenant_id).bind(actual).execute(&mut *tx).await?.rows_affected();
    if updated != 1 {
        return Err(authz_core::AuthzError::VersionConflict {
            entity: "user access profile",
            entity_id: user_id,
            expected_version: payload.expected_version,
            actual_version: actual,
        }
        .into());
    }
    let fingerprint = Sha256::digest(
        [
            user_id.as_bytes().as_slice(),
            role_id.as_bytes().as_slice(),
            operation.as_bytes(),
        ]
        .concat(),
    )
    .to_vec();
    sqlx::query("INSERT INTO identity_profile_mutation_audit (tenant_id,user_id,operation,entity_version,actor_ref,executor_ref,request_id,idempotency_key_hash,identity_fingerprint) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)")
        .bind(payload.tenant_id).bind(user_id).bind(operation).bind(version).bind(actor_ref).bind(&principal.subject).bind(&request_id.0).bind(&key_hash).bind(fingerprint).execute(&mut *tx).await?;
    let body = serde_json::json!({"user_id":user_id,"role_id":role_id,"operation":operation,"version":version});
    sqlx::query("UPDATE identity_profile_idempotency SET response_body=$5 WHERE tenant_id=$1 AND actor_ref=$2 AND operation=$3 AND idempotency_key_hash=$4")
        .bind(payload.tenant_id).bind(actor_ref).bind(operation).bind(&key_hash).bind(&body).execute(&mut *tx).await?;
    tx.commit().await?;
    Ok((StatusCode::OK, Json(body)))
}
