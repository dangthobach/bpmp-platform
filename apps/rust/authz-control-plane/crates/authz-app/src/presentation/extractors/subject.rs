//! Extractor that yields the validated [`Subject`] populated by middleware.

use axum::{async_trait, extract::FromRequestParts, http::request::Parts};

use crate::application::ports::authz_port::Subject;
use crate::presentation::error::ApiError;
use crate::presentation::middleware::identity::AuthenticatedSubject;

pub struct SubjectExt(pub Subject);

#[async_trait]
impl<S> FromRequestParts<S> for SubjectExt
where
    S: Send + Sync,
{
    type Rejection = ApiError;

    async fn from_request_parts(parts: &mut Parts, _state: &S) -> Result<Self, Self::Rejection> {
        let request_id = parts
            .extensions
            .get::<crate::presentation::middleware::request_id::RequestId>()
            .map(|r| r.0.clone())
            .unwrap_or_default();
        parts
            .extensions
            .get::<AuthenticatedSubject>()
            .cloned()
            .map(|s| SubjectExt(s.0))
            .ok_or_else(|| ApiError {
                status: 401,
                code: "UNAUTHORIZED",
                message: "subject not resolved".to_owned(),
                request_id,
            })
    }
}
