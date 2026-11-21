//! Idempotent, audited provisioning of a tenant access profile linked to an
//! already verified external IdP subject. Credentials remain with the IdP.

use crate::{
    error::ApiError,
    middleware::{request_id::RequestId, service_auth::ServicePrincipal},
    state::AppState,
};
use authz_core::AuthzError;
use axum::{
    extract::{Extension, Path, State},
    http::{HeaderMap, StatusCode},
    Json,
};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::Row;
use uuid::Uuid;

#[derive(Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ProvisionProfileRequest {
    pub tenant_id: Uuid,
    pub username: String,
    pub issuer: String,
    pub subject: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ProvisionedProfile {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub username: String,
    pub active: bool,
    pub version: i64,
    pub attributes_version: i64,
    pub roles: Vec<String>,
}

pub async fn provision_profile(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    headers: HeaderMap,
    Json(request): Json<ProvisionProfileRequest>,
) -> Result<(StatusCode, Json<ProvisionedProfile>), ApiError> {
    if !principal
        .capabilities
        .iter()
        .any(|capability| capability == "identity.profile.manage")
        || principal.tenant_id.as_deref() != Some(request.tenant_id.to_string().as_str())
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: "identity_profile_manage".into(),
            reason: "principal cannot manage identity profiles in this tenant".into(),
        }
        .into());
    }
    validate_request(&request)?;

    let idempotency_key = headers
        .get("Idempotency-Key")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty() && value.len() <= 200)
        .ok_or_else(|| invalid_request("Idempotency-Key is required and limited to 200 bytes"))?;
    let idempotency_key_hash = Sha256::digest(idempotency_key.as_bytes()).to_vec();
    let request_hash =
        Sha256::digest(serde_json::to_vec(&request).map_err(AuthzError::from)?).to_vec();
    let actor_ref = principal
        .actor_id
        .as_deref()
        .unwrap_or(principal.subject.as_str());
    let executor_ref = principal.subject.as_str();
    let actor_id = Uuid::parse_str(actor_ref).ok();

    let mut tx = state.pool.begin().await?;
    sqlx::query(
        "DELETE FROM identity_profile_idempotency WHERE tenant_id=$1 AND actor_ref=$2 AND operation='PROVISION_LINK' AND idempotency_key_hash=$3 AND expires_at <= now()",
    )
    .bind(request.tenant_id)
    .bind(actor_ref)
    .bind(&idempotency_key_hash)
    .execute(&mut *tx)
    .await?;

    let inserted = sqlx::query(
        r#"INSERT INTO identity_profile_idempotency
             (tenant_id,actor_ref,operation,idempotency_key_hash,request_hash)
           VALUES ($1,$2,'PROVISION_LINK',$3,$4)
           ON CONFLICT DO NOTHING"#,
    )
    .bind(request.tenant_id)
    .bind(actor_ref)
    .bind(&idempotency_key_hash)
    .bind(&request_hash)
    .execute(&mut *tx)
    .await?
    .rows_affected()
        == 1;

    if !inserted {
        let receipt = sqlx::query(
            r#"SELECT request_hash,response_body FROM identity_profile_idempotency
               WHERE tenant_id=$1 AND actor_ref=$2 AND operation='PROVISION_LINK'
                 AND idempotency_key_hash=$3 FOR UPDATE"#,
        )
        .bind(request.tenant_id)
        .bind(actor_ref)
        .bind(&idempotency_key_hash)
        .fetch_one(&mut *tx)
        .await?;
        let prior_hash: Vec<u8> = receipt.try_get("request_hash")?;
        if prior_hash != request_hash {
            return Err(AuthzError::IdempotencyConflict.into());
        }
        let body: Option<serde_json::Value> = receipt.try_get("response_body")?;
        let Some(body) = body else {
            return Err(AuthzError::Internal(
                "committed identity idempotency receipt has no response".into(),
            )
            .into());
        };
        let profile = serde_json::from_value(body).map_err(AuthzError::from)?;
        tx.commit().await?;
        return Ok((StatusCode::CREATED, Json(profile)));
    }

    let tenant_active = sqlx::query_scalar::<_, bool>(
        "SELECT is_active FROM tenant WHERE id=$1 AND NOT is_deleted FOR SHARE",
    )
    .bind(request.tenant_id)
    .fetch_optional(&mut *tx)
    .await?;
    match tenant_active {
        None => {
            return Err(AuthzError::TenantNotFound {
                tenant_id: request.tenant_id,
            }
            .into())
        }
        Some(false) => {
            return Err(AuthzError::TenantInactive {
                tenant_id: request.tenant_id,
            }
            .into())
        }
        Some(true) => {}
    }

    let user_id = Uuid::new_v4();
    let inserted_profile = sqlx::query(
        r#"INSERT INTO user_account
             (id,tenant_id,username,idp_issuer,idp_subject,is_active,created_by,updated_by)
           VALUES ($1,$2,$3,$4,$5,true,$6,$6)
           RETURNING version,attributes_version"#,
    )
    .bind(user_id)
    .bind(request.tenant_id)
    .bind(request.username.trim())
    .bind(&request.issuer)
    .bind(&request.subject)
    .bind(actor_id)
    .fetch_one(&mut *tx)
    .await;
    let row = match inserted_profile {
        Ok(row) => row,
        Err(sqlx::Error::Database(error)) if error.code().as_deref() == Some("23505") => {
            return Err(AuthzError::IdentityProfileConflict.into());
        }
        Err(error) => return Err(error.into()),
    };
    let version: i64 = row.try_get("version")?;
    let attributes_version: i64 = row.try_get("attributes_version")?;
    let profile = ProvisionedProfile {
        tenant_id: request.tenant_id,
        user_id,
        username: request.username.trim().to_owned(),
        active: true,
        version,
        attributes_version,
        roles: Vec::new(),
    };

    let identity_fingerprint =
        Sha256::digest([request.issuer.as_bytes(), &[0], request.subject.as_bytes()].concat())
            .to_vec();
    sqlx::query(
        r#"INSERT INTO identity_profile_mutation_audit
             (tenant_id,user_id,operation,entity_version,actor_ref,request_id,
              executor_ref,idempotency_key_hash,identity_fingerprint)
           VALUES ($1,$2,'PROVISION_LINK',$3,$4,$5,$6,$7,$8)"#,
    )
    .bind(request.tenant_id)
    .bind(user_id)
    .bind(version)
    .bind(actor_ref)
    .bind(&request_id.0)
    .bind(executor_ref)
    .bind(&idempotency_key_hash)
    .bind(identity_fingerprint)
    .execute(&mut *tx)
    .await?;

    sqlx::query(
        "UPDATE identity_profile_idempotency SET response_body=$5 WHERE tenant_id=$1 AND actor_ref=$2 AND operation='PROVISION_LINK' AND idempotency_key_hash=$3 AND request_hash=$4",
    )
    .bind(request.tenant_id)
    .bind(actor_ref)
    .bind(&idempotency_key_hash)
    .bind(&request_hash)
    .bind(json!(profile))
    .execute(&mut *tx)
    .await?;

    tx.commit().await?;
    Ok((StatusCode::CREATED, Json(profile)))
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct UpdateProfileRequest {
    pub tenant_id: Uuid,
    pub username: String,
    pub issuer: String,
    pub subject: String,
    pub active: bool,
    pub expected_version: i64,
}

pub async fn update_profile(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Path(user_id): axum::extract::Path<Uuid>,
    headers: HeaderMap,
    Json(request): Json<UpdateProfileRequest>,
) -> Result<Json<ProvisionedProfile>, ApiError> {
    if !principal.capabilities.iter().any(|capability| capability == "identity.profile.manage")
        || principal.tenant_id.as_deref() != Some(request.tenant_id.to_string().as_str())
    {
        return Err(AuthzError::PolicyDenied { policy_name: "identity_profile_manage".into(), reason: "principal cannot manage identity profiles in this tenant".into() }.into());
    }
    if request.expected_version < 0 {
        return Err(invalid_request("expected_version must be non-negative"));
    }
    validate_request(&ProvisionProfileRequest { tenant_id: request.tenant_id, username: request.username.clone(), issuer: request.issuer.clone(), subject: request.subject.clone() })?;
    let idempotency_key = headers.get("Idempotency-Key").and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty() && value.len() <= 200)
        .ok_or_else(|| invalid_request("Idempotency-Key is required and limited to 200 bytes"))?;
    let key_hash = Sha256::digest(idempotency_key.as_bytes()).to_vec();
    let request_hash = Sha256::digest(serde_json::to_vec(&(user_id, &request)).map_err(AuthzError::from)?).to_vec();
    let actor_ref = principal.actor_id.as_deref().unwrap_or(&principal.subject);
    let actor_id = Uuid::parse_str(actor_ref).ok();
    let mut tx = state.pool.begin().await?;
    sqlx::query("DELETE FROM identity_profile_idempotency WHERE tenant_id=$1 AND actor_ref=$2 AND operation='UPDATE_PROFILE' AND idempotency_key_hash=$3 AND expires_at<=now()")
        .bind(request.tenant_id).bind(actor_ref).bind(&key_hash).execute(&mut *tx).await?;
    let inserted = sqlx::query("INSERT INTO identity_profile_idempotency (tenant_id,actor_ref,operation,idempotency_key_hash,request_hash) VALUES ($1,$2,'UPDATE_PROFILE',$3,$4) ON CONFLICT DO NOTHING")
        .bind(request.tenant_id).bind(actor_ref).bind(&key_hash).bind(&request_hash).execute(&mut *tx).await?.rows_affected() == 1;
    if !inserted {
        let receipt = sqlx::query("SELECT request_hash,response_body FROM identity_profile_idempotency WHERE tenant_id=$1 AND actor_ref=$2 AND operation='UPDATE_PROFILE' AND idempotency_key_hash=$3 FOR UPDATE")
            .bind(request.tenant_id).bind(actor_ref).bind(&key_hash).fetch_one(&mut *tx).await?;
        let prior: Vec<u8> = receipt.try_get("request_hash")?;
        if prior != request_hash { return Err(AuthzError::IdempotencyConflict.into()); }
        let body: Option<serde_json::Value> = receipt.try_get("response_body")?;
        let Some(body) = body else { return Err(AuthzError::Internal("identity profile idempotency receipt has no response".into()).into()); };
        let profile = serde_json::from_value(body).map_err(AuthzError::from)?;
        tx.commit().await?;
        return Ok(Json(profile));
    }
    let current = sqlx::query("SELECT version FROM user_account WHERE id=$1 AND tenant_id=$2 AND NOT is_deleted FOR UPDATE")
        .bind(user_id).bind(request.tenant_id).fetch_optional(&mut *tx).await?;
    let Some(current) = current else { return Err(AuthzError::UserNotFound { user_id, tenant_id: request.tenant_id }.into()); };
    let actual: i64 = current.try_get("version")?;
    if actual != request.expected_version {
        return Err(AuthzError::VersionConflict { entity: "user access profile", entity_id: user_id, expected_version: request.expected_version, actual_version: actual }.into());
    }
    let updated = sqlx::query("UPDATE user_account SET username=$1,idp_issuer=$2,idp_subject=$3,is_active=$4,updated_by=$5,attributes_version=attributes_version+1 WHERE id=$6 AND tenant_id=$7 RETURNING version,attributes_version")
        .bind(request.username.trim()).bind(&request.issuer).bind(&request.subject).bind(request.active).bind(actor_id).bind(user_id).bind(request.tenant_id).fetch_one(&mut *tx).await;
    let updated = match updated { Ok(row) => row, Err(sqlx::Error::Database(error)) if error.code().as_deref()==Some("23505") => return Err(AuthzError::IdentityProfileConflict.into()), Err(error) => return Err(error.into()) };
    let roles: Vec<String> = sqlx::query_scalar("SELECT r.code FROM user_role ur JOIN role r ON r.id=ur.role_id AND r.tenant_id=ur.tenant_id WHERE ur.user_id=$1 AND ur.tenant_id=$2 AND (ur.expires_at IS NULL OR ur.expires_at>now()) ORDER BY r.code")
        .bind(user_id).bind(request.tenant_id).fetch_all(&mut *tx).await?;
    let profile = ProvisionedProfile { tenant_id: request.tenant_id, user_id, username: request.username.trim().to_owned(), active: request.active, version: updated.try_get("version")?, attributes_version: updated.try_get("attributes_version")?, roles };
    let fingerprint = Sha256::digest([request.issuer.as_bytes(), &[0], request.subject.as_bytes()].concat()).to_vec();
    sqlx::query("INSERT INTO identity_profile_mutation_audit (tenant_id,user_id,operation,entity_version,actor_ref,executor_ref,request_id,idempotency_key_hash,identity_fingerprint) VALUES ($1,$2,'UPDATE_PROFILE',$3,$4,$5,$6,$7,$8)")
        .bind(request.tenant_id).bind(user_id).bind(profile.version).bind(actor_ref).bind(&principal.subject).bind(&request_id.0).bind(&key_hash).bind(fingerprint).execute(&mut *tx).await?;
    sqlx::query("UPDATE identity_profile_idempotency SET response_body=$4 WHERE tenant_id=$1 AND actor_ref=$2 AND operation='UPDATE_PROFILE' AND idempotency_key_hash=$3")
        .bind(request.tenant_id).bind(actor_ref).bind(&key_hash).bind(serde_json::to_value(&profile).map_err(AuthzError::from)?).execute(&mut *tx).await?;
    tx.commit().await?;
    Ok(Json(profile))
}

fn validate_request(request: &ProvisionProfileRequest) -> Result<(), ApiError> {
    let username = request.username.trim();
    if username.is_empty() || username.len() > 100 {
        return Err(invalid_request("username must be between 1 and 100 bytes"));
    }
    if request.issuer.is_empty()
        || request.issuer.len() > 512
        || request
            .issuer
            .bytes()
            .any(|byte| byte.is_ascii_whitespace())
    {
        return Err(invalid_request(
            "issuer must be an exact non-empty OIDC issuer URL",
        ));
    }
    if request.subject.trim().is_empty() || request.subject.len() > 255 {
        return Err(invalid_request("subject must be between 1 and 255 bytes"));
    }
    Ok(())
}

fn invalid_request(reason: &str) -> ApiError {
    AuthzError::InvalidRequest {
        reason: reason.to_owned(),
    }
    .into()
}
