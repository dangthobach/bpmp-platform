use crate::{error::ApiError, state::AppState};
use authz_core::ids::{PermissionId, RoleId, TenantId, UserId};
use authz_db::repositories::rbac_write::{
    assign_role_to_permission, assign_role_to_user, insert_permission, insert_role,
};
use axum::{
    extract::{Path, State},
    http::StatusCode,
    Json,
};
use serde::Deserialize;
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

#[derive(Deserialize)]
pub struct AssignRoleRequest {
    pub tenant_id: Uuid,
    pub role_id: Uuid,
}

pub async fn assign_role(
    State(state): State<AppState>,
    Path(user_id): Path<Uuid>,
    Json(payload): Json<AssignRoleRequest>,
) -> Result<StatusCode, ApiError> {
    assign_role_to_user(
        &state.pool,
        TenantId(payload.tenant_id),
        UserId(user_id),
        RoleId(payload.role_id),
    )
    .await?;

    Ok(StatusCode::NO_CONTENT)
}
