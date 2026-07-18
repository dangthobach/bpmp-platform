//! Data filter models — Layer E.
//!
//! Covers field filters (masking), row filters (backend-agnostic AST),
//! temporal access policies, and external attribute source registry.

use crate::ids::{FieldFilterId, PermissionId, RowFilterId, TemporalPolicyId, TenantId};
use chrono::{DateTime, NaiveTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use uuid::Uuid;

// ─── Field Filter (Layer E-3: field masking) ──────────────────────────────────

/// Defines which fields of a resource are visible and which are masked
/// for a given permission.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FieldFilter {
    pub id: FieldFilterId,
    pub permission_id: PermissionId,
    pub resource_type: String,
    /// Fields that may be returned in full. Empty = all fields allowed.
    pub allowed_fields: Vec<String>,
    /// Fields that must be masked (not blocked — still returned, but obfuscated).
    pub masked_fields: Vec<String>,
    /// Pattern for masking, e.g. `"****"` or `"***-***-####"`.
    pub mask_pattern: Option<String>,
    pub metadata: super::metadata::EntityMetadata,
}

/// A masked field in the response.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MaskedField {
    pub field: String,
    pub pattern: String,
}

// ─── Row Filter (Layer E-1: backend-agnostic AST) ─────────────────────────────

/// A row-level filter that restricts which records a user can see.
///
/// The `filter_expr` is a backend-agnostic JSON AST that can be translated
/// to SQL WHERE clauses, Elasticsearch DSL, or MongoDB `$match` expressions.
///
/// Escape hatches (`sql_fragment`, `es_fragment`, `mongo_fragment`) are available
/// for edge cases that the AST cannot express — but require governance approval.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RowFilter {
    pub id: RowFilterId,
    pub permission_id: PermissionId,
    pub resource_type: String,
    /// Backend-agnostic AST — preferred path.
    pub filter_expr: crate::models::policy::ConditionNode,
    /// Escape hatch: raw SQL WHERE fragment (requires approval).
    pub sql_fragment: Option<String>,
    /// Escape hatch: raw Elasticsearch DSL (requires approval).
    pub es_fragment: Option<JsonValue>,
    /// Escape hatch: raw MongoDB `$match` (requires approval).
    pub mongo_fragment: Option<JsonValue>,
    pub priority: i32,
    pub is_active: bool,
    // Escape hatch governance fields
    pub escape_hatch_reason: Option<String>,
    pub escape_hatch_approved_by: Option<Uuid>,
    pub escape_hatch_approved_at: Option<DateTime<Utc>>,
    pub escape_hatch_ticket_ref: Option<String>,
    pub metadata: super::metadata::EntityMetadata,
}

impl RowFilter {
    /// Returns `true` if this filter uses an escape hatch instead of the AST.
    pub fn uses_escape_hatch(&self, backend: &str) -> bool {
        match backend {
            "sql" => self.sql_fragment.is_some(),
            "elasticsearch" => self.es_fragment.is_some(),
            "mongodb" => self.mongo_fragment.is_some(),
            _ => false,
        }
    }

    /// Returns `true` if escape hatch usage is properly approved.
    pub fn escape_hatch_approved(&self) -> bool {
        self.escape_hatch_approved_by.is_some()
            && self.escape_hatch_reason.is_some()
            && self.escape_hatch_ticket_ref.is_some()
    }
}

// ─── Temporal Policy (EC-1) ───────────────────────────────────────────────────

/// A temporal access gate that restricts when a permission is active.
///
/// Evaluated BEFORE the ABAC/ReBAC path — denies early without touching
/// the compiled predicate cache (because env.now() changes per request).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TemporalPolicy {
    pub id: TemporalPolicyId,
    pub permission_id: PermissionId,
    pub name: String,
    /// ISO weekday numbers (1=Mon, 7=Sun). Default: Mon–Fri.
    pub allowed_days: Vec<u8>,
    pub allowed_from: NaiveTime,
    pub allowed_until: NaiveTime,
    pub timezone: String,
    /// Optional CIDR whitelist for client IP. `None` = no IP restriction.
    pub allowed_cidr: Option<Vec<String>>,
    /// If `true`, user must have an active shift record to pass this gate.
    pub require_shift: bool,
    /// Reference to the shift table, e.g. `"shift_schedule:user_id"`.
    pub shift_table_ref: Option<String>,
    pub is_active: bool,
    pub metadata: super::metadata::EntityMetadata,
}

// ─── External Attribute Source (EC-4) ────────────────────────────────────────

/// Registry of external services that can provide user attributes JIT.
///
/// Used when a policy condition references an attribute that does not live
/// in `user_account.attributes` (e.g. `shift_status` from a Shift Service).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExternalAttributeSource {
    pub id: crate::ids::ExternalAttributeSourceId,
    pub tenant_id: TenantId,
    /// Short code referenced in AST: `{ "type": "external_attr", "source": "shift_service" }`.
    pub code: String,
    pub base_url: String,
    /// URL template, e.g. `"/internal/users/{userId}/attributes"`.
    pub attribute_path: String,
    pub cacheable: bool,
    /// How long to cache the fetched attributes (seconds).
    pub cache_ttl_secs: i32,
    /// Request timeout in milliseconds. Must be short — AuthZ is on the hot path.
    pub timeout_ms: i32,
    /// Value to return when the external source is unavailable.
    /// `None` means fail-closed (deny).
    pub fallback_value: Option<JsonValue>,
    pub metadata: super::metadata::EntityMetadata,
}

// ─── Filter Result (output of translators) ────────────────────────────────────

/// The result of translating a set of row filters for a specific backend.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RowFilterResult {
    pub backend: FilterBackend,
    pub sql_where: Option<SqlFilterResult>,
    pub es_filter: Option<JsonValue>,
    pub mongo_filter: Option<JsonValue>,
}

/// SQL-specific filter result with parameterized query.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SqlFilterResult {
    pub predicate: String,
    pub params: std::collections::HashMap<String, JsonValue>,
}

/// Supported filter backends.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum FilterBackend {
    Sql,
    Elasticsearch,
    Mongodb,
}

impl FilterBackend {
    pub fn as_str(self) -> &'static str {
        match self {
            FilterBackend::Sql => "sql",
            FilterBackend::Elasticsearch => "elasticsearch",
            FilterBackend::Mongodb => "mongodb",
        }
    }
}
