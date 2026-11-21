//! Resolves a verified external IdP subject to tenant-scoped BPMP access
//! profiles. IdP claims authenticate the principal; tenant roles are read from
//! the Authz control plane and are never copied from token role claims.

use crate::{error::ApiError, middleware::service_auth::ServicePrincipal, state::AppState};
use authz_core::AuthzError;
use axum::{
    extract::{Extension, State},
    Json,
};
use serde::{Deserialize, Serialize};
use sqlx::Row;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ResolveSubjectRequest {
    pub issuer: String,
    pub subject: String,
}

#[derive(Debug, Serialize)]
pub struct TenantAccessProfile {
    pub tenant_id: uuid::Uuid,
    pub user_id: uuid::Uuid,
    pub username: String,
    pub active: bool,
    pub version: i64,
    pub attributes_version: i64,
    pub roles: Vec<String>,
}

#[derive(Debug, Serialize)]
pub struct ResolveSubjectResponse {
    pub profiles: Vec<TenantAccessProfile>,
}

pub async fn resolve_subject(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Json(request): Json<ResolveSubjectRequest>,
) -> Result<Json<ResolveSubjectResponse>, ApiError> {
    if !principal
        .capabilities
        .iter()
        .any(|value| value == "identity.subject.resolve")
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: "identity_subject_resolve".into(),
            reason: "service principal cannot resolve IdP subjects".into(),
        }
        .into());
    }
    let issuer = request.issuer.trim();
    let subject = request.subject.trim();
    if issuer.is_empty() || issuer.len() > 512 || subject.is_empty() || subject.len() > 255 {
        return Err(AuthzError::InvalidRequest {
            reason: "issuer and subject are required and must fit their contract limits".into(),
        }
        .into());
    }

    let rows = sqlx::query(
        r#"SELECT u.id, u.tenant_id, u.username, u.is_active, u.version,
                  u.attributes_version,
                  COALESCE(array_agg(r.code ORDER BY r.code)
                      FILTER (WHERE r.code IS NOT NULL), ARRAY[]::text[]) AS roles
           FROM user_account u
           JOIN tenant t ON t.id = u.tenant_id AND t.is_active
           LEFT JOIN user_role ur ON ur.user_id = u.id AND ur.tenant_id = u.tenant_id
                AND (ur.expires_at IS NULL OR ur.expires_at > now())
           LEFT JOIN role r ON r.id = ur.role_id AND r.tenant_id = u.tenant_id
           WHERE u.idp_issuer = $1 AND u.idp_subject = $2
             AND u.is_active AND NOT u.is_deleted
           GROUP BY u.id
           ORDER BY u.tenant_id
           LIMIT 101"#,
    )
    .bind(issuer)
    .bind(subject)
    .fetch_all(&state.pool)
    .await?;

    // Gateway accepts at most 100 tenant profiles. Bound materialization here
    // as well so an unexpectedly broad subject link cannot exhaust the owner.
    if rows.len() > 100 {
        return Err(AuthzError::Internal(
            "OIDC subject resolves to too many tenant profiles".into(),
        )
        .into());
    }

    let mut profiles = Vec::with_capacity(rows.len());
    for row in rows {
        profiles.push(TenantAccessProfile {
            tenant_id: row.try_get("tenant_id")?,
            user_id: row.try_get("id")?,
            username: row.try_get("username")?,
            active: row.try_get("is_active")?,
            version: row.try_get("version")?,
            attributes_version: row.try_get("attributes_version")?,
            roles: row.try_get("roles")?,
        });
    }

    Ok(Json(ResolveSubjectResponse { profiles }))
}
