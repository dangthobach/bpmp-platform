use std::sync::Arc;

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, ResourceRef, Subject};
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

#[tracing::instrument(skip_all, fields(tenant = %sub.tenant_id))]
pub async fn handle(
    q: ListOrganizationsQuery,
    sub: &Subject,
    deps: &Deps,
) -> Result<Vec<OrganizationListItem>, AppError> {
    deps.authz
        .authorize(
            sub,
            "organization:read",
            &ResourceRef {
                resource_type: "organization".to_owned(),
                resource_ref: None,
                attributes: None,
            },
        )
        .await?;

    let mut uow = deps.uow_factory.begin().await?;
    let items = uow
        .organizations()
        .list(sub.tenant_id, q.offset, q.limit)
        .await?;
    uow.commit().await?;
    Ok(items)
}
