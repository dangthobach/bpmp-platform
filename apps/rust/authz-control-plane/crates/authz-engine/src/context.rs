//! AuthZ evaluation context — carries all inputs needed to evaluate a request.

use authz_core::{
    ids::{TenantId, UserId},
    models::filter::FilterBackend,
};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use std::net::IpAddr;

/// The full context required for a single AuthZ evaluation request.
///
/// Immutable after construction — all evaluation stages receive a shared reference.
#[derive(Debug, Clone)]
pub struct AuthzContext {
    pub tenant_id: TenantId,
    pub user_id: UserId,
    pub user_attributes: JsonValue,
    pub user_attributes_version: i64,
    pub resource: ResourceContext,
    pub env: EnvContext,
    pub backend: FilterBackend,
}

impl AuthzContext {
    /// Retrieves a user attribute value by key.
    pub fn user_attr(&self, key: &str) -> Option<&JsonValue> {
        self.user_attributes.get(key)
    }

    /// Retrieves a resource attribute value by key.
    pub fn resource_attr(&self, key: &str) -> Option<&JsonValue> {
        self.resource.attributes.get(key)
    }

    /// Generates the cache key for this context, incorporating the attributes version.
    /// Format: `authz:ctx:{tenant_id}:{user_id}:{version}`
    pub fn cache_key(&self) -> String {
        format!(
            "authz:ctx:{}:{}:{}",
            self.tenant_id, self.user_id, self.user_attributes_version
        )
    }
}

/// Information about the resource being accessed.
#[derive(Debug, Clone)]
pub struct ResourceContext {
    /// The resource type code, e.g. `"document"`, `"contract"`.
    pub resource_type: String,
    /// The external reference ID (domain service's own ID).
    pub resource_ref: Option<String>,
    /// Resource-level attributes used in ABAC evaluation.
    pub attributes: JsonValue,
}

/// Environment context resolved at request time.
///
/// ## EC-1: Temporal values MUST come from here — never from cache.
/// Values like `now`, `client_ip` change per-request and must not be
/// embedded in compiled predicates.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EnvContext {
    /// Timestamp when the request arrived. Used for temporal gate checks.
    pub request_time: DateTime<Utc>,
    /// Client IP address from the HTTP request (X-Forwarded-For or remote addr).
    pub client_ip: Option<IpAddr>,
}

impl Default for EnvContext {
    fn default() -> Self {
        Self {
            request_time: Utc::now(),
            client_ip: None,
        }
    }
}

impl EnvContext {
    /// Creates an EnvContext with the current time and optional IP.
    pub fn now_with_ip(client_ip: Option<IpAddr>) -> Self {
        Self {
            request_time: Utc::now(),
            client_ip,
        }
    }
}
