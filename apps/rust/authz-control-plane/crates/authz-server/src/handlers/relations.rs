//! `POST /authz/v1/relations` — Insert a relation tuple.
//!
//! Manages the ReBAC relation graph.
//! DB-level triggers enforce cycle detection and fanout limits.

use axum::{extract::State, http::StatusCode, Json};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tracing::instrument;
use uuid::Uuid;

use authz_core::ids::TenantId;
use authz_db::insert_relation_tuple;

use crate::{error::ApiError, state::AppState};

#[derive(Debug, Deserialize)]
pub struct InsertRelationRequest {
    pub tenant_id: Uuid,
    /// Subject in `"type:id"` format, e.g. `"user:550e8400-..."`
    pub subject: String,
    /// Relation name, e.g. `"delegate_of"`, `"member_of"`, `"reviewer_of"`
    pub relation: String,
    /// Object in `"type:id"` format, e.g. `"user:660e..."`
    pub object: String,
    /// Optional expiry for temporary relations
    pub expires_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Serialize)]
pub struct InsertRelationResponse {
    pub id: String,
    pub message: &'static str,
}

/// `POST /authz/v1/relations` — Insert a relation tuple.
///
/// The database trigger will reject the insert if it creates a cycle
/// or exceeds the configured fan-out limit for this relation type.
#[instrument(skip_all, name = "http.insert_relation")]
pub async fn insert_relation_handler(
    State(state): State<AppState>,
    Json(body): Json<InsertRelationRequest>,
) -> Result<(StatusCode, Json<InsertRelationResponse>), ApiError> {
    let tenant_id = TenantId::from_uuid(body.tenant_id);

    let id = insert_relation_tuple(
        &state.pool,
        tenant_id,
        &body.subject,
        &body.relation,
        &body.object,
        body.expires_at,
    )
    .await?;

    Ok((
        StatusCode::CREATED,
        Json(InsertRelationResponse {
            id: id.to_string(),
            message: "Relation tuple inserted",
        }),
    ))
}
