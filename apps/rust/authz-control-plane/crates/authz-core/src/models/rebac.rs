//! ReBAC (Relationship-based Access Control) models — Layer D (graph component).
//!
//! Implements a Zanzibar-style relation tuple model.

use crate::ids::{RelationTupleId, TenantId};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// A Zanzibar-style relation tuple: `(subject) --[relation]--> (object)`.
///
/// ## String encoding
/// Subjects and objects are encoded as `"type:id"` strings, e.g.:
/// - `"user:550e8400-e29b-41d4-a716-446655440000"`
/// - `"group:ALL_EMPLOYEES_HN"`
/// - `"contract:bfec-..."`
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RelationTuple {
    pub id: RelationTupleId,
    pub tenant_id: TenantId,
    /// The subject of the relation (who has the relation).
    pub subject: String,
    /// The relation name, e.g. `delegate_of`, `member_of`, `reviewer_of`.
    pub relation: String,
    /// The object of the relation (to whom/what).
    pub object: String,
    /// When set, this relation tuple expires and is treated as inactive.
    pub expires_at: Option<DateTime<Utc>>,
    pub metadata: super::metadata::EntityMetadata,
}

impl RelationTuple {
    /// Returns `true` if this tuple is still active (not expired).
    pub fn is_active(&self) -> bool {
        match self.expires_at {
            None => true,
            Some(exp) => exp > Utc::now(),
        }
    }

    /// Encode a subject reference as `"type:uuid"`.
    pub fn encode_user(user_id: uuid::Uuid) -> String {
        format!("user:{}", user_id)
    }

    /// Encode a group reference.
    pub fn encode_group(group_code: &str) -> String {
        format!("group:{}", group_code)
    }

    /// Encode a resource/contract reference.
    pub fn encode_resource(resource_type: &str, id: uuid::Uuid) -> String {
        format!("{}:{}", resource_type, id)
    }
}

/// Pre-computed reachability record for the ReBAC materialized graph.
///
/// Maintained incrementally by the CDC consumer when `relation_tuple` changes.
/// Allows O(1) lookup instead of O(depth) recursive traversal.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RelationReachability {
    pub id: uuid::Uuid,
    pub tenant_id: TenantId,
    pub subject: String,
    pub relation: String,
    /// Every object reachable from `subject` via `relation` (transitive).
    pub object: String,
    /// Number of hops from subject to object.
    pub depth: i32,
    /// Ordered list of nodes along the path — for debugging.
    pub path: Vec<String>,
    pub computed_at: DateTime<Utc>,
}

/// Constraint on a relation type — max fan-out limit.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RelationType {
    pub id: uuid::Uuid,
    pub tenant_id: TenantId,
    pub relation: String,
    /// Maximum number of objects a single subject can have with this relation.
    /// `None` means unlimited.
    pub max_fanout: Option<i32>,
    pub metadata: super::metadata::EntityMetadata,
}

/// A virtual group partition used to decompose "Big Node" groups.
///
/// When a group exceeds the max_fanout limit, it's split into sub-groups
/// each with fewer members. The policy engine traverses the partition tree.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GroupPartition {
    pub id: uuid::Uuid,
    pub tenant_id: TenantId,
    pub parent_group: String,
    pub child_group: String,
    /// The rule that determined this partition (e.g. `"branch_code=HN"`).
    pub partition_key: Option<String>,
    pub max_size: i32,
    pub metadata: super::metadata::EntityMetadata,
}
