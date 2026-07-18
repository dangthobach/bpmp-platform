//! `POST /authz/v1/check` — Binary authorization decision handler.

use axum::{extract::State, Json};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use tracing::instrument;

use authz_core::{
    ids::{TenantId, UserId},
    models::{filter::FilterBackend, policy::AuthzDecision},
};
use authz_engine::{
    context::{AuthzContext, EnvContext, ResourceContext},
    evaluator::pipeline::AuthzRequest,
};

use crate::{error::ApiError, state::AppState};

/// Request body for `POST /authz/v1/check`.
#[derive(Debug, Deserialize)]
pub struct CheckRequest {
    /// Tenant code (e.g. `"vpbank"`).
    pub tenant_id: uuid::Uuid,
    pub user_id: uuid::Uuid,
    pub resource_type: String,
    pub resource_ref: Option<String>,
    pub action: String,
    /// User attributes at request time (from JWT claims + Keycloak attributes).
    pub user_attributes: JsonValue,
    pub user_attributes_version: i64,
    /// Resource-level attributes from the calling service.
    pub resource_attributes: Option<JsonValue>,
    /// Requesting backend: `"sql"`, `"elasticsearch"`, `"mongodb"`.
    pub backend: Option<String>,
    /// Include full evaluation trace in response (debug mode only).
    pub include_trace: Option<bool>,
}

/// Response body for `POST /authz/v1/check`.
#[derive(Debug, Serialize)]
pub struct CheckResponse {
    pub decision: &'static str,
    pub deny_reason: Option<String>,
    pub decision_id: String,
    pub eval_trace: Option<JsonValue>,
}

/// `POST /authz/v1/check` — Binary authorization decision.
///
/// Returns ALLOW or DENY with an optional full evaluation trace.
/// The trace is only included when `include_trace: true` is set.
/// Never include trace in production — it reveals policy internals.
#[instrument(skip_all, name = "http.check")]
pub async fn check_handler(
    State(state): State<AppState>,
    Json(body): Json<CheckRequest>,
) -> Result<Json<CheckResponse>, ApiError> {
    let tenant_id = TenantId::from_uuid(body.tenant_id);
    let user_id = UserId::from_uuid(body.user_id);

    let backend = match body.backend.as_deref() {
        Some("elasticsearch") => FilterBackend::Elasticsearch,
        Some("mongodb") => FilterBackend::Mongodb,
        _ => FilterBackend::Sql,
    };

    let ctx = AuthzContext {
        tenant_id,
        user_id,
        user_attributes: body.user_attributes,
        user_attributes_version: body.user_attributes_version,
        resource: ResourceContext {
            resource_type: body.resource_type.clone(),
            resource_ref: body.resource_ref.clone(),
            attributes: body
                .resource_attributes
                .unwrap_or(JsonValue::Object(Default::default())),
        },
        env: EnvContext::default(),
        backend,
    };

    let req = AuthzRequest {
        tenant_id,
        user_id,
        action: body.action.clone(),
        context: ctx,
        include_trace: body.include_trace.unwrap_or(false),
    };

    let response = state.pipeline.evaluate(&req).await?;

    let trace_json = response
        .eval_trace
        .as_ref()
        .and_then(|t| serde_json::to_value(t).ok());

    Ok(Json(CheckResponse {
        decision: match response.decision {
            AuthzDecision::Allow => "ALLOW",
            AuthzDecision::Deny => "DENY",
        },
        deny_reason: response.deny_reason,
        decision_id: response.decision_id.to_string(),
        eval_trace: trace_json,
    }))
}
