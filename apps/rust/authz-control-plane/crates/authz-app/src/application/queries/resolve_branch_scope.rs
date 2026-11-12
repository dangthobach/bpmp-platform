use std::sync::Arc;

use uuid::Uuid;

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, ResourceRef, Subject};
use crate::application::ports::organization_repo::BranchScope;
use crate::application::ports::unit_of_work::UnitOfWorkFactory;

pub struct ResolveBranchScopeQuery {
    pub branch_id: Uuid,
}

pub struct Deps {
    pub authz: Arc<dyn AuthzPort>,
    pub uow_factory: Arc<dyn UnitOfWorkFactory>,
}

#[tracing::instrument(skip_all, fields(tenant = %sub.tenant_id, branch = %query.branch_id))]
pub async fn handle(
    query: ResolveBranchScopeQuery,
    sub: &Subject,
    deps: &Deps,
) -> Result<BranchScope, AppError> {
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
    let scope = uow
        .organizations()
        .resolve_branch_scope(sub.tenant_id, query.branch_id)
        .await?;
    uow.commit().await?;
    Ok(scope)
}
