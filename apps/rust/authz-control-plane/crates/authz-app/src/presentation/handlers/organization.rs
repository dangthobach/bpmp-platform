//! Organization HTTP API.

use axum::{
    extract::{Path, Query, State},
    Json,
};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::application::commands::{
    handle_add_node, handle_create_organization, handle_move_node, AddNodeCommand,
    CreateOrganizationCommand, MoveNodeCommand,
};
use crate::application::queries::{handle_list_organizations, ListOrganizationsQuery};
use crate::bootstrap::AppContainer;
use crate::domain::organization::NodeKind;
use crate::presentation::envelope::{created, ok, EnvelopeReply};
use crate::presentation::error::{ApiError, ApiResult};
use crate::presentation::extractors::SubjectExt;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CreateOrgBody {
    pub code: String,
    pub name: String,
}

#[derive(Serialize)]
pub struct CreateOrgResponse {
    pub id: Uuid,
    pub version: i64,
}

pub async fn create_organization(
    State(app): State<AppContainer>,
    SubjectExt(sub): SubjectExt,
    Json(body): Json<CreateOrgBody>,
) -> ApiResult<EnvelopeReply<CreateOrgResponse>> {
    let res = handle_create_organization(
        CreateOrganizationCommand {
            code: body.code,
            name: body.name,
        },
        &sub,
        &app.create_org_deps(),
    )
    .await
    .map_err(|e| ApiError::from_app(e, &sub.request_id))?;

    Ok(created(
        CreateOrgResponse {
            id: res.org_id,
            version: res.version,
        },
        sub.request_id,
    ))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AddNodeBody {
    pub parent_id: Uuid,
    pub kind: NodeKindDto,
    pub code: String,
    pub name: String,
    pub expected_version: i64,
}

#[derive(Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum NodeKindDto {
    Group,
    Subsidiary,
    Branch,
    Department,
}

impl From<NodeKindDto> for NodeKind {
    fn from(v: NodeKindDto) -> Self {
        match v {
            NodeKindDto::Group => NodeKind::Group,
            NodeKindDto::Subsidiary => NodeKind::Subsidiary,
            NodeKindDto::Branch => NodeKind::Branch,
            NodeKindDto::Department => NodeKind::Department,
        }
    }
}

#[derive(Serialize)]
pub struct AddNodeResponse {
    pub version: i64,
}

pub async fn add_node(
    State(app): State<AppContainer>,
    SubjectExt(sub): SubjectExt,
    Path(org_id): Path<Uuid>,
    Json(body): Json<AddNodeBody>,
) -> ApiResult<EnvelopeReply<AddNodeResponse>> {
    let res = handle_add_node(
        AddNodeCommand {
            org_id,
            parent_id: body.parent_id,
            kind: body.kind.into(),
            code: body.code,
            name: body.name,
            expected_version: body.expected_version,
        },
        &sub,
        &app.add_node_deps(),
    )
    .await
    .map_err(|e| ApiError::from_app(e, &sub.request_id))?;
    Ok(ok(
        AddNodeResponse {
            version: res.version,
        },
        sub.request_id,
    ))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MoveNodeBody {
    pub new_parent_id: Uuid,
    pub expected_version: i64,
}

#[derive(Serialize)]
pub struct EmptyData {}

pub async fn move_node(
    State(app): State<AppContainer>,
    SubjectExt(sub): SubjectExt,
    Path((org_id, node_id)): Path<(Uuid, Uuid)>,
    Json(body): Json<MoveNodeBody>,
) -> ApiResult<EnvelopeReply<EmptyData>> {
    handle_move_node(
        MoveNodeCommand {
            org_id,
            node_id,
            new_parent_id: body.new_parent_id,
            expected_version: body.expected_version,
        },
        &sub,
        &app.move_node_deps(),
    )
    .await
    .map_err(|e| ApiError::from_app(e, &sub.request_id))?;
    Ok(ok(EmptyData {}, sub.request_id))
}

#[derive(Deserialize)]
pub struct ListParams {
    #[serde(default)]
    pub offset: i64,
    #[serde(default = "default_limit")]
    pub limit: i64,
}

fn default_limit() -> i64 {
    50
}

#[derive(Serialize)]
pub struct OrgListRow {
    pub id: Uuid,
    pub code: String,
    pub name: String,
    pub root_path: String,
    pub node_count: i64,
    pub version: i64,
}

pub async fn list_organizations(
    State(app): State<AppContainer>,
    SubjectExt(sub): SubjectExt,
    Query(q): Query<ListParams>,
) -> ApiResult<EnvelopeReply<Vec<OrgListRow>>> {
    let items = handle_list_organizations(
        ListOrganizationsQuery {
            offset: q.offset,
            limit: q.limit.min(500),
        },
        &sub,
        &app.list_orgs_deps(),
    )
    .await
    .map_err(|e| ApiError::from_app(e, &sub.request_id))?;
    let rows = items
        .into_iter()
        .map(|i| OrgListRow {
            id: i.id,
            code: i.code,
            name: i.name,
            root_path: i.root_path,
            node_count: i.node_count,
            version: i.version,
        })
        .collect();
    Ok(ok(rows, sub.request_id))
}
