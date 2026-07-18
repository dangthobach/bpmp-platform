//! API error types and `IntoResponse` implementation.
//!
//! All handler errors are converted to structured JSON via this type.
//! Internal details (DB errors) are never exposed to the API caller.

use authz_core::AuthzError;
use axum::{
    http::StatusCode,
    response::{IntoResponse, Response},
    Json,
};
use serde::{Deserialize, Serialize};

/// Structured API error response.
#[derive(Debug, Serialize, Deserialize)]
pub struct ApiErrorResponse {
    pub error_code: String,
    pub message: String,
    pub request_id: Option<String>,
}

/// Wrapper that converts `AuthzError` to an HTTP response.
pub struct ApiError {
    pub inner: AuthzError,
    pub request_id: Option<String>,
}

impl ApiError {
    pub fn new(err: AuthzError) -> Self {
        Self {
            inner: err,
            request_id: None,
        }
    }

    pub fn with_request_id(mut self, id: impl Into<String>) -> Self {
        self.request_id = Some(id.into());
        self
    }
}

impl From<AuthzError> for ApiError {
    fn from(err: AuthzError) -> Self {
        Self::new(err)
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        let status = status_code_for(&self.inner);
        let code = self.inner.error_code().to_owned();

        // Never expose internal error details in the API response
        let message = if self.inner.is_safe_to_expose() {
            self.inner.to_string()
        } else {
            "An internal error occurred. Please contact support.".to_owned()
        };

        let body = ApiErrorResponse {
            error_code: code,
            message,
            request_id: self.request_id,
        };

        (status, Json(body)).into_response()
    }
}

fn status_code_for(err: &AuthzError) -> StatusCode {
    match err {
        AuthzError::TenantNotFound { .. } => StatusCode::NOT_FOUND,
        AuthzError::TenantInactive { .. } => StatusCode::FORBIDDEN,
        AuthzError::UserNotFound { .. } => StatusCode::NOT_FOUND,
        AuthzError::UserDeactivated { .. } => StatusCode::FORBIDDEN,
        AuthzError::VersionConflict { .. } => StatusCode::CONFLICT,
        AuthzError::InvalidToken { .. } => StatusCode::UNAUTHORIZED,
        AuthzError::MissingClaim { .. } => StatusCode::UNAUTHORIZED,
        AuthzError::EmergencyRevoked { .. } => StatusCode::FORBIDDEN,
        AuthzError::TemporalGateDenied { .. } => StatusCode::FORBIDDEN,
        AuthzError::PolicyDenied { .. } => StatusCode::FORBIDDEN,
        AuthzError::ReBacDenied => StatusCode::FORBIDDEN,
        AuthzError::InvalidRequest { .. } => StatusCode::BAD_REQUEST,
        AuthzError::RelationCycleDetected { .. } => StatusCode::CONFLICT,
        AuthzError::FanoutLimitExceeded { .. } => StatusCode::CONFLICT,
        AuthzError::EscapeHatchNotApproved => StatusCode::FORBIDDEN,
        _ => StatusCode::INTERNAL_SERVER_ERROR,
    }
}
