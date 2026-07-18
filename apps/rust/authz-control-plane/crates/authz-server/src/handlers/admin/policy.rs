use crate::{error::ApiError, state::AppState};
use authz_core::ids::TenantId;
use authz_db::repositories::policy_write::{
    insert_policy, insert_policy_version, promote_policy_version,
};
use axum::{
    extract::{Path, State},
    http::StatusCode,
    Json,
};
use serde::Deserialize;
use uuid::Uuid;

#[derive(Deserialize)]
pub struct UploadPolicyRequest {
    pub tenant_id: Uuid,
    pub name: String,
    pub description: Option<String>,
    pub policy_content: String,
}

pub async fn upload_policy(
    State(state): State<AppState>,
    Json(payload): Json<UploadPolicyRequest>,
) -> Result<(StatusCode, Json<serde_json::Value>), ApiError> {
    let policy_id = Uuid::new_v4();
    let tenant_id = TenantId(payload.tenant_id);

    insert_policy(
        &state.pool,
        policy_id,
        tenant_id,
        &payload.name,
        payload.description.as_deref(),
    )
    .await?;

    let version_id = Uuid::new_v4();
    insert_policy_version(
        &state.pool,
        version_id,
        policy_id,
        &payload.policy_content,
        "DRAFT",
        "Initial upload",
    )
    .await?;

    Ok((
        StatusCode::CREATED,
        Json(serde_json::json!({ "policy_id": policy_id, "version_id": version_id })),
    ))
}

pub async fn promote_policy(
    State(state): State<AppState>,
    Path(version_id): Path<Uuid>,
) -> Result<StatusCode, ApiError> {
    promote_policy_version(&state.pool, version_id).await?;
    Ok(StatusCode::NO_CONTENT)
}
