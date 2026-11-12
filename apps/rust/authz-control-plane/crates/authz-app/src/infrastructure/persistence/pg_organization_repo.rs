//! Postgres adapter for `OrganizationRepository`, scoped to a transaction.
//!
//! Storage strategy: **adjacency list** (`parent_id`) + **materialized path**
//! (`ltree`) so:
//! * subtree reads use `path <@ root` (GIST index, O(log N + k))
//! * cycle detection is structural at the domain layer + checked again in SQL
//! * move-subtree updates run in a single statement bounded by subtree size.

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use sqlx::{Postgres, Transaction};
use uuid::Uuid;

use crate::application::errors::AppError;
use crate::application::ports::organization_repo::{
    BranchDescendants, BranchScope, OrganizationListItem, OrganizationRepository,
};
use crate::domain::organization::{
    MaterializedPath, NodeId, NodeKind, OrgId, OrgNode, Organization,
};

/// Repository methods executed against a borrowed transaction.
/// The owning [`super::PgUnitOfWork`] is responsible for commit/rollback.
pub(crate) async fn load_aggregate(
    tx: &mut Transaction<'static, Postgres>,
    tenant_id: Uuid,
    id: OrgId,
) -> Result<Organization, AppError> {
    let row = sqlx::query_as::<_, (Uuid, i64)>(
        "SELECT root_node_id, version FROM org_aggregate \
         WHERE id = $1 AND tenant_id = $2",
    )
    .bind(id.0)
    .bind(tenant_id)
    .fetch_optional(&mut **tx)
    .await?;

    let (root_id, version) = row.ok_or_else(|| AppError::NotFound {
        resource: format!("organization:{}", id.0),
    })?;

    let nodes: Vec<OrgNode> = sqlx::query_as::<
        _,
        (
            Uuid,
            Option<Uuid>,
            String,
            String,
            String,
            String,
            bool,
            DateTime<Utc>,
            DateTime<Utc>,
        ),
    >(
        "SELECT id, parent_id, code, name, kind, path::text, is_active, created_at, updated_at \
         FROM org_node WHERE org_id = $1 AND tenant_id = $2",
    )
    .bind(id.0)
    .bind(tenant_id)
    .fetch_all(&mut **tx)
    .await?
    .into_iter()
    .map(|(id, parent, code, name, kind, path, active, c, u)| {
        Ok::<_, AppError>(OrgNode {
            id: NodeId(id),
            parent_id: parent.map(NodeId),
            code,
            name,
            kind: parse_kind(&kind)?,
            path: MaterializedPath::new(path).map_err(AppError::Domain)?,
            is_active: active,
            created_at: c,
            updated_at: u,
        })
    })
    .collect::<Result<_, _>>()?;

    Ok(Organization::hydrate(
        id,
        tenant_id,
        NodeId(root_id),
        nodes,
        version,
    ))
}

pub(crate) async fn save_aggregate(
    tx: &mut Transaction<'static, Postgres>,
    org: &Organization,
    expected_version: i64,
) -> Result<(), AppError> {
    // Upsert aggregate row with optimistic lock CAS.
    let updated = sqlx::query(
        "INSERT INTO org_aggregate (id, tenant_id, root_node_id, version) \
         VALUES ($1, $2, $3, $4) \
         ON CONFLICT (id) DO UPDATE \
            SET version = org_aggregate.version + 1, updated_at = now() \
            WHERE org_aggregate.version = $4 \
         RETURNING id",
    )
    .bind(org.id().0)
    .bind(org.tenant_id())
    .bind(
        org.nodes()
            .find(|n| n.parent_id.is_none())
            .map(|n| n.id.0)
            .ok_or_else(|| AppError::Internal("aggregate has no root".into()))?,
    )
    .bind(expected_version)
    .fetch_optional(&mut **tx)
    .await?;

    // CAS path: INSERT returns the new id; UPDATE returns the id only if
    // `version = $expected` matched. An empty result is always a conflict —
    // either the row exists with a newer version, or the expected version
    // does not correspond to current state.
    if updated.is_none() {
        return Err(AppError::Conflict("optimistic lock failed".into()));
    }

    // Upsert all nodes (small N: a single organization tree).
    for n in org.nodes() {
        sqlx::query(
            "INSERT INTO org_node \
              (id, org_id, tenant_id, parent_id, code, name, kind, path, is_active, created_at, updated_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8::ltree,$9,$10,$11) \
             ON CONFLICT (id) DO UPDATE SET \
                parent_id = EXCLUDED.parent_id, \
                code = EXCLUDED.code, \
                name = EXCLUDED.name, \
                kind = EXCLUDED.kind, \
                path = EXCLUDED.path, \
                is_active = EXCLUDED.is_active, \
                updated_at = EXCLUDED.updated_at",
        )
        .bind(n.id.0)
        .bind(org.id().0)
        .bind(org.tenant_id())
        .bind(n.parent_id.map(|p| p.0))
        .bind(&n.code)
        .bind(&n.name)
        .bind(n.kind.as_str())
        .bind(n.path.as_str())
        .bind(n.is_active)
        .bind(n.created_at)
        .bind(n.updated_at)
        .execute(&mut **tx)
        .await?;
    }
    Ok(())
}

pub(crate) async fn list_organizations(
    tx: &mut Transaction<'static, Postgres>,
    tenant_id: Uuid,
    offset: i64,
    limit: i64,
) -> Result<Vec<OrganizationListItem>, AppError> {
    let rows = sqlx::query_as::<_, (Uuid, String, String, String, i64, i64)>(
        "SELECT a.id, n.code, n.name, n.path::text, \
                (SELECT count(*) FROM org_node x WHERE x.org_id = a.id), \
                a.version \
         FROM org_aggregate a \
         JOIN org_node n ON n.id = a.root_node_id \
         WHERE a.tenant_id = $1 \
         ORDER BY n.code \
         OFFSET $2 LIMIT $3",
    )
    .bind(tenant_id)
    .bind(offset)
    .bind(limit)
    .fetch_all(&mut **tx)
    .await?;
    Ok(rows
        .into_iter()
        .map(
            |(id, code, name, path, count, version)| OrganizationListItem {
                id,
                code,
                name,
                root_path: path,
                node_count: count,
                version,
            },
        )
        .collect())
}

pub(crate) async fn resolve_branch_scope(
    tx: &mut Transaction<'static, Postgres>,
    tenant_id: Uuid,
    branch_id: Uuid,
) -> Result<BranchScope, AppError> {
    let row = sqlx::query_as::<_, (Uuid, i64, Vec<Uuid>, bool, bool)>(
        "WITH RECURSIVE target AS ( \
             SELECT id, org_id FROM org_node \
             WHERE id = $2 AND tenant_id = $1 AND kind = 'BRANCH' AND is_active \
         ), ancestry AS ( \
             SELECT n.id, n.parent_id, n.org_id, n.is_active, 0::INT AS depth \
             FROM org_node n JOIN target t ON t.id = n.id AND t.org_id = n.org_id \
             UNION ALL \
             SELECT p.id, p.parent_id, p.org_id, p.is_active, a.depth + 1 \
             FROM org_node p JOIN ancestry a ON p.id = a.parent_id AND p.org_id = a.org_id \
             WHERE p.tenant_id = $1 AND a.depth < 128 \
         ) \
         SELECT t.org_id, aggregate.version, \
                array_agg(a.id ORDER BY a.depth DESC), \
                bool_and(a.is_active), bool_or(a.parent_id IS NULL) \
         FROM target t \
         JOIN org_aggregate aggregate ON aggregate.id = t.org_id AND aggregate.tenant_id = $1 \
         JOIN ancestry a ON a.org_id = t.org_id \
         GROUP BY t.org_id, aggregate.version",
    )
    .bind(tenant_id)
    .bind(branch_id)
    .fetch_optional(&mut **tx)
    .await?
    .ok_or_else(|| AppError::NotFound {
        resource: format!("active branch:{branch_id}"),
    })?;

    let (organization_id, organization_version, ancestry, all_active, reaches_root) = row;
    if !all_active {
        return Err(AppError::NotFound {
            resource: format!("active branch:{branch_id}"),
        });
    }
    if !reaches_root || ancestry.last() != Some(&branch_id) {
        return Err(AppError::Internal(
            "organization branch ancestry is incomplete or malformed".into(),
        ));
    }
    Ok(BranchScope {
        organization_id,
        organization_version,
        ancestry,
    })
}

pub(crate) async fn resolve_branch_descendants(
    tx: &mut Transaction<'static, Postgres>,
    tenant_id: Uuid,
    branch_ids: &[Uuid],
    max_branches: usize,
) -> Result<Vec<BranchDescendants>, AppError> {
    if branch_ids.is_empty() || branch_ids.len() > 100 || max_branches == 0 {
        return Err(AppError::BadRequest(
            "branch descendant batch is outside limits".into(),
        ));
    }
    let rows = sqlx::query_as::<_, (Uuid, Uuid, i64, Vec<Uuid>, bool)>(
        "WITH RECURSIVE roots AS ( \
             SELECT n.id AS branch_id, n.org_id \
             FROM org_node n \
             WHERE n.id = ANY($2) AND n.tenant_id = $1 AND n.kind = 'BRANCH' AND n.is_active \
         ), tree AS ( \
             SELECT r.branch_id, n.id AS node_id, n.parent_id, n.org_id, n.kind, n.is_active, \
                    ARRAY[n.id] AS visited, 0::INT AS depth \
             FROM roots r JOIN org_node n ON n.id = r.branch_id AND n.org_id = r.org_id \
             UNION ALL \
             SELECT t.branch_id, c.id, c.parent_id, c.org_id, c.kind, c.is_active, \
                    t.visited || c.id, t.depth + 1 \
             FROM tree t JOIN org_node c ON c.parent_id = t.node_id AND c.org_id = t.org_id \
             WHERE c.tenant_id = $1 AND c.is_active AND t.depth < 128 AND NOT c.id = ANY(t.visited) \
         ) \
         SELECT r.branch_id, r.org_id, a.version, \
                array_agg(t.node_id ORDER BY t.node_id) FILTER (WHERE t.kind = 'BRANCH'), \
                bool_or(t.depth = 128 AND EXISTS ( \
                    SELECT 1 FROM org_node child \
                    WHERE child.parent_id = t.node_id AND child.org_id = t.org_id \
                      AND child.tenant_id = $1 AND child.is_active \
                )) \
         FROM roots r \
         JOIN org_aggregate a ON a.id = r.org_id AND a.tenant_id = $1 \
         JOIN tree t ON t.branch_id = r.branch_id \
         GROUP BY r.branch_id, r.org_id, a.version \
         ORDER BY r.branch_id",
    )
    .bind(tenant_id)
    .bind(branch_ids)
    .fetch_all(&mut **tx)
    .await?;
    if rows.len() != branch_ids.len() {
        return Err(AppError::NotFound {
            resource: "one or more active branches".into(),
        });
    }
    if rows.iter().any(|row| row.4) {
        return Err(AppError::Internal(
            "organization branch depth exceeds resolver limit".into(),
        ));
    }
    let total: usize = rows.iter().map(|row| row.3.len()).sum();
    if total > max_branches {
        return Err(AppError::BadRequest(
            "branch descendant result exceeds configured limit".into(),
        ));
    }
    Ok(rows
        .into_iter()
        .map(
            |(branch_id, organization_id, organization_version, descendant_branch_ids, _)| {
                BranchDescendants {
                    branch_id,
                    organization_id,
                    organization_version,
                    descendant_branch_ids,
                }
            },
        )
        .collect())
}

fn parse_kind(s: &str) -> Result<NodeKind, AppError> {
    Ok(match s {
        "GROUP" => NodeKind::Group,
        "SUBSIDIARY" => NodeKind::Subsidiary,
        "BRANCH" => NodeKind::Branch,
        "DEPARTMENT" => NodeKind::Department,
        other => return Err(AppError::Internal(format!("unknown node kind: {other}"))),
    })
}

/// Trait-implementing wrapper kept for API symmetry with the port.
/// Concrete UoW delegates here.
pub struct PgOrgRepoView;

#[async_trait]
impl OrganizationRepository for PgOrgRepoView {
    async fn load(&mut self, _tenant: Uuid, _id: OrgId) -> Result<Organization, AppError> {
        Err(AppError::Internal("call via UnitOfWork".into()))
    }
    async fn save(&mut self, _org: &Organization, _expected: i64) -> Result<(), AppError> {
        Err(AppError::Internal("call via UnitOfWork".into()))
    }
    async fn list(
        &mut self,
        _tenant: Uuid,
        _offset: i64,
        _limit: i64,
    ) -> Result<Vec<OrganizationListItem>, AppError> {
        Err(AppError::Internal("call via UnitOfWork".into()))
    }

    async fn resolve_branch_scope(
        &mut self,
        _tenant: Uuid,
        _branch_id: Uuid,
    ) -> Result<BranchScope, AppError> {
        Err(AppError::Internal("call via UnitOfWork".into()))
    }

    async fn resolve_branch_descendants(
        &mut self,
        _tenant: Uuid,
        _branch_ids: &[Uuid],
        _max_branches: usize,
    ) -> Result<Vec<BranchDescendants>, AppError> {
        Err(AppError::Internal("call via UnitOfWork".into()))
    }
}
