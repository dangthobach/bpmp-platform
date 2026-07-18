//! RBAC domain models — Layer B.
//!
//! Covers hierarchical roles, permissions, and user-role assignments.

use crate::ids::{PermissionId, RoleId, TenantId, UserId};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;

/// A role in the hierarchical RBAC system.
///
/// Roles form a tree via `parent_role_id`. The policy engine traverses the
/// tree upward to collect all inherited permissions.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Role {
    pub id: RoleId,
    pub tenant_id: TenantId,
    /// Unique short code within a tenant. Example: `BRANCH_MANAGER`.
    pub code: String,
    pub name: String,
    /// Parent role for inheritance. `None` means this is a root role.
    pub parent_role_id: Option<RoleId>,
    /// Higher priority roles take precedence in DENY-override evaluation.
    pub priority: i32,
    pub metadata: super::metadata::EntityMetadata,
}

/// A permission granted by a role.
///
/// Permissions define what actions are allowed on which resource type
/// and at what scope.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Permission {
    pub id: PermissionId,
    pub tenant_id: TenantId,
    /// Unique short code. Example: `DOC_READ_BRANCH`.
    pub code: String,
    pub resource_type: String,
    pub action: String,
    pub scope: PermissionScope,
    pub metadata: super::metadata::EntityMetadata,
}

/// The scope of a permission — how broadly it applies.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PermissionScope {
    /// Only resources the user owns.
    Own,
    /// All resources within the user's branch.
    Branch,
    /// All resources in the tenant.
    All,
}

/// Association between a role and a permission, with optional extra conditions.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RolePermission {
    pub role_id: RoleId,
    pub permission_id: PermissionId,
    /// Optional additional ABAC conditions beyond the permission's base scope.
    pub conditions: Option<JsonValue>,
    pub metadata: super::metadata::EntityMetadata,
}

/// Assignment of a role to a user, optionally scoped to a resource instance.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UserRole {
    pub user_id: UserId,
    pub role_id: RoleId,
    /// When set, the role applies only to this specific resource instance.
    /// Example: user A is REVIEWER only for contract batch #456.
    pub resource_scope_id: Option<crate::ids::ResourceInstanceId>,
    /// When set, the role assignment expires at this time (temporary permission).
    pub expires_at: Option<DateTime<Utc>>,
    pub metadata: super::metadata::EntityMetadata,
}

impl UserRole {
    /// Returns `true` if this assignment is still valid (not expired).
    pub fn is_active(&self) -> bool {
        match self.expires_at {
            None => true,
            Some(expires) => expires > Utc::now(),
        }
    }
}

/// Aggregated result of resolving a user's effective permissions for a
/// given resource type — used as the RBAC evaluation output.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EffectivePermissions {
    pub user_id: UserId,
    pub tenant_id: TenantId,
    pub resource_type: String,
    /// All permissions the user has via their role hierarchy.
    pub permissions: Vec<ResolvedPermission>,
}

/// A permission with its originating role and any associated conditions.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResolvedPermission {
    pub permission: Permission,
    pub role_id: RoleId,
    pub role_code: String,
    /// Conditions from `role_permission.conditions`, if any.
    pub extra_conditions: Option<JsonValue>,
    /// Row filter expressions associated with this permission.
    pub row_filter_exprs: Vec<JsonValue>,
    /// Field filter configuration for this permission.
    pub field_filter: Option<FieldFilterConfig>,
    /// Policy condition expression linked to this permission.
    pub policy_condition: Option<JsonValue>,
    pub policy_effect: PolicyEffect,
    pub policy_priority: i32,
}

/// Field filter configuration associated with a permission.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FieldFilterConfig {
    pub allowed_fields: Vec<String>,
    pub masked_fields: Vec<String>,
    pub mask_pattern: Option<String>,
}

/// The effect of a policy rule — ALLOW or DENY.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum PolicyEffect {
    Allow,
    Deny,
}
