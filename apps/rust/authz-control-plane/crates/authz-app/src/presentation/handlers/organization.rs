//! Organization HTTP API.

use authz_http_middleware::metadata_from_extensions;
use axum::{
    extract::{Path, Query, Request, State},
    Json,
};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::application::commands::{
    handle_add_node, handle_create_organization, handle_move_node, AddNodeCommand,
    CreateOrganizationCommand, MoveNodeCommand,
};
use crate::application::errors::AppError;
use crate::application::queries::{
    handle_get_organization, handle_list_organizations, handle_resolve_branch_scope,
    GetOrganizationQuery, ListOrganizationsQuery, ResolveBranchScopeQuery,
};
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

#[derive(Serialize)]
pub struct OrgNodeResponse {
    pub id: Uuid,
    pub parent_id: Option<Uuid>,
    pub code: String,
    pub name: String,
    pub kind: String,
    pub path: String,
    pub is_active: bool,
}

#[derive(Serialize)]
pub struct OrganizationResponse {
    pub id: Uuid,
    pub root_node_id: Uuid,
    pub version: i64,
    pub nodes: Vec<OrgNodeResponse>,
}

#[derive(Serialize)]
pub struct BranchScopeResponse {
    pub tenant_id: Uuid,
    pub branch_id: Uuid,
    pub organization_id: Uuid,
    pub organization_version: i64,
    pub ancestry: Vec<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BranchDescendantsRequest {
    pub branch_ids: Vec<Uuid>,
}

#[derive(Serialize)]
pub struct BranchDescendantsResponse {
    pub branch_id: Uuid,
    pub organization_id: Uuid,
    pub organization_version: i64,
    pub descendant_branch_ids: Vec<Uuid>,
}

/// Returns the canonical, tenant-scoped root-to-branch path from the
/// organization owner. The endpoint never accepts caller-supplied ancestry.
pub async fn resolve_branch_scope(
    State(app): State<AppContainer>,
    SubjectExt(sub): SubjectExt,
    Path(branch_id): Path<Uuid>,
) -> ApiResult<EnvelopeReply<BranchScopeResponse>> {
    let scope = handle_resolve_branch_scope(
        ResolveBranchScopeQuery { branch_id },
        &sub,
        &app.resolve_branch_scope_deps(),
    )
    .await
    .map_err(|error| ApiError::from_app(error, &sub.request_id))?;
    Ok(ok(
        BranchScopeResponse {
            tenant_id: sub.tenant_id,
            branch_id,
            organization_id: scope.organization_id,
            organization_version: scope.organization_version,
            ancestry: scope.ancestry,
        },
        sub.request_id,
    ))
}

/// Service-to-service variant used by BPMP Engine. The command-bound signed
/// workload proof is the only source of tenant/workload identity.
pub async fn resolve_branch_scope_internal(
    State(app): State<AppContainer>,
    Path(branch_id): Path<Uuid>,
    request: Request,
) -> ApiResult<EnvelopeReply<BranchScopeResponse>> {
    let metadata = metadata_from_extensions(request.extensions());
    let proof_hex = request
        .headers()
        .get("x-bpmp-workload-proof")
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| {
            ApiError::from_app(
                AppError::Unauthorized {
                    reason: "missing workload proof".into(),
                },
                &metadata.request_id,
            )
        })?;
    let command_id = request
        .headers()
        .get("x-bpmp-command-id")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| {
            ApiError::from_app(
                AppError::Unauthorized {
                    reason: "missing command id".into(),
                },
                &metadata.request_id,
            )
        })?;
    let proof = decode_proof_hex(proof_hex).ok_or_else(|| {
        ApiError::from_app(
            AppError::Unauthorized {
                reason: "invalid workload proof encoding".into(),
            },
            &metadata.request_id,
        )
    })?;
    let verifier = app.branch_scope_workload_verifier.as_ref().ok_or_else(|| {
        ApiError::from_app(
            AppError::AuthzUnavailable("branch scope workload verifier is not configured".into()),
            &metadata.request_id,
        )
    })?;
    let now_epoch_ms = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| {
            ApiError::from_app(
                AppError::Internal("system clock is before Unix epoch".into()),
                &metadata.request_id,
            )
        })?
        .as_millis()
        .try_into()
        .map_err(|_| {
            ApiError::from_app(
                AppError::Internal("system clock exceeds supported range".into()),
                &metadata.request_id,
            )
        })?;
    let verified = verifier
        .verify(&proof, command_id, now_epoch_ms)
        .map_err(|_| {
            ApiError::from_app(
                AppError::Unauthorized {
                    reason: "workload proof is invalid or expired".into(),
                },
                &metadata.request_id,
            )
        })?;
    let mut uow = app
        .uow_factory
        .begin()
        .await
        .map_err(|error| ApiError::from_app(error, &metadata.request_id))?;
    let tenant_id = uuid::Uuid::parse_str(&verified.tenant_id).map_err(|_| {
        ApiError::from_app(
            AppError::Unauthorized {
                reason: "workload proof tenant is invalid".into(),
            },
            &metadata.request_id,
        )
    })?;
    let scope = uow
        .organizations()
        .resolve_branch_scope(tenant_id, branch_id)
        .await
        .map_err(|error| ApiError::from_app(error, &metadata.request_id))?;
    uow.commit()
        .await
        .map_err(|error| ApiError::from_app(error, &metadata.request_id))?;
    Ok(ok(
        BranchScopeResponse {
            tenant_id,
            branch_id,
            organization_id: scope.organization_id,
            organization_version: scope.organization_version,
            ancestry: scope.ancestry,
        },
        metadata.request_id,
    ))
}

/// Returns current branch leaves for a bounded set of branch roots. The
/// signed workload proof is command-bound and is the sole tenant authority.
pub async fn resolve_branch_descendants_internal(
    State(app): State<AppContainer>,
    request: Request,
) -> ApiResult<EnvelopeReply<Vec<BranchDescendantsResponse>>> {
    let metadata = metadata_from_extensions(request.extensions());
    let proof_hex = request
        .headers()
        .get("x-bpmp-workload-proof")
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| {
            ApiError::from_app(
                AppError::Unauthorized {
                    reason: "missing workload proof".into(),
                },
                &metadata.request_id,
            )
        })?;
    let command_id = request
        .headers()
        .get("x-bpmp-command-id")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| {
            ApiError::from_app(
                AppError::Unauthorized {
                    reason: "missing command id".into(),
                },
                &metadata.request_id,
            )
        })?;
    let proof = decode_proof_hex(proof_hex).ok_or_else(|| {
        ApiError::from_app(
            AppError::Unauthorized {
                reason: "invalid workload proof encoding".into(),
            },
            &metadata.request_id,
        )
    })?;
    let verifier = app.branch_scope_workload_verifier.as_ref().ok_or_else(|| {
        ApiError::from_app(
            AppError::AuthzUnavailable("branch scope workload verifier is not configured".into()),
            &metadata.request_id,
        )
    })?;
    let now_epoch_ms = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| {
            ApiError::from_app(
                AppError::Internal("system clock is before Unix epoch".into()),
                &metadata.request_id,
            )
        })?
        .as_millis()
        .try_into()
        .map_err(|_| {
            ApiError::from_app(
                AppError::Internal("system clock exceeds supported range".into()),
                &metadata.request_id,
            )
        })?;
    let verified = verifier
        .verify(&proof, command_id, now_epoch_ms)
        .map_err(|_| {
            ApiError::from_app(
                AppError::Unauthorized {
                    reason: "workload proof is invalid or expired".into(),
                },
                &metadata.request_id,
            )
        })?;
    let tenant_id = Uuid::parse_str(&verified.tenant_id).map_err(|_| {
        ApiError::from_app(
            AppError::Unauthorized {
                reason: "workload proof tenant is invalid".into(),
            },
            &metadata.request_id,
        )
    })?;
    let body = axum::body::to_bytes(request.into_body(), 64 * 1024)
        .await
        .map_err(|_| {
            ApiError::from_app(
                AppError::BadRequest("invalid branch descendant request body".into()),
                &metadata.request_id,
            )
        })?;
    let input: BranchDescendantsRequest = serde_json::from_slice(&body).map_err(|_| {
        ApiError::from_app(
            AppError::BadRequest("invalid branch descendant request body".into()),
            &metadata.request_id,
        )
    })?;
    if input.branch_ids.is_empty() || input.branch_ids.len() > 100 {
        return Err(ApiError::from_app(
            AppError::BadRequest("branch_ids must contain 1..=100 entries".into()),
            &metadata.request_id,
        ));
    }
    let mut unique = std::collections::HashSet::new();
    if input.branch_ids.iter().any(|id| !unique.insert(*id)) {
        return Err(ApiError::from_app(
            AppError::BadRequest("branch_ids must be unique".into()),
            &metadata.request_id,
        ));
    }
    let mut uow = app
        .uow_factory
        .begin()
        .await
        .map_err(|error| ApiError::from_app(error, &metadata.request_id))?;
    let scopes = uow
        .organizations()
        .resolve_branch_descendants(tenant_id, &input.branch_ids, 10_000)
        .await
        .map_err(|error| ApiError::from_app(error, &metadata.request_id))?;
    uow.commit()
        .await
        .map_err(|error| ApiError::from_app(error, &metadata.request_id))?;
    Ok(ok(
        scopes
            .into_iter()
            .map(|scope| BranchDescendantsResponse {
                branch_id: scope.branch_id,
                organization_id: scope.organization_id,
                organization_version: scope.organization_version,
                descendant_branch_ids: scope.descendant_branch_ids,
            })
            .collect(),
        metadata.request_id,
    ))
}

fn decode_proof_hex(value: &str) -> Option<Vec<u8>> {
    if value.is_empty() || value.len() % 2 != 0 || value.len() > 64 * 1024 {
        return None;
    }
    value
        .as_bytes()
        .chunks_exact(2)
        .map(|pair| {
            let high = hex_nibble(pair[0])?;
            let low = hex_nibble(pair[1])?;
            Some((high << 4) | low)
        })
        .collect()
}

fn hex_nibble(value: u8) -> Option<u8> {
    match value {
        b'0'..=b'9' => Some(value - b'0'),
        b'a'..=b'f' => Some(value - b'a' + 10),
        b'A'..=b'F' => Some(value - b'A' + 10),
        _ => None,
    }
}

pub async fn get_organization(
    State(app): State<AppContainer>,
    SubjectExt(sub): SubjectExt,
    Path(org_id): Path<Uuid>,
) -> ApiResult<EnvelopeReply<OrganizationResponse>> {
    let organization =
        handle_get_organization(GetOrganizationQuery { org_id }, &sub, &app.get_org_deps())
            .await
            .map_err(|e| ApiError::from_app(e, &sub.request_id))?;

    let mut nodes = organization
        .nodes()
        .map(|node| OrgNodeResponse {
            id: node.id.0,
            parent_id: node.parent_id.map(|parent| parent.0),
            code: node.code.clone(),
            name: node.name.clone(),
            kind: node.kind.as_str().to_owned(),
            path: node.path.as_str().to_owned(),
            is_active: node.is_active,
        })
        .collect::<Vec<_>>();
    nodes.sort_unstable_by(|left, right| left.path.cmp(&right.path));

    Ok(ok(
        OrganizationResponse {
            id: organization.id().0,
            root_node_id: organization.root_id().0,
            version: organization.version(),
            nodes,
        },
        sub.request_id,
    ))
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
