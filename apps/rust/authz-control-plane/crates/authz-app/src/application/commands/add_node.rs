use std::sync::Arc;

use uuid::Uuid;

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, ResourceRef, Subject};
use crate::application::ports::unit_of_work::UnitOfWorkFactory;
use crate::domain::organization::{NodeId, NodeKind, OrgId};

pub struct AddNodeCommand {
    pub org_id: Uuid,
    pub parent_id: Uuid,
    pub kind: NodeKind,
    pub code: String,
    pub name: String,
    /// Optimistic-lock expected version for the aggregate.
    pub expected_version: i64,
}

pub struct Deps {
    pub authz: Arc<dyn AuthzPort>,
    pub uow_factory: Arc<dyn UnitOfWorkFactory>,
}

pub struct AddNodeResult {
    pub version: i64,
}

#[tracing::instrument(skip_all, fields(tenant = %sub.tenant_id, org = %cmd.org_id))]
pub async fn handle(
    cmd: AddNodeCommand,
    sub: &Subject,
    deps: &Deps,
) -> Result<AddNodeResult, AppError> {
    deps.authz
        .authorize(
            sub,
            "organization:write",
            &ResourceRef {
                resource_type: "organization".to_owned(),
                resource_ref: Some(cmd.org_id.to_string()),
                attributes: None,
            },
        )
        .await?;

    let mut uow = deps.uow_factory.begin().await?;
    let mut org = uow
        .organizations()
        .load(sub.tenant_id, OrgId(cmd.org_id))
        .await?;
    if org.version() != cmd.expected_version {
        return Err(AppError::Conflict(format!(
            "version mismatch: expected={} actual={}",
            cmd.expected_version,
            org.version()
        )));
    }
    let evt = org.add_node(NodeId(cmd.parent_id), cmd.kind, cmd.code, cmd.name)?;
    uow.organizations().save(&org, cmd.expected_version).await?;
    uow.outbox().enqueue_event(&evt, org.id().0, None).await?;
    uow.commit().await?;

    Ok(AddNodeResult {
        version: org.version(),
    })
}
