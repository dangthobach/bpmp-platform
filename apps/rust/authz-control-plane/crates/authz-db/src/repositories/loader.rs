//! Loader queries used by `authz-engine` warm-up paths.
//!
//! Each function is a single SQL statement returning rows in the exact shape
//! required by the in-memory fast-path structures (bitmap, cuckoo, temporal
//! bundle, resource-type cache).

use authz_core::{
    ids::{PermissionId, ResourceTypeId, TenantId, UserId},
    models::{
        filter::TemporalPolicy,
        resource::{ResourceSchemaDef, ResourceType},
    },
    AuthzError,
};
use chrono::NaiveTime;
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use uuid::Uuid;

use super::metadata::MetadataRow;

/// One row of the global permission grant set (warm-up of bitmap / cuckoo).
#[derive(Debug, Clone)]
pub struct UserPermissionGrant {
    pub tenant_id: TenantId,
    pub user_id: UserId,
    pub permission_code: String,
}

#[derive(sqlx::FromRow)]
struct GrantRow {
    tenant_id: Uuid,
    user_id: Uuid,
    permission_code: String,
}

/// Streams every active `(tenant, user, resource_type:action)` grant.
///
/// Joins `user_role → role_permission → permission` and filters out expired
/// role assignments. Result is bounded by `max_rows` to keep memory predictable.
#[tracing::instrument(skip(pool))]
pub async fn list_active_user_grants(
    pool: &PgPool,
    max_rows: i64,
) -> Result<Vec<UserPermissionGrant>, AuthzError> {
    let rows: Vec<GrantRow> = sqlx::query_as(
        r#"
        SELECT DISTINCT
            p.tenant_id              AS tenant_id,
            ur.user_id               AS user_id,
            (p.resource_type || ':' || p.action) AS permission_code
        FROM   user_role ur
        JOIN   role_permission rp ON rp.role_id = ur.role_id
        JOIN   permission p       ON p.id       = rp.permission_id
        WHERE  (ur.expires_at IS NULL OR ur.expires_at > NOW())
          AND  ur.is_deleted = false
          AND  rp.is_deleted = false
          AND  p.is_deleted = false
        LIMIT  $1
        "#,
    )
    .bind(max_rows)
    .fetch_all(pool)
    .await?;

    Ok(rows
        .into_iter()
        .map(|r| UserPermissionGrant {
            tenant_id: TenantId::from_uuid(r.tenant_id),
            user_id: UserId::from_uuid(r.user_id),
            permission_code: r.permission_code,
        })
        .collect())
}

#[derive(sqlx::FromRow)]
struct ResourceTypeRow {
    id: Uuid,
    tenant_id: Uuid,
    code: String,
    name: String,
    schema_def: JsonValue,
    #[sqlx(flatten)]
    metadata: MetadataRow,
}

/// Loads a single `ResourceType` by `(tenant_id, code)`.
#[tracing::instrument(skip(pool))]
pub async fn find_resource_type_by_code(
    pool: &PgPool,
    tenant_id: TenantId,
    code: &str,
) -> Result<Option<ResourceType>, AuthzError> {
    let row: Option<ResourceTypeRow> = sqlx::query_as(
        r#"
        SELECT id, tenant_id, code, name, schema_def,
               version, is_deleted, deleted_at, deleted_by,
               created_at, created_by, updated_at, updated_by
        FROM   resource_type
        WHERE  tenant_id = $1 AND code = $2 AND is_deleted = false
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(code)
    .fetch_optional(pool)
    .await?;

    row.map(|r| {
        let schema_def: ResourceSchemaDef =
            serde_json::from_value(r.schema_def).map_err(AuthzError::Serialization)?;
        Ok(ResourceType {
            id: ResourceTypeId::from_uuid(r.id),
            tenant_id: TenantId::from_uuid(r.tenant_id),
            code: r.code,
            name: r.name,
            schema_def,
            metadata: r.metadata.into(),
        })
    })
    .transpose()
}

#[derive(sqlx::FromRow)]
struct TemporalRow {
    id: Uuid,
    permission_id: Uuid,
    tenant_id: Uuid,
    name: String,
    allowed_days: Vec<i16>,
    allowed_from: NaiveTime,
    allowed_until: NaiveTime,
    timezone: String,
    allowed_cidr: Option<Vec<String>>,
    require_shift: bool,
    shift_table_ref: Option<String>,
    is_active: bool,
    #[sqlx(flatten)]
    metadata: MetadataRow,
}

/// Loads every active temporal policy joined with its owning tenant.
///
/// Returned tuples are `(tenant_id, TemporalPolicy)` so the loader can group
/// them per-tenant for the policy-bundle warm-up.
#[tracing::instrument(skip(pool))]
pub async fn list_active_temporal_policies(
    pool: &PgPool,
) -> Result<Vec<(TenantId, TemporalPolicy)>, AuthzError> {
    let rows: Vec<TemporalRow> = sqlx::query_as(
        r#"
        SELECT tp.id, tp.permission_id, p.tenant_id, tp.name,
               tp.allowed_days, tp.allowed_from, tp.allowed_until,
               tp.timezone,
               tp.allowed_cidr::text[] AS allowed_cidr,
               tp.require_shift, tp.shift_table_ref, tp.is_active,
               tp.version, tp.is_deleted, tp.deleted_at, tp.deleted_by,
               tp.created_at, tp.created_by, tp.updated_at, tp.updated_by
        FROM   temporal_policy tp
        JOIN   permission p ON p.id = tp.permission_id
        WHERE  tp.is_active = true
          AND  tp.is_deleted = false
          AND  p.is_deleted = false
        "#,
    )
    .fetch_all(pool)
    .await?;

    Ok(rows
        .into_iter()
        .map(|r| {
            let tenant = TenantId::from_uuid(r.tenant_id);
            let policy = TemporalPolicy {
                id: authz_core::ids::TemporalPolicyId::from_uuid(r.id),
                permission_id: PermissionId::from_uuid(r.permission_id),
                name: r.name,
                allowed_days: r.allowed_days.into_iter().map(|d| d as u8).collect(),
                allowed_from: r.allowed_from,
                allowed_until: r.allowed_until,
                timezone: r.timezone,
                allowed_cidr: r.allowed_cidr,
                require_shift: r.require_shift,
                shift_table_ref: r.shift_table_ref,
                is_active: r.is_active,
                metadata: r.metadata.into(),
            };
            (tenant, policy)
        })
        .collect())
}
