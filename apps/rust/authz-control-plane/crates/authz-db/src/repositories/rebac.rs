//! ReBAC repository — relation tuple queries and materialized reachability.
//!
//! Implements EC-2 two-tier lookup:
//! 1. Try O(1) materialized reachability table
//! 2. Fall back to live WITH RECURSIVE traversal with depth limit

use authz_core::{ids::TenantId, AuthzError};
use sqlx::PgPool;

/// Checks if `subject` can reach `object` via `relation` — using the
/// materialized reachability table (O(1) lookup).
///
/// Returns `None` if the materialized table has no entry (stale or not yet computed).
#[tracing::instrument(skip(pool))]
pub async fn check_reachability_materialized(
    pool: &PgPool,
    tenant_id: TenantId,
    subject: &str,
    relation: &str,
    object: &str,
) -> Result<Option<bool>, AuthzError> {
    let row: Option<bool> = sqlx::query_scalar(
        r#"
        SELECT EXISTS (
            SELECT 1
            FROM   relation_reachability
            WHERE  tenant_id = $1
              AND  subject   = $2
              AND  relation  = $3
              AND  object    = $4
        )
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(subject)
    .bind(relation)
    .bind(object)
    .fetch_one(pool)
    .await?;

    Ok(row)
}

/// Live traversal: fetches all direct objects for a subject+relation (one hop).
///
/// Used by the recursive live traversal in the ReBAC engine.
/// Limited result set — the engine controls depth via recursion counter.
#[tracing::instrument(skip(pool))]
pub async fn find_direct_objects(
    pool: &PgPool,
    tenant_id: TenantId,
    subject: &str,
    relation: &str,
    max_results: i64,
) -> Result<Vec<String>, AuthzError> {
    let rows: Vec<String> = sqlx::query_scalar(
        r#"
        SELECT object
        FROM   relation_tuple
        WHERE  tenant_id = $1
          AND  subject   = $2
          AND  relation  = $3
          AND  is_deleted = false
          AND  (expires_at IS NULL OR expires_at > NOW())
        LIMIT $4
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(subject)
    .bind(relation)
    .bind(max_results)
    .fetch_all(pool)
    .await?;

    Ok(rows)
}

/// Resolves all objects transitively reachable from `subject` via `relation`.
///
/// Used by ES/Mongo translators to inject a `terms` filter (Gap5).
/// Bounded by `max_results` to prevent unbounded result sets.
#[tracing::instrument(skip(pool))]
pub async fn resolve_reachable_objects(
    pool: &PgPool,
    tenant_id: TenantId,
    subject: &str,
    relation: &str,
    max_results: i64,
) -> Result<Vec<String>, AuthzError> {
    // Try materialized first (O(1) per row)
    let rows: Vec<String> = sqlx::query_scalar(
        r#"
        SELECT object
        FROM   relation_reachability
        WHERE  tenant_id = $1
          AND  subject   = $2
          AND  relation  = $3
        ORDER BY depth ASC
        LIMIT $4
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(subject)
    .bind(relation)
    .bind(max_results)
    .fetch_all(pool)
    .await?;

    if !rows.is_empty() {
        return Ok(rows);
    }

    // Fallback: live traversal with CTE (bounded)
    let rows: Vec<String> = sqlx::query_scalar(
        r#"
        WITH RECURSIVE reachable AS (
            SELECT object, 1 AS depth
            FROM   relation_tuple
            WHERE  tenant_id = $1
              AND  subject   = $2
              AND  relation  = $3
              AND  is_deleted = false
              AND  (expires_at IS NULL OR expires_at > NOW())

            UNION

            SELECT rt.object, r.depth + 1
            FROM   relation_tuple rt
            JOIN   reachable r ON rt.subject = r.object
            WHERE  rt.tenant_id = $1
              AND  rt.relation  = $3
              AND  rt.is_deleted = false
              AND  (rt.expires_at IS NULL OR rt.expires_at > NOW())
              AND  r.depth < 10  -- Hard depth limit: EC-2
        )
        SELECT DISTINCT object
        FROM   reachable
        LIMIT $4
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(subject)
    .bind(relation)
    .bind(max_results)
    .fetch_all(pool)
    .await?;

    Ok(rows)
}

/// Inserts a new relation tuple.
///
/// Returns `Err` if the trigger detects a cycle or fanout violation.
#[tracing::instrument(skip(pool))]
pub async fn insert_relation_tuple(
    pool: &PgPool,
    tenant_id: TenantId,
    subject: &str,
    relation: &str,
    object: &str,
    expires_at: Option<chrono::DateTime<chrono::Utc>>,
) -> Result<uuid::Uuid, AuthzError> {
    let id: Option<uuid::Uuid> = sqlx::query_scalar(
        r#"
        INSERT INTO relation_tuple (tenant_id, subject, relation, object, expires_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (tenant_id, subject, relation, object) DO NOTHING
        RETURNING id
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(subject)
    .bind(relation)
    .bind(object)
    .bind(expires_at)
    .fetch_optional(pool)
    .await?;

    match id {
        Some(id) => Ok(id),
        None => {
            // Idempotent: tuple already exists
            let existing: uuid::Uuid = sqlx::query_scalar(
                r#"
                SELECT id FROM relation_tuple
                WHERE tenant_id = $1 AND subject = $2 AND relation = $3 AND object = $4
                  AND is_deleted = false
                "#,
            )
            .bind(tenant_id.into_uuid())
            .bind(subject)
            .bind(relation)
            .bind(object)
            .fetch_one(pool)
            .await?;
            Ok(existing)
        }
    }
}
