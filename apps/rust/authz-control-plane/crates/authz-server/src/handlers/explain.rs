//! `POST /authz/v1/explain` — Decision trace handler (G7: Policy Debugger).
//!
//! Returns the full AST evaluation trace for the most recent decision
//! matching the given user+resource+action.

use axum::{extract::State, Json};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use tracing::instrument;
use uuid::Uuid;

use authz_core::ids::TenantId;
use authz_db::find_latest_decision;

use crate::{error::ApiError, state::AppState};

#[derive(Debug, Deserialize)]
pub struct ExplainRequest {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub resource_ref: String,
    pub action: String,
}

#[derive(Debug, Serialize)]
pub struct ExplainResponse {
    pub found: bool,
    pub decision: Option<String>,
    pub eval_trace: Option<JsonValue>,
    pub context: Option<JsonValue>,
    pub decided_at: Option<String>,
}

/// `POST /authz/v1/explain` — Returns the most recent decision trace (G7).
///
/// Used by the Policy Debugger to answer "why was I denied?".
#[instrument(skip_all, name = "http.explain")]
pub async fn explain_handler(
    State(state): State<AppState>,
    Json(body): Json<ExplainRequest>,
) -> Result<Json<ExplainResponse>, ApiError> {
    let tenant_id = TenantId::from_uuid(body.tenant_id);

    let log = find_latest_decision(
        &state.pool,
        tenant_id,
        body.user_id,
        &body.resource_ref,
        &body.action,
    )
    .await?;

    match log {
        None => Ok(Json(ExplainResponse {
            found: false,
            decision: None,
            eval_trace: None,
            context: None,
            decided_at: None,
        })),
        Some(entry) => {
            use authz_core::models::policy::AuthzDecision;
            Ok(Json(ExplainResponse {
                found: true,
                decision: Some(match entry.decision {
                    AuthzDecision::Allow => "ALLOW".to_owned(),
                    AuthzDecision::Deny => "DENY".to_owned(),
                }),
                eval_trace: serde_json::to_value(&entry.eval_trace).ok(),
                context: serde_json::to_value(&entry.context).ok(),
                decided_at: Some(entry.decided_at.to_rfc3339()),
            }))
        }
    }
}
