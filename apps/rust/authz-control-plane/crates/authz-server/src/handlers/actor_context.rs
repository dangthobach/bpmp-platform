//! Issues a short-lived actor proof from current tenant membership state.
//! The Gateway supplies the actor identity from its authenticated session, but
//! cannot choose roles or capabilities; those are read from Authz here.

use crate::{
    error::ApiError,
    middleware::service_auth::ServicePrincipal,
    state::{ActorContextSigning, AppState},
};
use authz_core::AuthzError;
use axum::{
    extract::{Extension, State},
    Json,
};
use base64::{engine::general_purpose::STANDARD, Engine as _};
use bpmp_authz_contracts::{
    authorization::v1::SignedActorContext, ActorProofCodec, AUTHORIZATION_PROOF_SCHEMA_VERSION,
};
use serde::{Deserialize, Serialize};
use sqlx::Row;
use uuid::Uuid;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct IssueActorContextRequest {
    pub tenant_id: Uuid,
    pub audience_workload_id: String,
    pub command_id: String,
}

#[derive(Debug, Serialize)]
pub struct IssueActorContextResponse {
    pub tenant_id: Uuid,
    pub actor_id: Uuid,
    pub audience_workload_id: String,
    pub command_id: String,
    pub issued_at_epoch_ms: u64,
    pub expires_at_epoch_ms: u64,
    pub roles: Vec<String>,
    pub capabilities: Vec<String>,
    pub signed_actor_context: String,
}

pub async fn issue(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Json(request): Json<IssueActorContextRequest>,
) -> Result<Json<IssueActorContextResponse>, ApiError> {
    let signing = state
        .actor_context_signing
        .as_ref()
        .ok_or(AuthzError::ActorContextIssuerUnavailable)?;
    let actor_id = principal
        .actor_id
        .as_deref()
        .and_then(|value| Uuid::parse_str(value).ok());
    if principal.tenant_id.as_deref() != Some(request.tenant_id.to_string().as_str())
        || actor_id.is_none()
        || !principal
            .capabilities
            .iter()
            .any(|capability| capability == "identity.actor-context.issue")
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: "identity_actor_context_issue".into(),
            reason: "service principal cannot issue actor contexts for this tenant".into(),
        }
        .into());
    }
    validate_scope(signing, &request)?;
    let Some(actor_id) = actor_id else {
        return Err(AuthzError::PolicyDenied {
            policy_name: "identity_actor_context_issue".into(),
            reason: "service principal has no valid actor identity".into(),
        }
        .into());
    };

    let row = sqlx::query(
        r#"SELECT u.query_revoked, u.query_revoke_epoch,
                  COALESCE(role_codes.codes, ARRAY[]::text[]) AS roles,
                  COALESCE(capability_codes.codes, ARRAY[]::text[]) AS capabilities
           FROM user_account u
           JOIN tenant t ON t.id=u.tenant_id AND t.is_active AND NOT t.is_deleted
           LEFT JOIN LATERAL (
               SELECT array_agg(selected.code ORDER BY selected.code) AS codes
               FROM (
                   SELECT DISTINCT r.code
                   FROM user_role ur
                   JOIN role r ON r.id=ur.role_id AND r.tenant_id=u.tenant_id
                       AND NOT r.is_deleted
                   WHERE ur.user_id=u.id AND ur.tenant_id=u.tenant_id
                       AND ur.resource_scope_id IS NULL
                       AND (ur.expires_at IS NULL OR ur.expires_at > now())
                   ORDER BY r.code
                   LIMIT $3
               ) selected
           ) role_codes ON TRUE
           LEFT JOIN LATERAL (
               SELECT array_agg(selected.code ORDER BY selected.code) AS codes
               FROM (
                   SELECT DISTINCT p.code
                   FROM user_role ur
                   JOIN role r ON r.id=ur.role_id AND r.tenant_id=u.tenant_id
                       AND NOT r.is_deleted
                   JOIN role_permission rp ON rp.role_id=r.id
                   JOIN permission p ON p.id=rp.permission_id
                       AND p.tenant_id=u.tenant_id AND NOT p.is_deleted
                   WHERE ur.user_id=u.id AND ur.tenant_id=u.tenant_id
                       AND ur.resource_scope_id IS NULL
                       AND (ur.expires_at IS NULL OR ur.expires_at > now())
                   ORDER BY p.code
                   LIMIT $4
               ) selected
           ) capability_codes ON TRUE
           WHERE u.id=$1 AND u.tenant_id=$2 AND u.is_active AND NOT u.is_deleted"#,
    )
    .bind(actor_id)
    .bind(request.tenant_id)
    .bind(signing.max_roles.saturating_add(1) as i64)
    .bind(signing.max_capabilities.saturating_add(1) as i64)
    .fetch_optional(&state.pool)
    .await?
    .ok_or(AuthzError::UserNotFound {
        user_id: actor_id,
        tenant_id: request.tenant_id,
    })?;

    let query_revoked: bool = row.try_get("query_revoked")?;
    let query_revoke_epoch: i64 = row.try_get("query_revoke_epoch")?;
    let query_audience = request.audience_workload_id == "projection-service";
    if query_audience && query_revoked {
        return Err(AuthzError::PolicyDenied {
            policy_name: "identity_actor_context_issue".into(),
            reason: "actor query access is revoked".into(),
        }
        .into());
    }
    // Query revoke epochs belong to Projection's read authorization domain.
    // Do not reuse them as the Engine/Human Runtime identity revoke epoch.
    let revoke_epoch = if query_audience {
        u64::try_from(query_revoke_epoch).map_err(|_| {
            AuthzError::Internal("negative actor query revoke epoch in Authz database".into())
        })?
    } else {
        0
    };
    let roles: Vec<String> = row.try_get("roles")?;
    let capabilities: Vec<String> = row.try_get("capabilities")?;
    if roles.len() > signing.max_roles || capabilities.len() > signing.max_capabilities {
        return Err(AuthzError::ActorContextIssuerUnavailable.into());
    }

    let issued_at_epoch_ms = u64::try_from(chrono::Utc::now().timestamp_millis())
        .map_err(|_| AuthzError::Internal("system clock is before Unix epoch".into()))?;
    let expires_at_epoch_ms = issued_at_epoch_ms
        .checked_add(signing.ttl_ms)
        .ok_or_else(|| AuthzError::Internal("actor proof expiry overflow".into()))?;
    let proof = SignedActorContext {
        schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
        tenant_id: request.tenant_id.to_string(),
        actor_id: actor_id.to_string(),
        roles: roles.clone(),
        capabilities: capabilities.clone(),
        revoke_epoch,
        issued_at_epoch_ms,
        expires_at_epoch_ms,
        audience_workload_id: request.audience_workload_id.clone(),
        command_id: request.command_id.clone(),
        signing_key_id: String::new(),
        content_hash: Vec::new(),
        signature: Vec::new(),
    };
    let signed_actor_context = ActorProofCodec::seal(
        proof,
        &signing.key_id,
        signing.signer.as_ref(),
        signing.proof_limits,
    )
    .map_err(|error| AuthzError::Internal(format!("actor context signing failed: {error}")))?;

    Ok(Json(IssueActorContextResponse {
        tenant_id: request.tenant_id,
        actor_id,
        audience_workload_id: request.audience_workload_id,
        command_id: request.command_id,
        issued_at_epoch_ms,
        expires_at_epoch_ms,
        roles,
        capabilities,
        signed_actor_context: STANDARD.encode(signed_actor_context),
    }))
}

fn validate_scope(
    signing: &ActorContextSigning,
    request: &IssueActorContextRequest,
) -> Result<(), ApiError> {
    if !signing
        .audiences
        .contains(request.audience_workload_id.as_str())
    {
        return Err(invalid_request(
            "audience workload is not in the Authz allowlist",
        ));
    }
    if !valid_scope_token(&request.audience_workload_id) || !valid_scope_token(&request.command_id)
    {
        return Err(invalid_request(
            "audience workload and command ID must use 1-128 ASCII identifier characters",
        ));
    }
    Ok(())
}

fn valid_scope_token(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b':'))
}

fn invalid_request(reason: &str) -> ApiError {
    AuthzError::InvalidRequest {
        reason: reason.to_owned(),
    }
    .into()
}

#[cfg(test)]
mod tests {
    use super::valid_scope_token;

    #[test]
    fn scope_tokens_are_bounded_ascii_identifiers() {
        assert!(valid_scope_token("human-runtime"));
        assert!(valid_scope_token("command.01:retry"));
        assert!(!valid_scope_token(""));
        assert!(!valid_scope_token("contains whitespace"));
        assert!(!valid_scope_token("line\nbreak"));
        assert!(!valid_scope_token("workload/other"));
        assert!(!valid_scope_token(&"x".repeat(129)));
    }
}
