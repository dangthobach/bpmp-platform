//! Tenant and user account repositories.
//!
//! All queries use parameterized inputs via sqlx — no string interpolation.
//! Implements G2 optimistic locking for attribute version updates.

use authz_core::{
    ids::{TenantId, UserId},
    models::tenant::{Tenant, TenantConfig, UserAccount},
    AuthzError,
};
use chrono::Utc;
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use uuid::Uuid;

use super::metadata::MetadataRow;

// ─── Internal Row Types ──────────────────────────────────────────────────────

#[derive(sqlx::FromRow)]
struct TenantRow {
    id: Uuid,
    code: String,
    name: String,
    is_active: bool,
    config: Option<JsonValue>,
    #[sqlx(flatten)]
    metadata: MetadataRow,
}

#[derive(sqlx::FromRow)]
struct UserRow {
    id: Uuid,
    tenant_id: Uuid,
    username: String,
    external_id: Option<String>,
    attributes: Option<JsonValue>,
    attributes_version: i64,
    is_active: bool,
    #[sqlx(flatten)]
    metadata: MetadataRow,
}

// ─── Tenant Repository ────────────────────────────────────────────────────────

/// Retrieves a tenant by its unique code.
#[tracing::instrument(skip(pool), fields(tenant_code = %code))]
pub async fn find_tenant_by_code(pool: &PgPool, code: &str) -> Result<Tenant, AuthzError> {
    let row: Option<TenantRow> = sqlx::query_as(
        r#"
        SELECT id, code, name, is_active, config,
               version, is_deleted, deleted_at, deleted_by,
               created_at, created_by, updated_at, updated_by
        FROM   tenant
        WHERE  code = $1 AND is_deleted = false
        "#,
    )
    .bind(code)
    .fetch_optional(pool)
    .await?;

    let row = row.ok_or_else(|| AuthzError::TenantNotFound {
        tenant_id: Uuid::nil(),
    })?;
    if !row.is_active {
        return Err(AuthzError::TenantInactive { tenant_id: row.id });
    }

    let config: TenantConfig =
        serde_json::from_value(row.config.unwrap_or_default()).unwrap_or_default();

    Ok(Tenant {
        id: TenantId::from_uuid(row.id),
        code: row.code,
        name: row.name,
        config,
        metadata: row.metadata.into(),
    })
}

/// Retrieves a tenant by its UUID.
#[tracing::instrument(skip(pool), fields(tenant_id = %tenant_id))]
pub async fn find_tenant_by_id(pool: &PgPool, tenant_id: TenantId) -> Result<Tenant, AuthzError> {
    let row: Option<TenantRow> = sqlx::query_as(
        r#"
        SELECT id, code, name, is_active, config,
               version, is_deleted, deleted_at, deleted_by,
               created_at, created_by, updated_at, updated_by
        FROM   tenant
        WHERE  id = $1 AND is_deleted = false
        "#,
    )
    .bind(tenant_id.into_uuid())
    .fetch_optional(pool)
    .await?;

    let row = row.ok_or(AuthzError::TenantNotFound {
        tenant_id: tenant_id.into_uuid(),
    })?;
    if !row.is_active {
        return Err(AuthzError::TenantInactive { tenant_id: row.id });
    }

    let config: TenantConfig =
        serde_json::from_value(row.config.unwrap_or_default()).unwrap_or_default();

    Ok(Tenant {
        id: TenantId::from_uuid(row.id),
        code: row.code,
        name: row.name,
        config,
        metadata: row.metadata.into(),
    })
}

// ─── User Account Repository ─────────────────────────────────────────────────

/// Retrieves a user account by external IdP subject ID.
#[tracing::instrument(skip(pool), fields(external_id = %external_id, tenant_id = %tenant_id))]
pub async fn find_user_by_external_id(
    pool: &PgPool,
    tenant_id: TenantId,
    external_id: &str,
) -> Result<UserAccount, AuthzError> {
    let row: Option<UserRow> = sqlx::query_as(
        r#"
        SELECT id, tenant_id, username, external_id, attributes,
               attributes_version, is_active,
               version, is_deleted, deleted_at, deleted_by,
               created_at, created_by, updated_at, updated_by
        FROM   user_account
        WHERE  tenant_id   = $1
          AND  external_id = $2
          AND  is_deleted  = false
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(external_id)
    .fetch_optional(pool)
    .await?;

    let row = row.ok_or(AuthzError::UserNotFound {
        user_id: Uuid::nil(),
        tenant_id: tenant_id.into_uuid(),
    })?;

    if !row.is_active {
        return Err(AuthzError::UserDeactivated { user_id: row.id });
    }

    Ok(UserAccount {
        id: UserId::from_uuid(row.id),
        tenant_id: TenantId::from_uuid(row.tenant_id),
        username: row.username,
        external_id: row.external_id,
        attributes: row
            .attributes
            .unwrap_or(JsonValue::Object(Default::default())),
        attributes_version: row.attributes_version,
        is_active: row.is_active,
        metadata: row.metadata.into(),
    })
}

/// Retrieves a user by their internal UUID.
#[tracing::instrument(skip(pool), fields(user_id = %user_id, tenant_id = %tenant_id))]
pub async fn find_user_by_id(
    pool: &PgPool,
    tenant_id: TenantId,
    user_id: UserId,
) -> Result<UserAccount, AuthzError> {
    let row: Option<UserRow> = sqlx::query_as(
        r#"
        SELECT id, tenant_id, username, external_id, attributes,
               attributes_version, is_active,
               version, is_deleted, deleted_at, deleted_by,
               created_at, created_by, updated_at, updated_by
        FROM   user_account
        WHERE  id        = $1
          AND  tenant_id = $2
          AND  is_deleted = false
        "#,
    )
    .bind(user_id.into_uuid())
    .bind(tenant_id.into_uuid())
    .fetch_optional(pool)
    .await?;

    let row = row.ok_or(AuthzError::UserNotFound {
        user_id: user_id.into_uuid(),
        tenant_id: tenant_id.into_uuid(),
    })?;

    if !row.is_active {
        return Err(AuthzError::UserDeactivated { user_id: row.id });
    }

    Ok(UserAccount {
        id: UserId::from_uuid(row.id),
        tenant_id: TenantId::from_uuid(row.tenant_id),
        username: row.username,
        external_id: row.external_id,
        attributes: row
            .attributes
            .unwrap_or(JsonValue::Object(Default::default())),
        attributes_version: row.attributes_version,
        is_active: row.is_active,
        metadata: row.metadata.into(),
    })
}

/// Updates user attributes with optimistic locking.
#[tracing::instrument(skip(pool, new_attributes))]
pub async fn update_user_attributes_versioned(
    pool: &PgPool,
    user_id: UserId,
    new_attributes: &JsonValue,
    new_version: i64,
) -> Result<bool, AuthzError> {
    let result = sqlx::query(
        r#"
        UPDATE user_account
        SET    attributes         = $1,
               attributes_version = $2,
               updated_at         = $3
        WHERE  id                 = $4
          AND  attributes_version < $2
          AND  is_deleted          = false
        "#,
    )
    .bind(new_attributes)
    .bind(new_version)
    .bind(Utc::now())
    .bind(user_id.into_uuid())
    .execute(pool)
    .await?;

    let was_updated = result.rows_affected() > 0;

    if !was_updated {
        tracing::debug!(
            user_id = %user_id,
            new_version,
            "Stale attribute sync event ignored (version already up to date)"
        );
    }

    Ok(was_updated)
}
