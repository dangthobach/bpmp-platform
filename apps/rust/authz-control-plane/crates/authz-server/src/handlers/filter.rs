//! `POST /authz/v1/filter` — Row filter generation handler.
//!
//! Returns the backend-specific row filter predicate for a given user+resource+action.
//! Domain services inject this into their DB/ES/Mongo queries.

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

#[derive(Debug, Deserialize)]
pub struct FilterRequest {
    pub tenant_id: uuid::Uuid,
    pub user_id: uuid::Uuid,
    pub resource_type: String,
    pub action: String,
    pub user_attributes: JsonValue,
    pub user_attributes_version: i64,
    pub backend: String,
}

#[derive(Debug, Serialize)]
pub struct FilterResponse {
    /// Authorization decision: `"ALLOW"` or `"DENY"`.
    pub decision: &'static str,
    /// Human-readable denial reason (omitted on ALLOW).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deny_reason: Option<String>,
    /// SQL predicate (only for `backend: "sql"`).
    pub sql_where: Option<String>,
    /// SQL parameters keyed by name (e.g. `"p1"`, `"p2"`).
    pub sql_params: Option<std::collections::HashMap<String, JsonValue>>,
    /// Elasticsearch DSL query (for `backend: "elasticsearch"`).
    pub es_filter: Option<JsonValue>,
    /// MongoDB `$match` expression (for `backend: "mongodb"`).
    pub mongo_filter: Option<JsonValue>,
    /// Fields the caller is allowed to return (empty = all allowed).
    pub allowed_fields: Vec<String>,
    /// Fields that must be masked in the response.
    pub masked_fields: Vec<String>,
}

/// `POST /authz/v1/filter` — Row filter generation.
///
/// Evaluates the user's permissions and returns:
/// - The authorization decision (ALLOW / DENY)
/// - The combined row filter predicate for the requested backend
/// - Field visibility constraints (allowed_fields, masked_fields)
///
/// On DENY, all filter fields are `null` — the caller must treat the
/// absence of a filter as "show nothing", not "show everything".
#[instrument(skip_all, name = "http.filter")]
pub async fn filter_handler(
    State(state): State<AppState>,
    Json(body): Json<FilterRequest>,
) -> Result<Json<FilterResponse>, ApiError> {
    let tenant_id = TenantId::from_uuid(body.tenant_id);
    let user_id = UserId::from_uuid(body.user_id);

    let backend = match body.backend.as_str() {
        "elasticsearch" => FilterBackend::Elasticsearch,
        "mongodb" => FilterBackend::Mongodb,
        _ => FilterBackend::Sql,
    };

    let ctx = AuthzContext {
        tenant_id,
        user_id,
        user_attributes: body.user_attributes,
        user_attributes_version: body.user_attributes_version,
        resource: ResourceContext {
            resource_type: body.resource_type.clone(),
            resource_ref: None,
            attributes: JsonValue::Object(Default::default()),
        },
        env: EnvContext::default(),
        backend,
    };

    let req = AuthzRequest {
        tenant_id,
        user_id,
        action: body.action.clone(),
        context: ctx,
        include_trace: false,
    };

    let resp = state.pipeline.evaluate(&req).await?;

    let (sql_where, sql_params, es_filter, mongo_filter) = if let Some(rf) = resp.row_filter {
        let sw = rf.sql_where.as_ref().map(|s| s.predicate.clone());
        let sp = rf.sql_where.map(|s| s.params);
        (sw, sp, rf.es_filter, rf.mongo_filter)
    } else {
        (None, None, None, None)
    };

    let (allowed_fields, masked_fields) = resp
        .field_filter
        .map(|ff| (ff.allowed_fields, ff.masked_fields))
        .unwrap_or_default();

    Ok(Json(FilterResponse {
        decision: match resp.decision {
            AuthzDecision::Allow => "ALLOW",
            AuthzDecision::Deny => "DENY",
        },
        deny_reason: resp.deny_reason,
        sql_where,
        sql_params,
        es_filter,
        mongo_filter,
        allowed_fields,
        masked_fields,
    }))
}
