//! Audit and decision log models — G7 Policy Debugger.
//!
//! Every AuthZ decision is recorded with a full AST evaluation trace,
//! enabling the Explain API and Replay API.

use crate::ids::{AuditLogId, PolicyId, PolicyVersionId, TenantId};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::models::policy::AuthzDecision;

/// A complete record of an authorization decision.
///
/// Stored in `authz_decision_log`. The `eval_trace` field captures
/// the result of each AST node, enabling the Explain and Replay APIs.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuthzDecisionLog {
    pub id: AuditLogId,
    pub tenant_id: TenantId,
    pub user_id: Uuid,
    pub resource_type: String,
    pub resource_ref: Option<String>,
    pub action: String,
    pub decision: AuthzDecision,
    pub matched_policy_id: Option<PolicyId>,
    /// Link to the exact policy version that was evaluated.
    pub policy_version_id: Option<PolicyVersionId>,
    /// Node-by-node trace of the AST evaluation.
    pub eval_trace: EvalTrace,
    /// Snapshot of user attributes and resource attributes at decision time.
    pub context: DecisionContext,
    pub decided_at: DateTime<Utc>,
    /// ID of the sidecar pod that made the decision (for distributed deployments).
    pub sidecar_id: Option<String>,
}

/// Node-by-node trace of an AST evaluation for a single decision.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EvalTrace {
    pub decision: AuthzDecision,
    pub matched_policy: Option<String>,
    /// Whether a shadow policy produced a different decision.
    pub shadow_diverged: bool,
    /// The denial reason code (only set when decision = DENY).
    pub deny_reason: Option<String>,
    /// Traces for each layer of the evaluation pipeline.
    pub layers: EvalLayers,
}

/// Evaluation traces organized by the 5 pipeline layers.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct EvalLayers {
    pub temporal_gate: Option<LayerTrace>,
    pub rbac: Option<LayerTrace>,
    pub resource_acl: Option<LayerTrace>,
    pub abac: Option<AstNodeTrace>,
    pub rebac: Option<LayerTrace>,
}

/// A simple pass/fail trace for non-AST layers.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LayerTrace {
    pub passed: bool,
    pub reason: Option<String>,
}

/// Recursive trace of an AST node evaluation.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AstNodeTrace {
    /// Human-readable description of the node.
    pub node: String,
    pub result: bool,
    pub reason: Option<String>,
    /// Left-hand resolved value (for leaf nodes).
    pub left_value: Option<JsonValue>,
    /// Right-hand resolved value (for leaf nodes).
    pub right_value: Option<JsonValue>,
    /// Traces for child nodes (for AND/OR nodes).
    pub children: Vec<AstNodeTrace>,
}

/// Snapshot of the evaluation context at decision time.
///
/// Used by the Replay API to re-evaluate a past decision with the current policy.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecisionContext {
    pub user: JsonValue,
    pub resource: JsonValue,
    pub env: EnvContextSnapshot,
}

/// Snapshot of the environment at decision time.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EnvContextSnapshot {
    pub request_time: DateTime<Utc>,
    pub client_ip: Option<String>,
}

impl AuthzDecisionLog {
    /// Returns `true` if the shadow evaluation diverged from the active decision.
    pub fn is_shadow_diverged(&self) -> bool {
        self.eval_trace.shadow_diverged
    }
}
