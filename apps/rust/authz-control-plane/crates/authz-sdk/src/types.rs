//! Wire-level DTOs aligned with `authz-server` HTTP API.
//!
//! All structs use `#[serde(deny_unknown_fields)]` per the platform's
//! input-validation policy: any unexpected field is a hard error,
//! not a silent ignore.

use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use uuid::Uuid;

/// Identity propagated through every PEP→PDP call.
///
/// Built once per HTTP request at the PEP edge (from JWT claims) and
/// passed by reference. Cloning is cheap because all fields are small.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Subject {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    /// User attributes (e.g. `{"branch_code":"HN01","level":3}`).
    pub attributes: JsonValue,
    /// Monotonic version; bumped in `authz-server` when attributes change.
    /// PEP cache MUST include this in the key.
    pub attributes_version: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckRequest {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub resource_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub resource_ref: Option<String>,
    pub action: String,
    pub user_attributes: JsonValue,
    pub user_attributes_version: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub resource_attributes: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub backend: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub include_trace: Option<bool>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum Decision {
    Allow,
    Deny,
}

#[derive(Debug, Clone, Deserialize)]
pub struct CheckResponse {
    pub decision: Decision,
    #[serde(default)]
    pub deny_reason: Option<String>,
    pub decision_id: String,
    #[serde(default)]
    pub eval_trace: Option<JsonValue>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FilterRequest {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub resource_type: String,
    pub action: String,
    pub backend: String,
    pub user_attributes: JsonValue,
    pub user_attributes_version: i64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct FilterResponse {
    pub decision: Decision,
    /// Backend-specific filter payload (SQL fragment, ES JSON, Mongo JSON).
    pub filter: JsonValue,
    #[serde(default)]
    pub allowed_fields: Option<JsonValue>,
    #[serde(default)]
    pub masked_fields: Option<JsonValue>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExplainRequest {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub resource_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub resource_ref: Option<String>,
    pub action: String,
    pub user_attributes: JsonValue,
    pub user_attributes_version: i64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ExplainResponse {
    pub decision: Decision,
    pub trace: JsonValue,
}
