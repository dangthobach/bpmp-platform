use authz_core::ids::{TenantId, UserId};
use authz_core::models::filter::FilterBackend;
use authz_core::AuthzError;
use authz_engine::context::{AuthzContext, EnvContext, ResourceContext};
use authz_engine::evaluator::pipeline::{AuthzEvaluationPipeline, AuthzRequest};
use std::sync::Arc;
use tonic::{Request, Response, Status};

use super::proto::authz_engine_server::AuthzEngine;
use super::proto::{
    CheckRequest, CheckResponse, ExplainRequest, ExplainResponse, FilterRequest, FilterResponse,
};
use serde_json::Value as JsonValue;
use uuid::Uuid;

pub struct AuthzServiceImpl {
    pub pipeline: Arc<AuthzEvaluationPipeline>,
    pub jwt_jwks_url: String,
    pub jwt_audience: String,
}

impl AuthzServiceImpl {
    async fn authenticate<T>(&self, request: &Request<T>) -> Result<(), Status> {
        let token = request
            .metadata()
            .get("authorization")
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.strip_prefix("Bearer "))
            .ok_or_else(|| Status::unauthenticated("missing bearer token"))?;

        crate::middleware::service_auth::verify_jwt(token, &self.jwt_jwks_url, &self.jwt_audience)
            .await
            .map_err(Status::unauthenticated)
    }
}

#[tonic::async_trait]
impl AuthzEngine for AuthzServiceImpl {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        self.authenticate(&request).await?;
        let req = request.into_inner();

        let context_json: JsonValue = if req.context.is_empty() {
            serde_json::json!({})
        } else {
            serde_json::from_str(&req.context)
                .map_err(|e| Status::invalid_argument(format!("Invalid context JSON: {}", e)))?
        };

        let tenant_id = TenantId(
            Uuid::parse_str(&req.tenant_id)
                .map_err(|_| Status::invalid_argument("Invalid tenant_id UUID"))?,
        );
        let user_id = UserId(
            Uuid::parse_str(&req.user_id)
                .map_err(|_| Status::invalid_argument("Invalid user_id UUID"))?,
        );

        let authz_context = AuthzContext {
            tenant_id,
            user_id,
            user_attributes: context_json
                .get("user")
                .cloned()
                .unwrap_or_else(|| serde_json::json!({})),
            user_attributes_version: 0,
            resource: ResourceContext {
                resource_type: req.resource_type.clone(),
                resource_ref: if req.resource_ref.is_empty() {
                    None
                } else {
                    Some(req.resource_ref.clone())
                },
                attributes: context_json
                    .get("resource")
                    .cloned()
                    .unwrap_or_else(|| serde_json::json!({})),
            },
            env: EnvContext::default(),
            backend: FilterBackend::Sql,
        };

        let authz_req = AuthzRequest {
            tenant_id,
            user_id,
            action: req.action.clone(),
            context: authz_context,
            include_trace: false,
        };

        let eval_result = self.pipeline.evaluate(&authz_req).await;

        let (decision, metadata) = match eval_result {
            Ok(result) => {
                let metadata = result
                    .deny_reason
                    .map(|r| serde_json::json!({"deny_reason": r}).to_string())
                    .unwrap_or_else(|| "{}".to_owned());
                (format!("{:?}", result.decision).to_uppercase(), metadata)
            }
            Err(e) => match e {
                AuthzError::TenantNotFound { .. } => (
                    "DENY".to_string(),
                    serde_json::json!({"error": "TENANT_NOT_FOUND"}).to_string(),
                ),
                _ => (
                    "DENY".to_string(),
                    serde_json::json!({"error": e.to_string()}).to_string(),
                ),
            },
        };

        Ok(Response::new(CheckResponse { decision, metadata }))
    }

    async fn filter(
        &self,
        request: Request<FilterRequest>,
    ) -> Result<Response<FilterResponse>, Status> {
        self.authenticate(&request).await?;
        let req = request.into_inner();

        let context_json: JsonValue = if req.context.is_empty() {
            serde_json::json!({})
        } else {
            serde_json::from_str(&req.context)
                .map_err(|e| Status::invalid_argument(format!("Invalid context JSON: {}", e)))?
        };

        let tenant_id = TenantId(
            Uuid::parse_str(&req.tenant_id)
                .map_err(|_| Status::invalid_argument("Invalid tenant_id UUID"))?,
        );
        let user_id = UserId(
            Uuid::parse_str(&req.user_id)
                .map_err(|_| Status::invalid_argument("Invalid user_id UUID"))?,
        );

        let backend = match req.backend.to_lowercase().as_str() {
            "sql" => FilterBackend::Sql,
            "elasticsearch" | "es" => FilterBackend::Elasticsearch,
            "mongodb" | "mongo" => FilterBackend::Mongodb,
            _ => {
                return Err(Status::invalid_argument(format!(
                    "Unsupported backend: {}",
                    req.backend
                )))
            }
        };

        let authz_context = AuthzContext {
            tenant_id,
            user_id,
            user_attributes: context_json
                .get("user")
                .cloned()
                .unwrap_or_else(|| serde_json::json!({})),
            user_attributes_version: 0,
            resource: ResourceContext {
                resource_type: req.resource_type.clone(),
                resource_ref: None,
                attributes: context_json
                    .get("resource")
                    .cloned()
                    .unwrap_or_else(|| serde_json::json!({})),
            },
            env: EnvContext::default(),
            backend,
        };

        let authz_req = AuthzRequest {
            tenant_id,
            user_id,
            action: req.action.clone(),
            context: authz_context,
            include_trace: false,
        };

        let eval_result = self.pipeline.evaluate(&authz_req).await;

        match eval_result {
            Ok(result) => {
                let filter_str = serde_json::to_string(&result.row_filter).unwrap_or_default();
                let allowed_fields_str =
                    serde_json::to_string(&result.field_filter).unwrap_or_default();
                let masked_fields_str = "{}".to_owned();

                Ok(Response::new(FilterResponse {
                    decision: format!("{:?}", result.decision).to_uppercase(),
                    filter: filter_str,
                    allowed_fields: allowed_fields_str,
                    masked_fields: masked_fields_str,
                }))
            }
            Err(_e) => Ok(Response::new(FilterResponse {
                decision: "DENY".to_string(),
                filter: "{}".to_string(),
                allowed_fields: "[]".to_string(),
                masked_fields: "{}".to_string(),
            })),
        }
    }

    async fn explain(
        &self,
        request: Request<ExplainRequest>,
    ) -> Result<Response<ExplainResponse>, Status> {
        self.authenticate(&request).await?;
        let req = request.into_inner();

        let context_json: JsonValue = if req.context.is_empty() {
            serde_json::json!({})
        } else {
            serde_json::from_str(&req.context)
                .map_err(|e| Status::invalid_argument(format!("Invalid context JSON: {}", e)))?
        };

        let tenant_id = TenantId(
            Uuid::parse_str(&req.tenant_id)
                .map_err(|_| Status::invalid_argument("Invalid tenant_id UUID"))?,
        );
        let user_id = UserId(
            Uuid::parse_str(&req.user_id)
                .map_err(|_| Status::invalid_argument("Invalid user_id UUID"))?,
        );

        let authz_context = AuthzContext {
            tenant_id,
            user_id,
            user_attributes: context_json
                .get("user")
                .cloned()
                .unwrap_or_else(|| serde_json::json!({})),
            user_attributes_version: 0,
            resource: ResourceContext {
                resource_type: req.resource_type.clone(),
                resource_ref: if req.resource_ref.is_empty() {
                    None
                } else {
                    Some(req.resource_ref.clone())
                },
                attributes: context_json
                    .get("resource")
                    .cloned()
                    .unwrap_or_else(|| serde_json::json!({})),
            },
            env: EnvContext::default(),
            backend: FilterBackend::Sql,
        };

        let authz_req = AuthzRequest {
            tenant_id,
            user_id,
            action: req.action.clone(),
            context: authz_context,
            include_trace: true, // Trace enabled for explain
        };

        let eval_result = self.pipeline.evaluate(&authz_req).await;

        let (decision, trace) = match eval_result {
            Ok(result) => {
                let trace_str = serde_json::to_string(&result.eval_trace).unwrap_or_default();
                (format!("{:?}", result.decision).to_uppercase(), trace_str)
            }
            Err(e) => (
                "DENY".to_string(),
                serde_json::json!({"error": e.to_string()}).to_string(),
            ),
        };

        Ok(Response::new(ExplainResponse { decision, trace }))
    }
}
