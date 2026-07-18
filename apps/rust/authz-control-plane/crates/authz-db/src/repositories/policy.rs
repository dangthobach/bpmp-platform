//! Policy repository — decision logging, policy version management, shadow log.

use authz_core::{
    ids::{AuditLogId, PolicyVersionId, TenantId},
    models::{
        audit::{AuthzDecisionLog, DecisionContext, EvalTrace},
        policy::{AuthzDecision, PolicyVersion, PolicyVersionStatus},
    },
    AuthzError,
};
use chrono::Utc;
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use uuid::Uuid;

use super::metadata::MetadataRow;

// ─── Internal Row Types ──────────────────────────────────────────────────────

#[derive(sqlx::FromRow)]
#[allow(dead_code)]
struct PolicyVersionRow {
    id: Uuid,
    policy_id: Uuid,
    version_num: i32,
    snapshot: Option<JsonValue>,
    status: String,
    published_by: Option<Uuid>,
    published_at: Option<chrono::DateTime<chrono::Utc>>,
    notes: Option<String>,
    #[sqlx(flatten)]
    metadata: MetadataRow,
}

#[derive(sqlx::FromRow)]
#[allow(dead_code)]
struct DecisionLogRow {
    id: Uuid,
    tenant_id: Uuid,
    user_id: Uuid,
    resource_type: String,
    resource_ref: Option<String>,
    action: String,
    decision: String,
    matched_policy_id: Option<Uuid>,
    policy_version_id: Option<Uuid>,
    eval_trace: Option<JsonValue>,
    context: Option<JsonValue>,
    decided_at: chrono::DateTime<chrono::Utc>,
    sidecar_id: Option<String>,
}

#[derive(sqlx::FromRow)]
struct ShadowReportRow {
    total_count: Option<i64>,
    diverged_count: Option<i64>,
    new_denials: Option<i64>,
    new_allows: Option<i64>,
}

// ─── Policy Version ───────────────────────────────────────────────────────────

/// Finds the currently ACTIVE policy version for a given tenant and resource type.
#[tracing::instrument(skip(pool))]
pub async fn find_active_policy_version(
    pool: &PgPool,
    tenant_id: TenantId,
    resource_type: &str,
) -> Result<Option<PolicyVersion>, AuthzError> {
    let row: Option<PolicyVersionRow> = sqlx::query_as(
        r#"
        SELECT pv.id, pv.policy_id, pv.version_num, pv.snapshot,
               pv.status, pv.published_by, pv.published_at, pv.notes,
               pv.version, pv.is_deleted, pv.deleted_at, pv.deleted_by,
               pv.created_at, pv.created_by, pv.updated_at, pv.updated_by
        FROM   policy_version pv
        JOIN   policy p ON p.id = pv.policy_id
        JOIN   policy_rule pr ON pr.policy_id = p.id
        WHERE  p.tenant_id       = $1
          AND  pr.resource_type  = $2
          AND  pv.status         = 'ACTIVE'
          AND  p.is_active       = true
          AND  pv.is_deleted     = false
          AND  p.is_deleted      = false
          AND  pr.is_deleted     = false
        ORDER BY pv.version_num DESC
        LIMIT 1
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(resource_type)
    .fetch_optional(pool)
    .await?;

    Ok(row.map(|r| PolicyVersion {
        id: PolicyVersionId::from_uuid(r.id),
        policy_id: authz_core::ids::PolicyId::from_uuid(r.policy_id),
        version_num: r.version_num,
        snapshot: r.snapshot.unwrap_or(JsonValue::Object(Default::default())),
        status: PolicyVersionStatus::Active,
        published_by: r.published_by,
        published_at: r.published_at,
        notes: r.notes,
        metadata: r.metadata.into(),
    }))
}

/// Finds the SHADOW policy version (for parallel evaluation in shadow mode).
#[tracing::instrument(skip(pool))]
pub async fn find_shadow_policy_version(
    pool: &PgPool,
    tenant_id: TenantId,
    resource_type: &str,
) -> Result<Option<PolicyVersion>, AuthzError> {
    let row: Option<PolicyVersionRow> = sqlx::query_as(
        r#"
        SELECT pv.id, pv.policy_id, pv.version_num, pv.snapshot,
               pv.status, pv.published_by, pv.published_at, pv.notes,
               pv.version, pv.is_deleted, pv.deleted_at, pv.deleted_by,
               pv.created_at, pv.created_by, pv.updated_at, pv.updated_by
        FROM   policy_version pv
        JOIN   policy p ON p.id = pv.policy_id
        JOIN   policy_rule pr ON pr.policy_id = p.id
        WHERE  p.tenant_id       = $1
          AND  pr.resource_type  = $2
          AND  pv.status         = 'SHADOW'
          AND  p.is_active       = true
          AND  pv.is_deleted     = false
          AND  p.is_deleted      = false
          AND  pr.is_deleted     = false
        ORDER BY pv.version_num DESC
        LIMIT 1
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(resource_type)
    .fetch_optional(pool)
    .await?;

    Ok(row.map(|r| PolicyVersion {
        id: PolicyVersionId::from_uuid(r.id),
        policy_id: authz_core::ids::PolicyId::from_uuid(r.policy_id),
        version_num: r.version_num,
        snapshot: r.snapshot.unwrap_or(JsonValue::Object(Default::default())),
        status: PolicyVersionStatus::Shadow,
        published_by: r.published_by,
        published_at: r.published_at,
        notes: r.notes,
        metadata: r.metadata.into(),
    }))
}

// ─── Decision Log ─────────────────────────────────────────────────────────────

/// Records an authorization decision.
///
/// Idempotent: ON CONFLICT DO NOTHING prevents duplicate entries from WAL relay retries.
#[tracing::instrument(skip(pool, log), fields(
    decision = ?log.decision,
    user_id = %log.user_id,
    action = %log.action
))]
pub async fn insert_decision_log(pool: &PgPool, log: &AuthzDecisionLog) -> Result<(), AuthzError> {
    let decision_str = match log.decision {
        AuthzDecision::Allow => "ALLOW",
        AuthzDecision::Deny => "DENY",
    };

    let eval_trace = serde_json::to_value(&log.eval_trace).map_err(AuthzError::Serialization)?;
    let context = serde_json::to_value(&log.context).map_err(AuthzError::Serialization)?;

    sqlx::query(
        r#"
        INSERT INTO authz_decision_log
            (id, tenant_id, user_id, resource_type, resource_ref,
             action, decision, matched_policy_id, policy_version_id,
             eval_trace, context, decided_at, sidecar_id)
        VALUES
            ($1, $2, $3, $4, $5,
             $6, $7, $8, $9,
             $10, $11, $12, $13)
        ON CONFLICT (id) DO NOTHING
        "#,
    )
    .bind(log.id.into_uuid())
    .bind(log.tenant_id.into_uuid())
    .bind(log.user_id)
    .bind(&log.resource_type)
    .bind(&log.resource_ref)
    .bind(&log.action)
    .bind(decision_str)
    .bind(log.matched_policy_id.map(|id| id.into_uuid()))
    .bind(log.policy_version_id.map(|id| id.into_uuid()))
    .bind(eval_trace)
    .bind(context)
    .bind(log.decided_at)
    .bind(&log.sidecar_id)
    .execute(pool)
    .await?;

    Ok(())
}

/// Retrieves the most recent decision log for a user + resource + action.
///
/// Used by the Explain API.
#[tracing::instrument(skip(pool))]
pub async fn find_latest_decision(
    pool: &PgPool,
    tenant_id: TenantId,
    user_id: Uuid,
    resource_ref: &str,
    action: &str,
) -> Result<Option<AuthzDecisionLog>, AuthzError> {
    let row: Option<DecisionLogRow> = sqlx::query_as(
        r#"
        SELECT id, tenant_id, user_id, resource_type, resource_ref,
               action, decision, matched_policy_id, policy_version_id,
               eval_trace, context, decided_at, sidecar_id
        FROM   authz_decision_log
        WHERE  tenant_id    = $1
          AND  user_id      = $2
          AND  resource_ref = $3
          AND  action       = $4
        ORDER BY decided_at DESC
        LIMIT 1
        "#,
    )
    .bind(tenant_id.into_uuid())
    .bind(user_id)
    .bind(resource_ref)
    .bind(action)
    .fetch_optional(pool)
    .await?;

    match row {
        None => Ok(None),
        Some(r) => {
            let decision = if r.decision == "ALLOW" {
                AuthzDecision::Allow
            } else {
                AuthzDecision::Deny
            };

            let eval_trace: EvalTrace =
                serde_json::from_value(r.eval_trace.unwrap_or(JsonValue::Null))
                    .map_err(AuthzError::Serialization)?;
            let context: DecisionContext =
                serde_json::from_value(r.context.unwrap_or(JsonValue::Null))
                    .map_err(AuthzError::Serialization)?;

            Ok(Some(AuthzDecisionLog {
                id: AuditLogId::from_uuid(r.id),
                tenant_id,
                user_id: r.user_id,
                resource_type: r.resource_type,
                resource_ref: r.resource_ref,
                action: r.action,
                decision,
                matched_policy_id: r
                    .matched_policy_id
                    .map(authz_core::ids::PolicyId::from_uuid),
                policy_version_id: r.policy_version_id.map(PolicyVersionId::from_uuid),
                eval_trace,
                context,
                decided_at: r.decided_at,
                sidecar_id: r.sidecar_id,
            }))
        }
    }
}

/// Records shadow divergence when shadow and active policies disagree.
#[tracing::instrument(skip(pool))]
#[allow(clippy::too_many_arguments)]
pub async fn insert_shadow_log(
    pool: &PgPool,
    shadow_version_id: PolicyVersionId,
    user_id: Option<Uuid>,
    resource_ref: Option<&str>,
    action: &str,
    shadow_decision: AuthzDecision,
    active_decision: AuthzDecision,
    context_snapshot: Option<&JsonValue>,
) -> Result<(), AuthzError> {
    let shadow_str = match shadow_decision {
        AuthzDecision::Allow => "ALLOW",
        AuthzDecision::Deny => "DENY",
    };
    let active_str = match active_decision {
        AuthzDecision::Allow => "ALLOW",
        AuthzDecision::Deny => "DENY",
    };

    sqlx::query(
        r#"
        INSERT INTO policy_shadow_log
            (policy_version_id, user_id, resource_ref, action,
             shadow_decision, active_decision, context_snapshot, logged_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        "#,
    )
    .bind(shadow_version_id.into_uuid())
    .bind(user_id)
    .bind(resource_ref)
    .bind(action)
    .bind(shadow_str)
    .bind(active_str)
    .bind(context_snapshot)
    .bind(Utc::now())
    .execute(pool)
    .await?;

    Ok(())
}

/// Returns the divergence statistics for a shadow policy version.
///
/// Used by the CLI to decide whether to block promotion.
#[tracing::instrument(skip(pool))]
pub async fn get_shadow_divergence_report(
    pool: &PgPool,
    shadow_version_id: PolicyVersionId,
    since_days: i32,
) -> Result<ShadowDivergenceReport, AuthzError> {
    let row: ShadowReportRow = sqlx::query_as(
        r#"
        SELECT
            COUNT(*)                                    AS total_count,
            COUNT(*) FILTER (WHERE diverged)            AS diverged_count,
            COUNT(*) FILTER (WHERE shadow_decision = 'DENY'  AND active_decision = 'ALLOW') AS new_denials,
            COUNT(*) FILTER (WHERE shadow_decision = 'ALLOW' AND active_decision = 'DENY')  AS new_allows
        FROM policy_shadow_log
        WHERE policy_version_id = $1
          AND logged_at > NOW() - ($2::int * INTERVAL '1 day')
        "#
    )
    .bind(shadow_version_id.into_uuid())
    .bind(since_days as f64)
    .fetch_one(pool)
    .await?;

    let total = row.total_count.unwrap_or(0) as u64;
    let diverged = row.diverged_count.unwrap_or(0) as u64;
    let divergence_pct = if total > 0 {
        (diverged as f64 / total as f64) * 100.0
    } else {
        0.0
    };

    Ok(ShadowDivergenceReport {
        policy_version_id: shadow_version_id,
        total_evaluations: total,
        diverged_count: diverged,
        divergence_pct,
        new_denials: row.new_denials.unwrap_or(0) as u64,
        new_allows: row.new_allows.unwrap_or(0) as u64,
    })
}

/// Divergence report for a shadow policy version.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ShadowDivergenceReport {
    pub policy_version_id: PolicyVersionId,
    pub total_evaluations: u64,
    pub diverged_count: u64,
    pub divergence_pct: f64,
    pub new_denials: u64,
    pub new_allows: u64,
}
