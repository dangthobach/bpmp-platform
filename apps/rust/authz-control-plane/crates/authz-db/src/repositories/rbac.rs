//! RBAC repository — P1 optimized combined query.
//!
//! The key query here eliminates N+1 by fetching role hierarchy + permissions +
//! row filters + field filters + policy conditions in a single JOIN.

use authz_core::{
    ids::{TenantId, UserId},
    models::rbac::{
        EffectivePermissions, FieldFilterConfig, Permission, PermissionScope, PolicyEffect,
        ResolvedPermission,
    },
    AuthzError,
};
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use uuid::Uuid;

use super::metadata::MetadataRow;

#[derive(sqlx::FromRow)]
struct RbacRow {
    permission_id: Uuid,
    permission_code: String,
    resource_type: String,
    action: String,
    scope: String,
    role_id: Uuid,
    role_code: String,
    extra_conditions: Option<JsonValue>,
    row_filter_expr: Option<JsonValue>,
    allowed_fields: Option<Vec<String>>,
    masked_fields: Option<Vec<String>>,
    mask_pattern: Option<String>,
    policy_condition: Option<JsonValue>,
    policy_effect: Option<String>,
    policy_priority: Option<i32>,
    #[sqlx(flatten)]
    permission_metadata: MetadataRow,
}

/// Fetches a user's effective permissions for a given resource type.
#[tracing::instrument(skip(pool), fields(
    user_id = %user_id,
    tenant_id = %tenant_id,
    resource_type = %resource_type
))]
pub async fn fetch_effective_permissions(
    pool: &PgPool,
    user_id: UserId,
    tenant_id: TenantId,
    resource_type: &str,
) -> Result<EffectivePermissions, AuthzError> {
    let rows: Vec<RbacRow> = sqlx::query_as(
        r#"
        SELECT
            p.id              AS permission_id,
            p.code            AS permission_code,
            p.resource_type,
            p.action,
            p.scope,
            r.id              AS role_id,
            r.code            AS role_code,
            rp.conditions     AS extra_conditions,
            rf.filter_expr    AS row_filter_expr,
            ff.allowed_fields,
            ff.masked_fields,
            ff.mask_pattern,
            pr.condition_expr AS policy_condition,
            pol.effect        AS policy_effect,
            pol.priority      AS policy_priority,
            p.version,
            p.is_deleted,
            p.deleted_at,
            p.deleted_by,
            p.created_at,
            p.created_by,
            p.updated_at,
            p.updated_by
        FROM user_role ur

        JOIN LATERAL (
            WITH RECURSIVE role_tree AS (
                SELECT id, parent_role_id
                FROM   role
                WHERE  id = ur.role_id
                  AND  is_deleted = false
                UNION ALL
                SELECT r2.id, r2.parent_role_id
                FROM   role r2
                JOIN   role_tree rt ON r2.id = rt.parent_role_id
                WHERE  r2.is_deleted = false
            )
            SELECT id FROM role_tree
        ) r_hier ON true

        JOIN role r ON r.id = r_hier.id AND r.tenant_id = $2 AND r.is_deleted = false
        JOIN role_permission rp ON rp.role_id = r.id AND rp.is_deleted = false
        JOIN permission p ON p.id = rp.permission_id
            AND p.resource_type = $3
            AND p.tenant_id     = $2
            AND p.is_deleted    = false

        LEFT JOIN row_filter rf ON rf.permission_id = p.id
            AND rf.resource_type = $3
            AND rf.is_active = true
            AND rf.is_deleted = false

        LEFT JOIN field_filter ff ON ff.permission_id = p.id
            AND ff.resource_type = $3
            AND ff.is_deleted = false

        LEFT JOIN policy_rule pr ON pr.resource_type = $3
            AND pr.action = p.action
            AND pr.is_deleted = false

        LEFT JOIN policy pol ON pol.id = pr.policy_id
            AND pol.is_active = true
            AND pol.tenant_id = $2
            AND pol.is_deleted = false

        WHERE ur.user_id   = $1
          AND ur.tenant_id = $2
          AND ur.is_deleted = false
          AND (ur.expires_at IS NULL OR ur.expires_at > NOW())

        ORDER BY pol.priority DESC
        "#,
    )
    .bind(user_id.into_uuid())
    .bind(tenant_id.into_uuid())
    .bind(resource_type)
    .fetch_all(pool)
    .await?;

    let permissions = rows
        .into_iter()
        .map(|row| {
            let scope = match row.scope.as_str() {
                "branch" => PermissionScope::Branch,
                "all" => PermissionScope::All,
                _ => PermissionScope::Own,
            };

            let effect = match row.policy_effect.as_deref() {
                Some("DENY") => PolicyEffect::Deny,
                _ => PolicyEffect::Allow,
            };

            let permission = Permission {
                id: authz_core::ids::PermissionId::from_uuid(row.permission_id),
                tenant_id,
                code: row.permission_code,
                resource_type: row.resource_type,
                action: row.action,
                scope,
                metadata: row.permission_metadata.into(),
            };

            let field_filter = match (row.allowed_fields, row.masked_fields) {
                (Some(allowed), Some(masked)) => Some(FieldFilterConfig {
                    allowed_fields: allowed,
                    masked_fields: masked,
                    mask_pattern: row.mask_pattern,
                }),
                _ => None,
            };

            ResolvedPermission {
                permission,
                role_id: authz_core::ids::RoleId::from_uuid(row.role_id),
                role_code: row.role_code,
                extra_conditions: row.extra_conditions,
                row_filter_exprs: row.row_filter_expr.map(|v| vec![v]).unwrap_or_default(),
                field_filter,
                policy_condition: row.policy_condition,
                policy_effect: effect,
                policy_priority: row.policy_priority.unwrap_or(0),
            }
        })
        .collect();

    Ok(EffectivePermissions {
        user_id,
        tenant_id,
        resource_type: resource_type.to_owned(),
        permissions,
    })
}
