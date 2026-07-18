use authz_core::{
    ids::{PermissionId, RoleId, TenantId, UserId},
    AuthzError,
};
use sqlx::PgPool;

/// Inserts a new role for a tenant.
///
/// `code` must be unique within the tenant (enforced by DB constraint).
pub async fn insert_role(
    pool: &PgPool,
    role_id: RoleId,
    tenant_id: TenantId,
    code: &str,
    name: &str,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO role (id, tenant_id, code, name)
        VALUES ($1, $2, $3, $4)
        "#,
    )
    .bind(role_id.into_uuid())
    .bind(tenant_id.into_uuid())
    .bind(code)
    .bind(name)
    .execute(pool)
    .await?;
    Ok(())
}

/// Inserts a new permission definition for a tenant.
///
/// `code` must be unique within the tenant. `scope` is one of `own | branch | all`.
/// Use [`assign_role_to_permission`] to link the permission to a role.
pub async fn insert_permission(
    pool: &PgPool,
    permission_id: PermissionId,
    tenant_id: TenantId,
    code: &str,
    resource_type: &str,
    action: &str,
    scope: &str,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO permission (id, tenant_id, code, resource_type, action, scope)
        VALUES ($1, $2, $3, $4, $5, $6)
        "#,
    )
    .bind(permission_id.into_uuid())
    .bind(tenant_id.into_uuid())
    .bind(code)
    .bind(resource_type)
    .bind(action)
    .bind(scope)
    .execute(pool)
    .await?;
    Ok(())
}

/// Links a permission to a role via the `role_permission` junction table.
///
/// `conditions` is an optional JSON ABAC overlay applied on top of the base permission.
pub async fn assign_role_to_permission(
    pool: &PgPool,
    role_id: RoleId,
    permission_id: PermissionId,
    conditions: Option<serde_json::Value>,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO role_permission (role_id, permission_id, conditions)
        VALUES ($1, $2, $3)
        "#,
    )
    .bind(role_id.into_uuid())
    .bind(permission_id.into_uuid())
    .bind(conditions)
    .execute(pool)
    .await?;
    Ok(())
}

/// Assigns a role to a user within a tenant.
pub async fn assign_role_to_user(
    pool: &PgPool,
    tenant_id: TenantId,
    user_id: UserId,
    role_id: RoleId,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO user_role (user_id, role_id, tenant_id)
        VALUES ($1, $2, $3)
        "#,
    )
    .bind(user_id.into_uuid())
    .bind(role_id.into_uuid())
    .bind(tenant_id.into_uuid())
    .execute(pool)
    .await?;
    Ok(())
}
