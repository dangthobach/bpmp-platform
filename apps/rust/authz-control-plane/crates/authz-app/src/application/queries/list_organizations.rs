use std::sync::Arc;

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, Subject};
use crate::application::ports::organization_repo::OrganizationListItem;
use crate::application::ports::unit_of_work::UnitOfWorkFactory;

pub struct ListOrganizationsQuery {
    pub offset: i64,
    pub limit: i64,
}

pub struct Deps {
    pub authz: Arc<dyn AuthzPort>,
    pub uow_factory: Arc<dyn UnitOfWorkFactory>,
}

/// PEP pattern for read paths: ask PDP for a filter, then push it down to SQL.
/// In this scaffolding we apply only the tenant filter at the repository layer
/// and request a placeholder filter from the PDP to demonstrate the call path.
#[tracing::instrument(skip_all, fields(tenant = %sub.tenant_id))]
pub async fn handle(
    q: ListOrganizationsQuery,
    sub: &Subject,
    deps: &Deps,
) -> Result<Vec<OrganizationListItem>, AppError> {
    let _filter = deps
        .authz
        .filter(sub, "organization", "read", "sql")
        .await
        .ok(); // soft-fail: list operations apply tenant guard regardless.

    let mut uow = deps.uow_factory.begin().await?;
    let items = uow
        .organizations()
        .list(sub.tenant_id, q.offset, q.limit)
        .await?;
    uow.commit().await?;
    Ok(items)
}
