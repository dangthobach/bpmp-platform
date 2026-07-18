//! Policy and ABAC AST models — Layer D.
//!
//! Contains the JSON AST type system for condition expressions,
//! policy lifecycle models, and shadow mode evaluation types.

use crate::ids::{PolicyId, PolicyRuleId, PolicyVersionId, TenantId};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use uuid::Uuid;

// ─── Policy Lifecycle ─────────────────────────────────────────────────────────

/// An authorization policy — a named group of rules with an effect and priority.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Policy {
    pub id: PolicyId,
    pub tenant_id: TenantId,
    pub name: String,
    pub effect: PolicyEffect,
    /// DENY policies with higher priority override ALLOW policies.
    pub priority: i32,
    pub is_active: bool,
    pub metadata: super::metadata::EntityMetadata,
}

/// The effect of evaluating a policy.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum PolicyEffect {
    Allow,
    Deny,
}

/// A rule within a policy, binding it to a subject type, resource, and action.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PolicyRule {
    pub id: PolicyRuleId,
    pub policy_id: PolicyId,
    pub subject_type: SubjectType,
    pub resource_type: String,
    pub action: String,
    /// The ABAC condition expression as a JSON AST.
    pub condition_expr: ConditionNode,
    pub metadata: super::metadata::EntityMetadata,
}

/// Types of subjects that policy rules can target.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum SubjectType {
    Role,
    User,
    Group,
}

// ─── Policy Versioning ────────────────────────────────────────────────────────

/// A versioned snapshot of a policy at a specific point in time.
///
/// Lifecycle: DRAFT → SHADOW → ACTIVE → ARCHIVED
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PolicyVersion {
    pub id: PolicyVersionId,
    pub policy_id: PolicyId,
    pub version_num: i32,
    /// Full snapshot of all policy rules at publish time.
    pub snapshot: JsonValue,
    pub status: PolicyVersionStatus,
    pub published_by: Option<Uuid>,
    pub published_at: Option<DateTime<Utc>>,
    pub notes: Option<String>,
    pub metadata: super::metadata::EntityMetadata,
}

/// The lifecycle state of a policy version.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum PolicyVersionStatus {
    Draft,
    Shadow,
    Active,
    Archived,
}

/// A shadow divergence log entry — recorded when shadow and active policies disagree.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PolicyShadowLog {
    pub id: Uuid,
    pub policy_version_id: PolicyVersionId,
    pub user_id: Option<Uuid>,
    pub resource_ref: Option<String>,
    pub action: Option<String>,
    /// Decision from the SHADOW policy version.
    pub shadow_decision: AuthzDecision,
    /// Decision from the currently ACTIVE policy version.
    pub active_decision: AuthzDecision,
    /// `true` when shadow and active disagree.
    pub diverged: bool,
    /// Full context snapshot for replay and debugging.
    pub context_snapshot: Option<JsonValue>,
    pub logged_at: DateTime<Utc>,
}

/// The authorization decision outcome.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum AuthzDecision {
    Allow,
    Deny,
}

// ─── ABAC AST Types ───────────────────────────────────────────────────────────

/// A node in the ABAC condition AST.
///
/// ## Design
/// The AST is stored as JSONB in `policy_rule.condition_expr` and
/// `row_filter.filter_expr`. The evaluator walks this tree recursively.
///
/// Example JSON representation:
/// ```json
/// {
///   "operator": "AND",
///   "conditions": [
///     {
///       "left": { "type": "user_attr", "key": "branch_code" },
///       "op": "eq",
///       "right": { "type": "resource_field", "key": "branchCode" }
///     }
///   ]
/// }
/// ```
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "operator", rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ConditionNode {
    /// All child conditions must be true.
    And { conditions: Vec<ConditionNode> },
    /// At least one child condition must be true.
    Or { conditions: Vec<ConditionNode> },
    /// A leaf condition comparing two values with an operator.
    #[serde(rename = "LEAF")]
    Leaf(LeafCondition),
}

/// A single comparison expression: `left op right`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LeafCondition {
    pub left: ValueSource,
    pub op: ComparisonOperator,
    pub right: ValueSource,
}

/// The source of a value in a condition expression.
///
/// Each variant defines where to look up the value at evaluation time.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ValueSource {
    /// A user attribute from `user_account.attributes`.
    UserAttr { key: String },

    /// A field from the resource being evaluated.
    /// Mapped to backend-specific names via `schema_field_registry`.
    ResourceField { key: String },

    /// A hardcoded constant value.
    Literal { value: JsonValue },

    /// An environment value resolved at request time (never cached).
    Env { kind: EnvKind },

    /// Triggers a ReBAC graph traversal.
    /// `target` is the path to the object, e.g. `"resource.owner_id"`.
    Relation { key: String, target: String },

    /// An attribute fetched JIT from an external service.
    ExternalAttr { source: String, key: String },
}

/// Environment values available in AST conditions.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum EnvKind {
    /// Current UTC timestamp.
    Now,
    /// Current UTC date (no time component).
    CurrentDate,
    /// Client IP address from the request.
    RequestIp,
}

/// Comparison operator for leaf conditions.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ComparisonOperator {
    Eq,
    Neq,
    In,
    NotIn,
    Gte,
    Lte,
    Like,
    IsNull,
    /// Only valid with `ValueSource::Relation` — triggers graph traversal.
    Exists,
}

impl ConditionNode {
    /// Returns `true` if this node or any descendant contains a `Relation` value source.
    /// Used by the pipeline to decide whether to invoke the ReBAC engine.
    pub fn has_relation_nodes(&self) -> bool {
        match self {
            ConditionNode::And { conditions } | ConditionNode::Or { conditions } => {
                conditions.iter().any(|c| c.has_relation_nodes())
            }
            ConditionNode::Leaf(leaf) => {
                matches!(&leaf.left, ValueSource::Relation { .. })
                    || matches!(&leaf.right, ValueSource::Relation { .. })
            }
        }
    }

    /// Returns `true` if this node or any descendant references an external attribute source.
    pub fn has_external_attr_nodes(&self) -> bool {
        match self {
            ConditionNode::And { conditions } | ConditionNode::Or { conditions } => {
                conditions.iter().any(|c| c.has_external_attr_nodes())
            }
            ConditionNode::Leaf(leaf) => {
                matches!(&leaf.left, ValueSource::ExternalAttr { .. })
                    || matches!(&leaf.right, ValueSource::ExternalAttr { .. })
            }
        }
    }
}
