//! HTTP error mapping. Every `AppError` becomes an envelope with the
//! correct status, machine code, and safe message.

use authz_http_middleware::{problem_response, RequestMetadata};
use axum::{
    http::StatusCode,
    response::{IntoResponse, Response},
};

use crate::application::errors::AppError;

/// Concrete envelope body for error responses — `data` is always null.
#[derive(Debug)]
pub struct ApiError {
    pub status: u16,
    pub code: &'static str,
    pub message: String,
    pub request_id: String,
}

impl ApiError {
    pub fn from_app(err: AppError, request_id: impl Into<String>) -> Self {
        let code = err.code();
        let status = err.http_status();
        let message = match &err {
            AppError::Database(_) | AppError::Internal(_) => "internal error".to_owned(),
            other => other.to_string(),
        };
        Self {
            status,
            code,
            message,
            request_id: request_id.into(),
        }
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        let status = StatusCode::from_u16(self.status).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
        let metadata = RequestMetadata {
            correlation_id: self.request_id.clone(),
            trace_id: self.request_id.clone(),
            request_id: self.request_id,
        };
        problem_response(status, self.code, &self.message, &metadata, false)
    }
}

/// Helper alias so handlers can write `-> ApiResult<T>`.
pub type ApiResult<T> = Result<T, ApiError>;
