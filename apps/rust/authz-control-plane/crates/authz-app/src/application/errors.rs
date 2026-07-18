//! Application-level error type. Maps to HTTP at the presentation boundary.

use thiserror::Error;

use crate::domain::DomainError;

#[derive(Debug, Error)]
#[non_exhaustive]
pub enum AppError {
    #[error("forbidden: {reason}")]
    Forbidden { reason: String },

    #[error("unauthorized: {reason}")]
    Unauthorized { reason: String },

    #[error("not found: {resource}")]
    NotFound { resource: String },

    #[error("conflict: {0}")]
    Conflict(String),

    #[error("bad request: {0}")]
    BadRequest(String),

    #[error("domain rule violated: {0}")]
    Domain(#[from] DomainError),

    #[error("authz service unavailable: {0}")]
    AuthzUnavailable(String),

    #[error("database error: {0}")]
    Database(String),

    #[error("internal error: {0}")]
    Internal(String),
}

impl AppError {
    /// Stable code for API envelope + metrics.
    pub fn code(&self) -> &'static str {
        match self {
            AppError::Forbidden { .. } => "FORBIDDEN",
            AppError::Unauthorized { .. } => "UNAUTHORIZED",
            AppError::NotFound { .. } => "NOT_FOUND",
            AppError::Conflict(_) => "CONFLICT",
            AppError::BadRequest(_) => "BAD_REQUEST",
            AppError::Domain(_) => "DOMAIN_RULE",
            AppError::AuthzUnavailable(_) => "AUTHZ_UNAVAILABLE",
            AppError::Database(_) => "DATABASE_ERROR",
            AppError::Internal(_) => "INTERNAL_ERROR",
        }
    }

    pub fn http_status(&self) -> u16 {
        match self {
            AppError::Forbidden { .. } => 403,
            AppError::Unauthorized { .. } => 401,
            AppError::NotFound { .. } => 404,
            AppError::Conflict(_) => 409,
            AppError::BadRequest(_) | AppError::Domain(_) => 400,
            AppError::AuthzUnavailable(_) => 503,
            AppError::Database(_) | AppError::Internal(_) => 500,
        }
    }
}

impl From<sqlx::Error> for AppError {
    fn from(e: sqlx::Error) -> Self {
        AppError::Database(e.to_string())
    }
}
