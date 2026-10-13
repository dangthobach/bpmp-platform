use crate::handlers::query_revoke::{change, RevokeCommand};
use crate::middleware::service_auth::ServicePrincipal;
use crate::{error::ApiError, state::AppState};
use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    Extension, Json,
};
use serde::Deserialize;
use uuid::Uuid;

#[derive(Deserialize)]
pub struct RevokeRequest {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub expected_version: i64,
    pub reason: String,
}

pub async fn revoke_user(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Json(payload): Json<RevokeRequest>,
) -> Result<StatusCode, ApiError> {
    let _response = change(
        State(state),
        Extension(principal),
        Json(RevokeCommand {
            tenant_id: payload.tenant_id,
            actor_id: payload.user_id,
            expected_version: payload.expected_version,
            revoked: true,
            reason: payload.reason,
        }),
    )
    .await?;
    Ok(StatusCode::NO_CONTENT)
}

pub async fn clear_revoke(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Path(user_id): Path<Uuid>,
    Query(payload): Query<RestoreQuery>,
) -> Result<StatusCode, ApiError> {
    let _response = change(
        State(state),
        Extension(principal),
        Json(RevokeCommand {
            tenant_id: payload.tenant_id,
            actor_id: user_id,
            expected_version: payload.expected_version,
            revoked: false,
            reason: payload.reason,
        }),
    )
    .await?;
    Ok(StatusCode::NO_CONTENT)
}

#[derive(Deserialize)]
pub struct RestoreQuery {
    pub tenant_id: Uuid,
    pub expected_version: i64,
    pub reason: String,
}
