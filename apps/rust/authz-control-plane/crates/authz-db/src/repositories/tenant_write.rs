use authz_core::{
    ids::TenantId,
    models::tenant::{Tenant, TenantConfig},
    AuthzError,
};
use chrono::Utc;
use serde_json::Value as JsonValue;
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use super::tenant::get_tenant_for_admin;

#[derive(Clone, Copy)]
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

pub struct TenantConfigurationReadiness<'a> {
    pub tenant_id: TenantId,
    pub tenant_version: i64,
    pub ready: bool,
    pub profile_set_hash: &'a [u8; 32],
    pub event_id: Uuid,
    pub event_sequence: i64,
}

pub struct UpdateTenant<'a> {
    pub code: Option<&'a str>,
    pub name: Option<&'a str>,
    pub config: Option<&'a TenantConfig>,
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
        VALUES ($1, $2, $3, false, $4, $5, $5)
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
    insert_tenant_outbox(
        &mut tx,
        command.tenant_id,
        command.code,
        version,
        "CREATED",
        &audit,
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
    if command.code.is_none() && command.name.is_none() && command.config.is_none() {
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
            updated_by = $4
        WHERE id = $5 AND version = $6 AND is_deleted = false
        RETURNING version, to_jsonb(tenant)
        "#,
    )
    .bind(command.code)
    .bind(command.name)
    .bind(config)
    .bind(actor_id)
    .bind(tenant_id.into_uuid())
    .bind(command.expected_version)
    .fetch_one(&mut *tx)
    .await;
    let (version, current) = map_tenant_code_conflict(updated, command.code.unwrap_or_default())?;
    let current_code = tenant_code(&current)?.to_owned();
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
    insert_tenant_outbox(
        &mut tx,
        tenant_id,
        &current_code,
        version,
        "UPDATED",
        &audit,
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
    if matches!(status, TenantStatus::Active) {
        let ready = sqlx::query_scalar::<_, bool>(
            r#"
            SELECT ready
            FROM tenant_configuration_readiness
            WHERE tenant_id = $1 AND tenant_version = $2
            FOR SHARE
            "#,
        )
        .bind(tenant_id.into_uuid())
        .bind(current_version)
        .fetch_optional(&mut *tx)
        .await?
        .unwrap_or(false);
        if !ready {
            return Err(AuthzError::InvalidRequest {
                reason: "tenant configuration is not ready for activation".into(),
            });
        }
    }
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
    let current_code = tenant_code(&current)?.to_owned();
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
    let lifecycle_kind = match status {
        TenantStatus::Active => "ACTIVATED",
        TenantStatus::Suspended => "SUSPENDED",
    };
    insert_tenant_outbox(
        &mut tx,
        tenant_id,
        &current_code,
        next_version,
        lifecycle_kind,
        &audit,
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
    let current_code = tenant_code(&current)?.to_owned();
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
    insert_tenant_outbox(
        &mut tx,
        tenant_id,
        &current_code,
        version,
        "DELETED",
        &audit,
    )
    .await?;
    tx.commit().await?;
    Ok(version)
}

pub async fn apply_tenant_configuration_readiness(
    pool: &PgPool,
    readiness: TenantConfigurationReadiness<'_>,
) -> Result<bool, AuthzError> {
    let mut tx = pool.begin().await?;
    let current_version = sqlx::query_scalar::<_, i64>(
        "SELECT version FROM tenant WHERE id = $1 AND is_deleted = false FOR UPDATE",
    )
    .bind(readiness.tenant_id.into_uuid())
    .fetch_optional(&mut *tx)
    .await?
    .ok_or(AuthzError::TenantNotFound {
        tenant_id: readiness.tenant_id.into_uuid(),
    })?;
    if current_version != readiness.tenant_version {
        return Ok(false);
    }
    let tag = sqlx::query(
        r#"
        INSERT INTO tenant_configuration_readiness
            (tenant_id, tenant_version, ready, profile_set_hash, event_id, event_sequence, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp())
        ON CONFLICT (tenant_id) DO UPDATE
        SET tenant_version = EXCLUDED.tenant_version,
            ready = EXCLUDED.ready,
            profile_set_hash = EXCLUDED.profile_set_hash,
            event_id = EXCLUDED.event_id,
            event_sequence = EXCLUDED.event_sequence,
            updated_at = EXCLUDED.updated_at
        WHERE tenant_configuration_readiness.event_sequence < EXCLUDED.event_sequence
        "#,
    )
    .bind(readiness.tenant_id.into_uuid())
    .bind(readiness.tenant_version)
    .bind(readiness.ready)
    .bind(readiness.profile_set_hash.as_slice())
    .bind(readiness.event_id)
    .bind(readiness.event_sequence)
    .execute(&mut *tx)
    .await?;
    tx.commit().await?;
    Ok(tag.rows_affected() == 1)
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

async fn insert_tenant_outbox(
    tx: &mut Transaction<'_, Postgres>,
    tenant_id: TenantId,
    tenant_code: &str,
    tenant_version: i64,
    lifecycle_kind: &str,
    audit: &TenantMutationAudit<'_>,
) -> Result<(), AuthzError> {
    let event_id = Uuid::new_v4();
    let event_sequence: i64 =
        sqlx::query_scalar("SELECT nextval('tenant_lifecycle_event_sequence')")
            .fetch_one(&mut **tx)
            .await?;
    let occurred_at = Utc::now();
    let kind = match lifecycle_kind {
        "CREATED" => 1,
        "UPDATED" => 2,
        "ACTIVATION_REQUESTED" => 3,
        "ACTIVATED" => 4,
        "SUSPENDED" => 5,
        "DELETED" => 6,
        _ => {
            return Err(AuthzError::InvalidRequest {
                reason: "tenant lifecycle kind is invalid".into(),
            });
        }
    };
    let payload = serde_json::json!({
        "schema_version": 1,
        "event_id": event_id.to_string(),
        "event_sequence": event_sequence,
        "tenant_id": tenant_id.into_uuid().to_string(),
        "tenant_code": tenant_code,
        "tenant_version": tenant_version,
        "kind": kind,
        "actor_ref": audit.actor_ref,
        "correlation_id": audit.request_id,
        "occurred_at_epoch_ms": occurred_at.timestamp_millis(),
    });
    sqlx::query(
        r#"
        INSERT INTO tenant_lifecycle_outbox
            (event_id, event_sequence, tenant_id, tenant_code, tenant_version,
             lifecycle_kind, actor_ref, correlation_id, payload, occurred_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
        "#,
    )
    .bind(event_id)
    .bind(event_sequence)
    .bind(tenant_id.into_uuid())
    .bind(tenant_code)
    .bind(tenant_version)
    .bind(lifecycle_kind)
    .bind(audit.actor_ref)
    .bind(audit.request_id)
    .bind(payload)
    .bind(occurred_at)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

fn tenant_code(value: &JsonValue) -> Result<&str, AuthzError> {
    value
        .get("code")
        .and_then(JsonValue::as_str)
        .ok_or_else(|| AuthzError::InvalidRequest {
            reason: "tenant code is missing from lifecycle state".into(),
        })
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
