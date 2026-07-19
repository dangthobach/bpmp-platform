//! Tenant and user identity models.
//!
//! These are the Layer A domain objects — identity and multi-tenancy.

use crate::ids::{TenantId, UserId};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use uuid::Uuid;

/// A tenant in the multi-tenant deployment.
///
/// All entities in the system are scoped to a tenant.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Tenant {
    pub id: TenantId,
    pub code: String,
    pub name: String,
    pub is_active: bool,
    /// Tenant-specific configuration (fail_mode, rate limits, etc.)
    pub config: TenantConfig,
    pub metadata: super::metadata::EntityMetadata,
}

/// Per-tenant configuration that drives AuthZ engine behavior.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TenantConfig {
    /// What to do when policy bundle is unavailable.
    /// "DENY" (default, banking) or "OPEN" (internal tools).
    pub fail_mode: FailMode,

    /// Maximum ReBAC traversal depth for this tenant.
    pub rebac_max_depth: u32,

    /// Whether shadow mode evaluation is active for any policies.
    pub shadow_mode_enabled: bool,
}

impl Default for TenantConfig {
    fn default() -> Self {
        Self {
            fail_mode: FailMode::Deny,
            rebac_max_depth: 10,
            shadow_mode_enabled: false,
        }
    }
}

/// Fail mode determines AuthZ behavior when the policy engine cannot evaluate.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum FailMode {
    /// Deny all access when policy unavailable (banking default).
    Deny,
    /// Allow access when policy unavailable (internal tools).
    Open,
}

/// A user account within a tenant.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UserAccount {
    pub id: UserId,
    pub tenant_id: TenantId,
    pub username: String,
    /// External identity provider subject ID (e.g. Keycloak `sub` claim).
    pub external_id: Option<String>,
    /// Dynamic user attributes used in ABAC evaluation.
    /// Example: `{"branch_code": "HN01", "level": 3}`
    pub attributes: JsonValue,
    /// Monotonic version incremented on every attribute sync.
    /// Cache keys embed this version to detect staleness without TTL.
    pub attributes_version: i64,
    pub is_active: bool,
    pub metadata: super::metadata::EntityMetadata,
}

impl UserAccount {
    /// Returns an attribute value by key.
    pub fn get_attribute(&self, key: &str) -> Option<&JsonValue> {
        self.attributes.get(key)
    }

    /// Returns the cache key suffix embedding the current attributes version.
    /// Format: `{user_id}:{version}`
    pub fn cache_version_key(&self) -> String {
        format!("{}:{}", self.id, self.attributes_version)
    }
}

/// Audit trail entry for a single attribute change on a user account.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UserAttributeHistory {
    pub id: Uuid,
    pub user_id: UserId,
    pub attribute: String,
    pub old_value: Option<String>,
    pub new_value: Option<String>,
    pub changed_at: DateTime<Utc>,
    /// The admin or system job that triggered the change.
    pub changed_by: Option<UserId>,
}
