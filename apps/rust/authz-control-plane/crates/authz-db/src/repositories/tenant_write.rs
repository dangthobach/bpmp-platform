use authz_core::{
    ids::TenantId,
    models::tenant::{Tenant, TenantConfig},
    AuthzError,
};
use serde_json::Value as JsonValue;
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use super::tenant::get_tenant_for_admin;

pub enum TenantStatus {
    Active,
    Suspended,
}

pub struct TenantMutationAudit<'a> {
    pub actor_ref: &'a str,
    pub request_id: &'a str,
}

pub struct CreateTenant<'a> {
    pub tenant_id: TenantId,
    pub code: &'a str,
    pub name: &'a str,
    pub config: &'a TenantConfig,
}

pub struct UpdateTenant<'a> {
    pub code: Option<&'a str>,
    pub name: Option<&'a str>,
    pub config: Option<&'a TenantConfig>,
    pub is_active: Option<bool>,
    pub expected_version: i64,
}

pub async fn insert_tenant(
    pool: &PgPool,
    command: CreateTenant<'_>,
    audit: TenantMutationAudit<'_>,
) -> Result<Tenant, AuthzError> {
    let mut tx = pool.begin().await?;
    let actor_id = Uuid::parse_str(audit.actor_ref).ok();
    let config = serde_json::to_value(command.config)?;
    let insert = sqlx::query_scalar::<_, i64>(
        r#"
        INSERT INTO tenant (id, code, name, is_active, config, created_by, updated_by)
        VALUES ($1, $2, $3, true, $4, $5, $5)
        RETURNING version
        "#,
    )
    .bind(command.tenant_id.into_uuid())
    .bind(command.code)
    .bind(command.name)
    .bind(config)
    .bind(actor_id)
    .fetch_one(&mut *tx)
    .await;
    let version = map_tenant_code_conflict(insert, command.code)?;
    let current = tenant_json(&mut tx, command.tenant_id).await?;
    insert_tenant_audit(
        &mut tx,
        command.tenant_id,
        "CREATE",
        version,
        &audit,
        None,
        Some(current),
    )
    .await?;
    tx.commit().await?;
    get_tenant_for_admin(pool, command.tenant_id).await
}

pub async fn update_tenant(
    pool: &PgPool,
    tenant_id: TenantId,
    command: UpdateTenant<'_>,
    audit: TenantMutationAudit<'_>,
) -> Result<Tenant, AuthzError> {
    if command.code.is_none()
        && command.name.is_none()
        && command.config.is_none()
        && command.is_active.is_none()
    {
        return Err(AuthzError::InvalidRequest {
            reason: "tenant update contains no changes".into(),
        });
    }
    let mut tx = pool.begin().await?;
    let (previous, current_version) = lock_tenant(&mut tx, tenant_id).await?;
    ensure_version(tenant_id, command.expected_version, current_version)?;
    let actor_id = Uuid::parse_str(audit.actor_ref).ok();
    let config = command.config.map(serde_json::to_value).transpose()?;
    let updated = sqlx::query_as::<_, (i64, JsonValue)>(
        r#"
        UPDATE tenant
        SET code = COALESCE($1, code),
            name = COALESCE($2, name),
            config = COALESCE($3, config),
            is_active = COALESCE($4, is_active),
            updated_by = $5
        WHERE id = $6 AND version = $7 AND is_deleted = false
        RETURNING version, to_jsonb(tenant)
        "#,
    )
    .bind(command.code)
    .bind(command.name)
    .bind(config)
    .bind(command.is_active)
    .bind(actor_id)
    .bind(tenant_id.into_uuid())
    .bind(command.expected_version)
    .fetch_one(&mut *tx)
    .await;
    let (version, current) = map_tenant_code_conflict(updated, command.code.unwrap_or_default())?;
    insert_tenant_audit(
        &mut tx,
        tenant_id,
        "UPDATE",
        version,
        &audit,
        Some(previous),
        Some(current),
    )
    .await?;
    tx.commit().await?;
    get_tenant_for_admin(pool, tenant_id).await
}

pub async fn update_tenant_status(
    pool: &PgPool,
    tenant_id: TenantId,
    status: TenantStatus,
    expected_version: i64,
    audit: TenantMutationAudit<'_>,
) -> Result<i64, AuthzError> {
    let is_active = match status {
        TenantStatus::Active => true,
        TenantStatus::Suspended => false,
    };

    let mut tx = pool.begin().await?;
    let (previous, current_version) = lock_tenant(&mut tx, tenant_id).await?;
    ensure_version(tenant_id, expected_version, current_version)?;
    let actor_id = Uuid::parse_str(audit.actor_ref).ok();
    let (next_version, current): (i64, JsonValue) = sqlx::query_as(
        r#"
        UPDATE tenant
        SET is_active = $1, updated_by = $4
        WHERE id = $2 AND version = $3 AND is_deleted = false
        RETURNING version, to_jsonb(tenant)
        "#,
    )
    .bind(is_active)
    .bind(tenant_id.into_uuid())
    .bind(expected_version)
    .bind(actor_id)
    .fetch_one(&mut *tx)
    .await?;
    insert_tenant_audit(
        &mut tx,
        tenant_id,
        "STATUS",
        next_version,
        &audit,
        Some(previous),
        Some(current),
    )
    .await?;
    tx.commit().await?;
    Ok(next_version)
}

pub async fn delete_tenant(
    pool: &PgPool,
    tenant_id: TenantId,
    expected_version: i64,
    audit: TenantMutationAudit<'_>,
) -> Result<i64, AuthzError> {
    let mut tx = pool.begin().await?;
    let (previous, current_version) = lock_tenant(&mut tx, tenant_id).await?;
    ensure_version(tenant_id, expected_version, current_version)?;
    let actor_id = Uuid::parse_str(audit.actor_ref).ok();
    let (version, current): (i64, JsonValue) = sqlx::query_as(
        r#"
        UPDATE tenant
        SET is_active = false,
            is_deleted = true,
            deleted_by = $1,
            updated_by = $1
        WHERE id = $2 AND version = $3 AND is_deleted = false
        RETURNING version, to_jsonb(tenant)
        "#,
    )
    .bind(actor_id)
    .bind(tenant_id.into_uuid())
    .bind(expected_version)
    .fetch_one(&mut *tx)
    .await?;
    insert_tenant_audit(
        &mut tx,
        tenant_id,
        "DELETE",
        version,
        &audit,
        Some(previous),
        Some(current),
    )
    .await?;
    tx.commit().await?;
    Ok(version)
}

async fn lock_tenant(
    tx: &mut Transaction<'_, Postgres>,
    tenant_id: TenantId,
) -> Result<(JsonValue, i64), AuthzError> {
    sqlx::query_as(
        r#"
        SELECT to_jsonb(tenant), version
        FROM tenant
        WHERE id = $1 AND is_deleted = false
        FOR UPDATE
        "#,
    )
    .bind(tenant_id.into_uuid())
    .fetch_optional(&mut **tx)
    .await?
    .ok_or(AuthzError::TenantNotFound {
        tenant_id: tenant_id.into_uuid(),
    })
}

fn ensure_version(
    tenant_id: TenantId,
    expected_version: i64,
    actual_version: i64,
) -> Result<(), AuthzError> {
    if expected_version == actual_version {
        return Ok(());
    }
    Err(AuthzError::VersionConflict {
        entity: "tenant",
        entity_id: tenant_id.into_uuid(),
        expected_version,
        actual_version,
    })
}

async fn tenant_json(
    tx: &mut Transaction<'_, Postgres>,
    tenant_id: TenantId,
) -> Result<JsonValue, AuthzError> {
    Ok(
        sqlx::query_scalar("SELECT to_jsonb(tenant) FROM tenant WHERE id = $1")
            .bind(tenant_id.into_uuid())
            .fetch_one(&mut **tx)
            .await?,
    )
}

#[allow(clippy::too_many_arguments)]
async fn insert_tenant_audit(
    tx: &mut Transaction<'_, Postgres>,
    tenant_id: TenantId,
    operation: &str,
    entity_version: i64,
    audit: &TenantMutationAudit<'_>,
    previous: Option<JsonValue>,
    current: Option<JsonValue>,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO tenant_audit_log
            (tenant_id, operation, entity_version, actor_ref, request_id,
             previous_value, current_value)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(operation)
    .bind(entity_version)
    .bind(audit.actor_ref)
    .bind(audit.request_id)
    .bind(previous)
    .bind(current)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

fn map_tenant_code_conflict<T>(
    result: Result<T, sqlx::Error>,
    code: &str,
) -> Result<T, AuthzError> {
    match result {
        Err(sqlx::Error::Database(error)) if error.code().as_deref() == Some("23505") => {
            Err(AuthzError::TenantCodeConflict {
                code: code.to_owned(),
            })
        }
        Err(error) => Err(AuthzError::Database(error)),
        Ok(value) => Ok(value),
    }
}

/// Deactivates users who have not been active for a specified number of days.
///
/// Uses `FOR UPDATE SKIP LOCKED` and `LIMIT` to avoid locking the entire table
/// and to keep memory usage low. Returns the number of users deactivated in this batch.
#[tracing::instrument(skip(pool))]
pub async fn deactivate_inactive_users(
    pool: &PgPool,
    days: i32,
    batch_size: i64,
) -> Result<u64, AuthzError> {
    let result = sqlx::query(
        r#"
        UPDATE user_account 
        SET is_active = false, updated_at = now()
        WHERE id IN (
            SELECT id FROM user_account 
            WHERE is_active = true 
              AND is_deleted = false
              AND last_active_at < now() - ($1::int * INTERVAL '1 day')
            LIMIT $2 
            FOR UPDATE SKIP LOCKED
        )
          AND is_deleted = false
        "#,
    )
    .bind(days)
    .bind(batch_size)
    .execute(pool)
    .await?;

    Ok(result.rows_affected())
}
