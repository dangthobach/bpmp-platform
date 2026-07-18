//! Request ID middleware — injects `X-Request-ID` into every request and response.
//!
//! Enables correlation across logs, audit records, and API responses.

use axum::{
    body::Body,
    http::{HeaderName, HeaderValue, Request, Response},
    middleware::Next,
};
use std::str::FromStr;
use uuid::Uuid;

pub const REQUEST_ID_HEADER: &str = "x-request-id";

/// Middleware that injects a unique request ID into every request.
///
/// If the incoming request already has `X-Request-ID`, it is preserved.
/// Otherwise, a new UUID v4 is generated.
pub async fn inject_request_id(mut req: Request<Body>, next: Next) -> Response<Body> {
    let request_id = req
        .headers()
        .get(REQUEST_ID_HEADER)
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_owned())
        .unwrap_or_else(|| Uuid::new_v4().to_string());

    // Store in request extensions for handler access
    req.extensions_mut().insert(RequestId(request_id.clone()));

    // Propagate the span field
    let span = tracing::info_span!("request", request_id = %request_id);
    let _guard = span.enter();

    let mut response = next.run(req).await;

    // Echo the request ID back in the response header
    if let Ok(header_val) = HeaderValue::from_str(&request_id) {
        response
            .headers_mut()
            .insert(HeaderName::from_str(REQUEST_ID_HEADER).unwrap(), header_val);
    }

    response
}

/// Newtype wrapper for the request ID, stored in request extensions.
#[derive(Debug, Clone)]
pub struct RequestId(pub String);

pub struct RequestIdLayer;
