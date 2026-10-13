use crate::{error::ApiError, middleware::service_auth::ServicePrincipal, state::AppState};
use authz_core::AuthzError;
use axum::{extract::State, Extension, Json};
use serde::{Deserialize, Serialize};
use sqlx::Row;
use uuid::Uuid;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RevokeCommand {
    pub tenant_id: Uuid,
    pub actor_id: Uuid,
    pub expected_version: i64,
    pub revoked: bool,
    pub reason: String,
}

pub async fn change(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Json(body): Json<RevokeCommand>,
) -> Result<Json<serde_json::Value>, ApiError> {
    if principal.tenant_id.as_deref() != Some(body.tenant_id.to_string().as_str())
        || !principal.capabilities.iter().any(|c| c == "authz.revoke")
    {
        return Err(AuthzError::InvalidRequest {
            reason: "revoke workload capability required".into(),
        }
        .into());
    }
    if body.expected_version < 0 || body.reason.trim().is_empty() || body.reason.len() > 2048 {
        return Err(AuthzError::InvalidRequest {
            reason: "version and bounded revoke reason required".into(),
        }
        .into());
    }
    let mut tx = state.pool.begin().await.map_err(AuthzError::from)?;
    let actual:Option<i64>=sqlx::query_scalar("SELECT version FROM user_account WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE").bind(body.tenant_id).bind(body.actor_id).fetch_optional(&mut *tx).await.map_err(AuthzError::from)?;
    let actual = actual.ok_or(AuthzError::UserNotFound {
        user_id: body.actor_id,
        tenant_id: body.tenant_id,
    })?;
    if actual != body.expected_version {
        return Err(AuthzError::VersionConflict {
            entity: "user_account",
            entity_id: body.actor_id,
            expected_version: body.expected_version,
            actual_version: actual,
        }
        .into());
    }
    let row = sqlx::query("UPDATE user_account SET query_revoke_epoch=query_revoke_epoch+1, query_revoked=$4, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND version=$3 AND NOT is_deleted RETURNING query_revoke_epoch, version")
        .bind(body.tenant_id).bind(body.actor_id).bind(body.expected_version).bind(body.revoked)
        .fetch_optional(&mut *tx).await.map_err(AuthzError::from)?
        .ok_or_else(|| AuthzError::InvalidRequest { reason: "actor absent or stale version".into() })?;
    let epoch: i64 = row.get("query_revoke_epoch");
    let version: i64 = row.get("version");
    sqlx::query("INSERT INTO query_revoke_feed(sequence,tenant_id,actor_id,revoke_epoch,revoked,reason,performed_by) SELECT revision,$1,$2,$3,$4,$5,$6 FROM query_security_revision WHERE singleton")
        .bind(body.tenant_id).bind(body.actor_id).bind(epoch).bind(body.revoked).bind(&body.reason).bind(principal.subject)
        .execute(&mut *tx).await.map_err(AuthzError::from)?;
    tx.commit().await.map_err(AuthzError::from)?;
    Ok(Json(
        serde_json::json!({"revoke_epoch":epoch,"version":version}),
    ))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FeedRequest {
    pub tenant_id: Uuid,
    pub after: i64,
    pub limit: i64,
}
#[derive(Serialize)]
pub struct FeedEvent {
    pub sequence: i64,
    pub actor_id: Uuid,
    pub revoke_epoch: i64,
    pub revoked: bool,
}
pub async fn feed(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Json(body): Json<FeedRequest>,
) -> Result<Json<serde_json::Value>, ApiError> {
    if principal.tenant_id.as_deref() != Some(body.tenant_id.to_string().as_str())
        || !principal.capabilities.iter().any(|c| c == "authz.filter")
    {
        return Err(AuthzError::InvalidRequest {
            reason: "invalid revoke feed workload scope".into(),
        }
        .into());
    }
    if body.after < 0 || !(1..=1000).contains(&body.limit) {
        return Err(AuthzError::InvalidRequest {
            reason: "invalid bounded feed cursor".into(),
        }
        .into());
    }
    let rows = sqlx::query("SELECT sequence,actor_id,revoke_epoch,revoked FROM query_revoke_feed WHERE tenant_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3")
        .bind(body.tenant_id).bind(body.after).bind(body.limit).fetch_all(&state.pool).await.map_err(AuthzError::from)?;
    let events: Vec<_> = rows
        .iter()
        .map(|r| FeedEvent {
            sequence: r.get("sequence"),
            actor_id: r.get("actor_id"),
            revoke_epoch: r.get("revoke_epoch"),
            revoked: r.get("revoked"),
        })
        .collect();
    let next = events.last().map_or(body.after, |e| e.sequence);
    Ok(Json(
        serde_json::json!({"schema_version":1,"tenant_id":body.tenant_id,"next_sequence":next,"events":events}),
    ))
}
