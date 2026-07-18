use std::sync::Arc;

use uuid::Uuid;

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, ResourceRef, Subject};
use crate::application::ports::unit_of_work::UnitOfWorkFactory;
use crate::domain::organization::Organization;

pub struct CreateOrganizationCommand {
    pub code: String,
    pub name: String,
}

pub struct Deps {
    pub authz: Arc<dyn AuthzPort>,
    pub uow_factory: Arc<dyn UnitOfWorkFactory>,
}

pub struct CreateOrganizationResult {
    pub org_id: Uuid,
    pub version: i64,
}

#[tracing::instrument(skip_all, fields(tenant = %sub.tenant_id, code = %cmd.code))]
pub async fn handle(
    cmd: CreateOrganizationCommand,
    sub: &Subject,
    deps: &Deps,
) -> Result<CreateOrganizationResult, AppError> {
    // 1) Authorize ───────────────────────────────────────────────────────────
    deps.authz
        .authorize(
            sub,
            "organization:create",
            &ResourceRef {
                resource_type: "organization".to_owned(),
                resource_ref: None,
                attributes: None,
            },
        )
        .await?;

    // 2) Domain step ─────────────────────────────────────────────────────────
    let (org, evt) = Organization::create_root(sub.tenant_id, cmd.code, cmd.name)?;

    // 3) Persist in one transaction ──────────────────────────────────────────
    let mut uow = deps.uow_factory.begin().await?;
    uow.organizations().save(&org, 0).await?;
    uow.outbox().enqueue_event(&evt, org.id().0, None).await?;
    uow.commit().await?;

    metrics::counter!("authz_app_organization_created_total").increment(1);
    Ok(CreateOrganizationResult {
        org_id: org.id().0,
        version: org.version(),
    })
}
