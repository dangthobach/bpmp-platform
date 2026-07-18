//! Adapter from the application's [`AuthzPort`] to `authz-sdk`.

use std::sync::Arc;

use async_trait::async_trait;
use serde_json::json;

use authz_sdk::{AuthzClient, CheckRequest, Decision, FilterRequest};

use crate::application::errors::AppError;
use crate::application::ports::authz_port::{AuthzPort, ResourceRef, SqlFilter, Subject};

pub struct SdkAuthzAdapter {
    client: Arc<dyn AuthzClient>,
    /// Failure policy when PDP is unreachable. Default: DENY.
    fail_open: bool,
}

impl SdkAuthzAdapter {
    pub fn new(client: Arc<dyn AuthzClient>, fail_open: bool) -> Self {
        Self { client, fail_open }
    }
}

#[async_trait]
impl AuthzPort for SdkAuthzAdapter {
    #[tracing::instrument(skip_all, fields(action = %action, resource = %res.resource_type))]
    async fn authorize(
        &self,
        sub: &Subject,
        action: &str,
        res: &ResourceRef,
    ) -> Result<(), AppError> {
        let req = CheckRequest {
            tenant_id: sub.tenant_id,
            user_id: sub.user_id,
            resource_type: res.resource_type.clone(),
            resource_ref: res.resource_ref.clone(),
            action: action.to_owned(),
            user_attributes: sub.attributes.clone(),
            user_attributes_version: sub.attributes_version,
            resource_attributes: res.attributes.clone(),
            backend: None,
            include_trace: None,
        };
        match self.client.check(&req).await {
            Ok(r) if r.decision == Decision::Allow => Ok(()),
            Ok(r) => Err(AppError::Forbidden {
                reason: r.deny_reason.unwrap_or_else(|| "denied".to_owned()),
            }),
            Err(e) => {
                metrics::counter!("authz_app_pdp_errors_total", "code" => e.code()).increment(1);
                if self.fail_open {
                    tracing::warn!(error = %e, "PDP unreachable — fail-open");
                    Ok(())
                } else {
                    Err(AppError::AuthzUnavailable(e.to_string()))
                }
            }
        }
    }

    async fn filter(
        &self,
        sub: &Subject,
        resource_type: &str,
        action: &str,
        backend: &str,
    ) -> Result<SqlFilter, AppError> {
        let req = FilterRequest {
            tenant_id: sub.tenant_id,
            user_id: sub.user_id,
            resource_type: resource_type.to_owned(),
            action: action.to_owned(),
            backend: backend.to_owned(),
            user_attributes: sub.attributes.clone(),
            user_attributes_version: sub.attributes_version,
        };
        match self.client.filter(&req).await {
            Ok(r) if r.decision == Decision::Allow => Ok(SqlFilter { raw: r.filter }),
            Ok(_) => Ok(SqlFilter {
                raw: json!({"$deny": true}),
            }),
            Err(e) => Err(AppError::AuthzUnavailable(e.to_string())),
        }
    }
}
