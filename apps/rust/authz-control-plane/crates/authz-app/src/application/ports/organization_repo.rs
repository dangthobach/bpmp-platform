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

/// Canonical branch ancestry returned by the organization owner.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BranchScope {
    pub organization_id: Uuid,
    pub organization_version: i64,
    /// Ordered root-to-branch. Contains organization nodes, including the
    /// branch itself, so consumers can evaluate descendant inheritance.
    pub ancestry: Vec<Uuid>,
}

/// Current active branch leaves covered by each requested branch root.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BranchDescendants {
    pub branch_id: Uuid,
    pub organization_id: Uuid,
    pub organization_version: i64,
    pub descendant_branch_ids: Vec<Uuid>,
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

    /// Resolves an active branch and its canonical ancestry within a tenant.
    async fn resolve_branch_scope(
        &mut self,
        tenant_id: Uuid,
        branch_id: Uuid,
    ) -> Result<BranchScope, AppError>;

    /// Resolves current active branch descendants for a bounded batch.
    async fn resolve_branch_descendants(
        &mut self,
        tenant_id: Uuid,
        branch_ids: &[Uuid],
        max_branches: usize,
    ) -> Result<Vec<BranchDescendants>, AppError>;
}
