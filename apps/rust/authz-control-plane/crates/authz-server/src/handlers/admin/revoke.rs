use crate::{error::ApiError, state::AppState};
use authz_core::ids::UserId;
use axum::{
    extract::{Path, State},
    http::StatusCode,
    Json,
};
use serde::Deserialize;
use uuid::Uuid;

#[derive(Deserialize)]
pub struct RevokeRequest {
    pub user_id: Uuid,
    pub reason: String,
}

pub async fn revoke_user(
    State(state): State<AppState>,
    Json(payload): Json<RevokeRequest>,
) -> Result<StatusCode, ApiError> {
    state.emergency_revoke.revoke(UserId(payload.user_id));
    Ok(StatusCode::NO_CONTENT)
}

pub async fn clear_revoke(
    State(state): State<AppState>,
    Path(user_id): Path<Uuid>,
) -> Result<StatusCode, ApiError> {
    state.emergency_revoke.clear_revoke(UserId(user_id));
    Ok(StatusCode::NO_CONTENT)
}
