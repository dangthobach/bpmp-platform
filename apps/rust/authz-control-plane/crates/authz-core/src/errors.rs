//! Unified error type for the entire AuthZ platform.
//!
//! ## Design decisions
//! - `thiserror` derives `std::error::Error` with structured variants.
//! - Every variant carries enough context for structured logging.
//! - No `anyhow::Error` in the hot evaluation path — only at the HTTP boundary.
//! - `#[non_exhaustive]` allows adding variants without breaking callers.

use thiserror::Error;
use uuid::Uuid;

/// Top-level error type for all AuthZ operations.
#[derive(Debug, Error)]
#[non_exhaustive]
pub enum AuthzError {
    // ── Identity & Tenant ──────────────────────────────────────────────────
    /// Tenant was not found or is inactive.
    #[error("tenant not found: {tenant_id}")]
    TenantNotFound { tenant_id: Uuid },

    /// Tenant exists but is suspended or inactive.
    #[error("tenant is inactive: {tenant_id}")]
    TenantInactive { tenant_id: Uuid },

    /// User was not found within the given tenant.
    #[error("user not found: {user_id} in tenant {tenant_id}")]
    UserNotFound { user_id: Uuid, tenant_id: Uuid },

    /// User account is deactivated.
    #[error("user account is deactivated: {user_id}")]
    UserDeactivated { user_id: Uuid },

    /// A mutable entity changed after the caller read it.
    #[error(
        "version conflict for {entity} {entity_id}: expected {expected_version}, actual {actual_version}"
    )]
    VersionConflict {
        entity: &'static str,
        entity_id: Uuid,
        expected_version: i64,
        actual_version: i64,
    },

    // ── Authentication ──────────────────────────────────────────────────────
    /// JWT token is missing, expired, or invalid.
    #[error("invalid JWT token: {reason}")]
    InvalidToken { reason: String },

    /// Required claim is missing from the JWT.
    #[error("missing JWT claim: {claim}")]
    MissingClaim { claim: &'static str },

    // ── Authorization ───────────────────────────────────────────────────────
    /// Emergency revoke is in effect for this user.
    #[error("access denied: emergency revoke active for user {user_id}")]
    EmergencyRevoked { user_id: Uuid },

    /// Temporal gate blocked the request.
    #[error("access denied by temporal policy: {reason}")]
    TemporalGateDenied { reason: String },

    /// ABAC/RBAC evaluation denied the request.
    #[error("access denied by policy: {policy_name} (reason: {reason})")]
    PolicyDenied { policy_name: String, reason: String },

    /// ReBAC graph check denied the request.
    #[error("access denied: no qualifying relation found")]
    ReBacDenied,

    // ── Resource ────────────────────────────────────────────────────────────
    /// Resource type is not registered in this tenant.
    #[error("unknown resource type: {resource_type} for tenant {tenant_id}")]
    UnknownResourceType {
        resource_type: String,
        tenant_id: Uuid,
    },

    /// Unknown field referenced in policy AST.
    #[error("unknown field '{field}' for resource type '{resource_type}'")]
    UnknownField {
        field: String,
        resource_type: String,
    },

    // ── Policy ──────────────────────────────────────────────────────────────
    /// Policy or policy version was not found.
    #[error("policy not found: {policy_id}")]
    PolicyNotFound { policy_id: Uuid },

    /// A concrete immutable policy version was not found.
    #[error("policy version not found: {version_id}")]
    PolicyVersionNotFound { version_id: Uuid },

    /// Policy is not in the expected lifecycle state.
    #[error("policy version {version_id} is not in state {expected_state}")]
    InvalidPolicyState {
        version_id: Uuid,
        expected_state: &'static str,
    },

    /// AST node contains an unsupported operator.
    #[error("unsupported AST operator: '{operator}' for node type '{node_type}'")]
    UnsupportedOperator { operator: String, node_type: String },

    /// AST expression could not be parsed from JSON.
    #[error("invalid condition expression: {reason}")]
    InvalidConditionExpr { reason: String },

    // ── ReBAC Graph ─────────────────────────────────────────────────────────
    /// Inserting a relation tuple would create a cycle.
    #[error("cycle detected: ({subject}) -[{relation}]-> ({object}) would create a cycle")]
    RelationCycleDetected {
        subject: String,
        relation: String,
        object: String,
    },

    /// Maximum fan-out for a relation type exceeded.
    #[error("fan-out limit exceeded: subject={subject} relation={relation} limit={limit}")]
    FanoutLimitExceeded {
        subject: String,
        relation: String,
        limit: i32,
    },

    /// ReBAC live traversal exceeded maximum depth.
    #[error("ReBAC traversal depth exceeded maximum {max_depth} hops")]
    ReBacDepthExceeded { max_depth: u32 },

    /// ReBAC circuit breaker is open.
    #[error("ReBAC circuit breaker open for tenant {tenant_id}")]
    ReBacCircuitOpen { tenant_id: Uuid },

    // ── JIT Attribute Fetching ───────────────────────────────────────────────
    /// External attribute source returned an error or timed out.
    #[error("JIT attribute fetch failed: source={fetch_source} key={key} reason={reason}")]
    JitAttributeUnavailable {
        fetch_source: String,
        key: String,
        reason: String,
    },

    // ── Filter / Data Layer ──────────────────────────────────────────────────
    /// Filter backend is not supported for this operation.
    #[error("unsupported filter backend: {backend}")]
    UnsupportedFilterBackend { backend: String },

    /// ES terms filter would exceed the maximum allowed size.
    #[error(
        "ReBAC terms filter too large: {size} > {max_size}; consider pre-materializing relations"
    )]
    TermsFilterTooLarge { size: usize, max_size: usize },

    // ── Escape Hatch Governance ──────────────────────────────────────────────
    /// Escape hatch SQL/ES/Mongo fragment inserted without required approval.
    #[error("escape hatch requires approval: set approved_by, reason, and ticket_ref")]
    EscapeHatchNotApproved,

    // ── Input Validation ─────────────────────────────────────────────────────
    /// Request failed validation.
    #[error("invalid request: {reason}")]
    InvalidRequest { reason: String },

    // ── Infrastructure ────────────────────────────────────────────────────────
    /// Database operation failed.
    #[error("database error: {0}")]
    Database(#[from] sqlx::Error),

    /// Serialization or deserialization error.
    #[error("serialization error: {0}")]
    Serialization(#[from] serde_json::Error),

    /// Unexpected internal error (should be rare in production).
    #[error("internal error: {0}")]
    Internal(String),
}

impl AuthzError {
    /// Returns the machine-readable error code for logging and API responses.
    pub fn error_code(&self) -> &'static str {
        match self {
            AuthzError::TenantNotFound { .. } => "TENANT_NOT_FOUND",
            AuthzError::TenantInactive { .. } => "TENANT_INACTIVE",
            AuthzError::UserNotFound { .. } => "USER_NOT_FOUND",
            AuthzError::UserDeactivated { .. } => "USER_DEACTIVATED",
            AuthzError::VersionConflict { .. } => "VERSION_CONFLICT",
            AuthzError::InvalidToken { .. } => "INVALID_TOKEN",
            AuthzError::MissingClaim { .. } => "MISSING_CLAIM",
            AuthzError::EmergencyRevoked { .. } => "EMERGENCY_REVOKED",
            AuthzError::TemporalGateDenied { .. } => "TEMPORAL_GATE_DENIED",
            AuthzError::PolicyDenied { .. } => "POLICY_DENIED",
            AuthzError::ReBacDenied => "REBAC_DENIED",
            AuthzError::UnknownResourceType { .. } => "UNKNOWN_RESOURCE_TYPE",
            AuthzError::UnknownField { .. } => "UNKNOWN_FIELD",
            AuthzError::PolicyNotFound { .. } => "POLICY_NOT_FOUND",
            AuthzError::PolicyVersionNotFound { .. } => "POLICY_VERSION_NOT_FOUND",
            AuthzError::InvalidPolicyState { .. } => "INVALID_POLICY_STATE",
            AuthzError::UnsupportedOperator { .. } => "UNSUPPORTED_OPERATOR",
            AuthzError::InvalidConditionExpr { .. } => "INVALID_CONDITION_EXPR",
            AuthzError::RelationCycleDetected { .. } => "RELATION_CYCLE_DETECTED",
            AuthzError::FanoutLimitExceeded { .. } => "FANOUT_LIMIT_EXCEEDED",
            AuthzError::ReBacDepthExceeded { .. } => "REBAC_DEPTH_EXCEEDED",
            AuthzError::ReBacCircuitOpen { .. } => "REBAC_CIRCUIT_OPEN",
            AuthzError::JitAttributeUnavailable { .. } => "JIT_UNAVAILABLE",
            AuthzError::UnsupportedFilterBackend { .. } => "UNSUPPORTED_FILTER_BACKEND",
            AuthzError::TermsFilterTooLarge { .. } => "TERMS_FILTER_TOO_LARGE",
            AuthzError::EscapeHatchNotApproved => "ESCAPE_HATCH_NOT_APPROVED",
            AuthzError::InvalidRequest { .. } => "INVALID_REQUEST",
            AuthzError::Database(_) => "DATABASE_ERROR",
            AuthzError::Serialization(_) => "SERIALIZATION_ERROR",
            AuthzError::Internal(_) => "INTERNAL_ERROR",
        }
    }

    /// Returns `true` if this error represents an authorization denial
    /// (as opposed to an infrastructure or input error).
    pub fn is_denial(&self) -> bool {
        matches!(
            self,
            AuthzError::EmergencyRevoked { .. }
                | AuthzError::TenantInactive { .. }
                | AuthzError::TemporalGateDenied { .. }
                | AuthzError::PolicyDenied { .. }
                | AuthzError::ReBacDenied
        )
    }

    /// Returns `true` if this error is safe to expose in the API response
    /// (no internal details that could aid an attacker).
    pub fn is_safe_to_expose(&self) -> bool {
        !matches!(self, AuthzError::Database(_) | AuthzError::Internal(_))
    }
}
