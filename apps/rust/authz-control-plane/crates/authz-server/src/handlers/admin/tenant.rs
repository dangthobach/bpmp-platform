use crate::{
    error::ApiError,
    middleware::{request_id::RequestId, service_auth::ServicePrincipal},
    state::AppState,
};
use authz_core::{
    ids::TenantId,
    models::tenant::{Tenant, TenantConfig},
    AuthzError,
};
use authz_db::repositories::{
    delete_tenant as db_delete_tenant, get_tenant_for_admin, insert_tenant, list_tenants_for_admin,
    update_tenant as db_update_tenant, update_tenant_status as db_update_tenant_status,
    CreateTenant, TenantMutationAudit, TenantStatus, UpdateTenant,
};
use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    Extension, Json,
};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Deserialize)]
pub struct CreateTenantRequest {
    pub code: String,
    pub name: String,
    #[serde(default)]
    pub config: TenantConfig,
}

#[derive(Deserialize)]
pub struct UpdateTenantRequest {
    pub code: Option<String>,
    pub name: Option<String>,
    pub config: Option<TenantConfig>,
    pub is_active: Option<bool>,
    pub expected_version: i64,
}

#[derive(Deserialize)]
pub struct ListTenantsQuery {
    pub after_code: Option<String>,
    pub limit: Option<u16>,
}

#[derive(Deserialize)]
pub struct DeleteTenantQuery {
    pub expected_version: i64,
}

#[derive(Serialize)]
pub struct TenantResponse {
    pub id: Uuid,
    pub code: String,
    pub name: String,
    pub is_active: bool,
    pub config: TenantConfig,
    pub version: i64,
    pub is_deleted: bool,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

impl From<Tenant> for TenantResponse {
    fn from(tenant: Tenant) -> Self {
        Self {
            id: tenant.id.into_uuid(),
            code: tenant.code,
            name: tenant.name,
            is_active: tenant.is_active,
            config: tenant.config,
            version: tenant.metadata.version,
            is_deleted: tenant.metadata.is_deleted,
            created_at: tenant.metadata.created_at,
            updated_at: tenant.metadata.updated_at,
        }
    }
}

pub async fn create_tenant(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Json(payload): Json<CreateTenantRequest>,
) -> Result<(StatusCode, Json<TenantResponse>), ApiError> {
    validate_code(&payload.code)?;
    validate_name(&payload.name)?;
    validate_config(&payload.config)?;
    let tenant = insert_tenant(
        &state.pool,
        CreateTenant {
            tenant_id: TenantId::new(),
            code: &payload.code,
            name: payload.name.trim(),
            config: &payload.config,
        },
        mutation_audit(&principal, &request_id),
    )
    .await?;
    Ok((StatusCode::CREATED, Json(tenant.into())))
}

pub async fn get_tenant(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
) -> Result<Json<TenantResponse>, ApiError> {
    Ok(Json(
        get_tenant_for_admin(&state.pool, TenantId(id))
            .await?
            .into(),
    ))
}

pub async fn list_tenants(
    State(state): State<AppState>,
    Query(query): Query<ListTenantsQuery>,
) -> Result<Json<Vec<TenantResponse>>, ApiError> {
    if let Some(after_code) = query.after_code.as_deref() {
        validate_code(after_code)?;
    }
    let limit = query.limit.unwrap_or(50).clamp(1, 200);
    let tenants =
        list_tenants_for_admin(&state.pool, query.after_code.as_deref(), i64::from(limit)).await?;
    Ok(Json(tenants.into_iter().map(Into::into).collect()))
}

pub async fn update_tenant(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Json(payload): Json<UpdateTenantRequest>,
) -> Result<Json<TenantResponse>, ApiError> {
    if let Some(code) = payload.code.as_deref() {
        validate_code(code)?;
    }
    if let Some(name) = payload.name.as_deref() {
        validate_name(name)?;
    }
    if let Some(config) = payload.config.as_ref() {
        validate_config(config)?;
    }
    let tenant = db_update_tenant(
        &state.pool,
        TenantId(id),
        UpdateTenant {
            code: payload.code.as_deref(),
            name: payload.name.as_deref().map(str::trim),
            config: payload.config.as_ref(),
            is_active: payload.is_active,
            expected_version: payload.expected_version,
        },
        mutation_audit(&principal, &request_id),
    )
    .await?;
    Ok(Json(tenant.into()))
}

#[derive(Deserialize)]
pub struct UpdateTenantStatusRequest {
    pub status: String,
    pub expected_version: i64,
}

#[derive(Serialize)]
pub struct VersionResponse {
    pub version: i64,
}

pub async fn update_tenant_status(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
    Json(payload): Json<UpdateTenantStatusRequest>,
) -> Result<Json<VersionResponse>, ApiError> {
    let status = match payload.status.as_str() {
        "ACTIVE" => TenantStatus::Active,
        "SUSPENDED" => TenantStatus::Suspended,
        _ => {
            return Err(invalid_request(
                "invalid status; expected ACTIVE or SUSPENDED",
            ));
        }
    };
    let version = db_update_tenant_status(
        &state.pool,
        TenantId(id),
        status,
        payload.expected_version,
        mutation_audit(&principal, &request_id),
    )
    .await?;
    Ok(Json(VersionResponse { version }))
}

pub async fn delete_tenant(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
    Query(query): Query<DeleteTenantQuery>,
    Extension(principal): Extension<ServicePrincipal>,
    Extension(request_id): Extension<RequestId>,
) -> Result<Json<VersionResponse>, ApiError> {
    let version = db_delete_tenant(
        &state.pool,
        TenantId(id),
        query.expected_version,
        mutation_audit(&principal, &request_id),
    )
    .await?;
    Ok(Json(VersionResponse { version }))
}

fn mutation_audit<'a>(
    principal: &'a ServicePrincipal,
    request_id: &'a RequestId,
) -> TenantMutationAudit<'a> {
    TenantMutationAudit {
        actor_ref: &principal.subject,
        request_id: &request_id.0,
    }
}

fn validate_code(code: &str) -> Result<(), ApiError> {
    if !(2..=50).contains(&code.len())
        || !code
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || b"-_".contains(&byte))
    {
        return Err(invalid_request(
            "tenant code must be 2-50 lowercase ASCII letters, digits, '-' or '_'",
        ));
    }
    Ok(())
}

fn validate_name(name: &str) -> Result<(), ApiError> {
    if name.trim().is_empty() || name.len() > 200 {
        return Err(invalid_request("tenant name must be 1-200 bytes"));
    }
    Ok(())
}

fn validate_config(config: &TenantConfig) -> Result<(), ApiError> {
    if config.rebac_max_depth == 0 || config.rebac_max_depth > 64 {
        return Err(invalid_request("rebac_max_depth must be between 1 and 64"));
    }
    Ok(())
}

fn invalid_request(reason: &str) -> ApiError {
    AuthzError::InvalidRequest {
        reason: reason.to_owned(),
    }
    .into()
}
