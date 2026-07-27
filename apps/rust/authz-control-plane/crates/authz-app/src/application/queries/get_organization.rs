use std::sync::Arc;

use uuid::Uuid;

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, ResourceRef, Subject};
use crate::application::ports::unit_of_work::UnitOfWorkFactory;
use crate::domain::organization::{OrgId, Organization};

pub struct GetOrganizationQuery {
    pub org_id: Uuid,
}

pub struct Deps {
    pub authz: Arc<dyn AuthzPort>,
    pub uow_factory: Arc<dyn UnitOfWorkFactory>,
}

#[tracing::instrument(skip_all, fields(tenant = %sub.tenant_id, org = %query.org_id))]
pub async fn handle(
    query: GetOrganizationQuery,
    sub: &Subject,
    deps: &Deps,
) -> Result<Organization, AppError> {
    deps.authz
        .authorize(
            sub,
            "organization:read",
            &ResourceRef {
                resource_type: "organization".to_owned(),
                resource_ref: Some(query.org_id.to_string()),
                attributes: None,
            },
        )
        .await?;

    let mut uow = deps.uow_factory.begin().await?;
    let organization = uow
        .organizations()
        .load(sub.tenant_id, OrgId(query.org_id))
        .await?;
    uow.commit().await?;
    Ok(organization)
}
