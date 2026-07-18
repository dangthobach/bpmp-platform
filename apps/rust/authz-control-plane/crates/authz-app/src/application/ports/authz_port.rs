//! PEP outbound port — the only contract through which the application
//! consults the PDP. Adapter lives in `infrastructure::authz_adapter`.

use async_trait::async_trait;
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::application::errors::AppError;

/// Authenticated subject extracted from the JWT.
/// Constructed by middleware and passed by reference to every use-case.
#[derive(Debug, Clone)]
pub struct Subject {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub attributes: JsonValue,
    pub attributes_version: i64,
    /// Correlation id of the originating request.
    pub request_id: String,
}

#[derive(Debug, Clone)]
pub struct ResourceRef {
    pub resource_type: String,
    pub resource_ref: Option<String>,
    pub attributes: Option<JsonValue>,
}

#[derive(Debug, Clone)]
pub struct SqlFilter {
    /// Backend-specific filter payload returned by the PDP.
    /// For SQL backend, this is a JSON-encoded WHERE fragment.
    pub raw: JsonValue,
}

#[async_trait]
pub trait AuthzPort: Send + Sync {
    /// Returns `Ok(())` on ALLOW; `AppError::Forbidden` on DENY.
    /// Any transport error maps to `AuthzUnavailable` so the PEP can
    /// apply its fail-mode policy.
    async fn authorize(
        &self,
        sub: &Subject,
        action: &str,
        res: &ResourceRef,
    ) -> Result<(), AppError>;

    /// Returns a backend filter fragment to be ANDed with the base query.
    async fn filter(
        &self,
        sub: &Subject,
        resource_type: &str,
        action: &str,
        backend: &str,
    ) -> Result<SqlFilter, AppError>;
}
