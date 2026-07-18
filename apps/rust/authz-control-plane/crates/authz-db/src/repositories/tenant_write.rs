use authz_core::{ids::TenantId, AuthzError};
use sqlx::PgPool;

pub enum TenantStatus {
    Active,
    Suspended,
}

pub async fn insert_tenant(
    pool: &PgPool,
    tenant_id: TenantId,
    name: &str,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO tenant (id, code, name, is_active)
        VALUES ($1, $2, $3, true)
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(name.to_lowercase().replace(" ", "_"))
    .bind(name)
    .execute(pool)
    .await?;
    Ok(())
}

pub async fn update_tenant_status(
    pool: &PgPool,
    tenant_id: TenantId,
    status: TenantStatus,
    expected_version: i64,
) -> Result<i64, AuthzError> {
    let is_active = match status {
        TenantStatus::Active => true,
        TenantStatus::Suspended => false,
    };

    let mut tx = pool.begin().await?;
    let current_version: Option<i64> = sqlx::query_scalar(
        r#"
        SELECT version
        FROM tenant
        WHERE id = $1 AND is_deleted = false
        FOR UPDATE
        "#,
    )
    .bind(tenant_id.into_uuid())
    .fetch_optional(&mut *tx)
    .await?;

    let current_version = current_version.ok_or(AuthzError::TenantNotFound {
        tenant_id: tenant_id.into_uuid(),
    })?;
    if current_version != expected_version {
        return Err(AuthzError::VersionConflict {
            entity: "tenant",
            entity_id: tenant_id.into_uuid(),
            expected_version,
            actual_version: current_version,
        });
    }

    let next_version: i64 = sqlx::query_scalar(
        r#"
        UPDATE tenant
        SET is_active = $1
        WHERE id = $2 AND version = $3 AND is_deleted = false
        RETURNING version
        "#,
    )
    .bind(is_active)
    .bind(tenant_id.into_uuid())
    .bind(expected_version)
    .fetch_one(&mut *tx)
    .await?;
    tx.commit().await?;
    Ok(next_version)
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
