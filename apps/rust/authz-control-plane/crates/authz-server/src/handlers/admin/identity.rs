use crate::{error::ApiError, middleware::service_auth::ServicePrincipal, state::AppState};
use authz_core::AuthzError;
use axum::{
    extract::{Extension, Query, State},
    Json,
};
use serde::{Deserialize, Serialize};
use sqlx::Row;
use uuid::Uuid;

#[derive(Debug, Deserialize)]
pub struct ListUsersQuery {
    pub tenant_id: Uuid,
    pub after_user_id: Option<Uuid>,
    pub limit: Option<i64>,
}

#[derive(Debug, Serialize)]
pub struct UserDirectoryEntry {
    pub user_id: Uuid,
    pub username: String,
    pub external_id: Option<String>,
    pub is_active: bool,
    pub query_revoked: bool,
    pub attributes_version: i64,
    pub roles: Vec<String>,
    pub version: i64,
    pub created_at_epoch_ms: u64,
    pub updated_at_epoch_ms: u64,
}

#[derive(Debug, Serialize)]
pub struct UserDirectoryPage {
    pub users: Vec<UserDirectoryEntry>,
    pub next_page_token: String,
}

pub async fn list_users(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<ListUsersQuery>,
) -> Result<Json<UserDirectoryPage>, ApiError> {
    let tenant_claim = principal
        .tenant_id
        .as_deref()
        .and_then(|value| Uuid::parse_str(value).ok());
    if tenant_claim != Some(query.tenant_id)
        || !principal
            .capabilities
            .iter()
            .any(|value| value == "identity.user.read")
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: "identity_user_directory".into(),
            reason: "principal is not authorized for this tenant user directory".into(),
        }
        .into());
    }
    let limit = query.limit.unwrap_or(50);
    if !(1..=100).contains(&limit) {
        return Err(AuthzError::InvalidRequest {
            reason: "limit must be between 1 and 100".into(),
        }
        .into());
    }
    let rows = sqlx::query(
        r#"SELECT u.id, u.username, u.external_id, u.is_active, u.query_revoked,
                  u.attributes_version, u.version, u.created_at, u.updated_at,
                  COALESCE(array_agg(r.code ORDER BY r.code) FILTER (WHERE r.code IS NOT NULL), ARRAY[]::text[]) AS roles
           FROM user_account u
           LEFT JOIN user_role ur ON ur.user_id = u.id AND ur.tenant_id = u.tenant_id
                AND (ur.expires_at IS NULL OR ur.expires_at > now())
           LEFT JOIN role r ON r.id = ur.role_id AND r.tenant_id = u.tenant_id
           WHERE u.tenant_id = $1 AND NOT u.is_deleted AND ($2::uuid IS NULL OR u.id > $2)
           GROUP BY u.id
           ORDER BY u.id
           LIMIT $3"#,
    )
    .bind(query.tenant_id)
    .bind(query.after_user_id)
    .bind(limit + 1)
    .fetch_all(&state.pool)
    .await?;

    let mut users = Vec::with_capacity(rows.len().min(limit as usize));
    for row in rows {
        users.push(UserDirectoryEntry {
            user_id: row.try_get("id")?,
            username: row.try_get("username")?,
            external_id: row.try_get("external_id")?,
            is_active: row.try_get("is_active")?,
            query_revoked: row.try_get("query_revoked")?,
            attributes_version: row.try_get("attributes_version")?,
            version: row.try_get("version")?,
            created_at_epoch_ms: row
                .try_get::<chrono::DateTime<chrono::Utc>, _>("created_at")?
                .timestamp_millis()
                .max(0) as u64,
            updated_at_epoch_ms: row
                .try_get::<chrono::DateTime<chrono::Utc>, _>("updated_at")?
                .timestamp_millis()
                .max(0) as u64,
            roles: row.try_get("roles")?,
        });
    }
    let has_more = users.len() > limit as usize;
    if has_more {
        users.truncate(limit as usize);
    }
    let next_page_token = if has_more {
        users
            .last()
            .map(|user| user.user_id.to_string())
            .unwrap_or_default()
    } else {
        String::new()
    };
    Ok(Json(UserDirectoryPage {
        users,
        next_page_token,
    }))
}
