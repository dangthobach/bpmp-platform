use crate::{error::ApiError, state::AppState};
use authz_core::{ids::TenantId, AuthzError};
use authz_db::repositories::tenant_write::{
    insert_tenant as db_insert_tenant, update_tenant_status as db_update_tenant_status,
    TenantStatus,
};
use axum::{
    extract::{Path, State},
    http::StatusCode,
    Json,
};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Deserialize)]
pub struct CreateTenantRequest {
    pub name: String,
}

#[derive(Serialize)]
pub struct TenantResponse {
    pub id: Uuid,
    pub name: String,
    pub status: String,
    pub version: i64,
}

pub async fn create_tenant(
    State(state): State<AppState>,
    Json(payload): Json<CreateTenantRequest>,
) -> Result<(StatusCode, Json<TenantResponse>), ApiError> {
    let tenant_id = TenantId(Uuid::new_v4());
    db_insert_tenant(&state.pool, tenant_id, &payload.name).await?;

    Ok((
        StatusCode::CREATED,
        Json(TenantResponse {
            id: tenant_id.into_uuid(),
            name: payload.name,
            status: "ACTIVE".to_string(),
            version: 0,
        }),
    ))
}

#[derive(Deserialize)]
pub struct UpdateTenantStatusRequest {
    pub status: String,
    pub expected_version: i64,
}

#[derive(Serialize)]
pub struct UpdateTenantStatusResponse {
    pub version: i64,
}

pub async fn update_tenant_status(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
    Json(payload): Json<UpdateTenantStatusRequest>,
) -> Result<Json<UpdateTenantStatusResponse>, ApiError> {
    let status = match payload.status.to_uppercase().as_str() {
        "ACTIVE" => TenantStatus::Active,
        "SUSPENDED" => TenantStatus::Suspended,
        _ => {
            return Err(ApiError::from(AuthzError::InvalidRequest {
                reason: "Invalid status. Use ACTIVE or SUSPENDED".into(),
            }))
        }
    };

    let version =
        db_update_tenant_status(&state.pool, TenantId(id), status, payload.expected_version)
            .await?;

    Ok(Json(UpdateTenantStatusResponse { version }))
}
