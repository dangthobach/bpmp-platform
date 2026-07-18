//! Repository contract for the Organization aggregate.

use async_trait::async_trait;
use uuid::Uuid;

use crate::application::errors::AppError;
use crate::domain::organization::{OrgId, Organization};

/// Read-model row for `list_organizations`.
#[derive(Debug, Clone)]
pub struct OrganizationListItem {
    pub id: Uuid,
    pub code: String,
    pub name: String,
    pub root_path: String,
    pub node_count: i64,
    pub version: i64,
}

#[async_trait]
pub trait OrganizationRepository: Send + Sync {
    /// Load aggregate fully (root + all descendants).
    /// MUST scope by `tenant_id`.
    async fn load(&mut self, tenant_id: Uuid, id: OrgId) -> Result<Organization, AppError>;

    /// Persist the aggregate.
    ///
    /// Implementation MUST:
    /// * Check `expected_version` via optimistic lock (compare-and-swap).
    /// * Upsert nodes and delete those removed.
    /// * Bump `version` to `expected_version + 1` on success.
    async fn save(&mut self, org: &Organization, expected_version: i64) -> Result<(), AppError>;

    /// Paged read model — no domain logic, no joins beyond aggregates.
    async fn list(
        &mut self,
        tenant_id: Uuid,
        offset: i64,
        limit: i64,
    ) -> Result<Vec<OrganizationListItem>, AppError>;
}
