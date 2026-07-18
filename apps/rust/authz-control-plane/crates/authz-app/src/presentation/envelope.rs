//! Envelope wrapper reused for REST + gRPC.
//!
//! Re-exports [`authz_sdk::EnvelopeResponse`] so that downstream consumers
//! (TypeScript SDKs, Java gateways) only need to learn one shape.

use axum::{http::StatusCode, response::IntoResponse, Json};
use serde::Serialize;

pub use authz_sdk::EnvelopeResponse as Envelope;

/// Convenience wrapper that pairs an HTTP status with an envelope body.
pub struct EnvelopeReply<T: Serialize>(pub StatusCode, pub Envelope<T>);

impl<T: Serialize> IntoResponse for EnvelopeReply<T> {
    fn into_response(self) -> axum::response::Response {
        (self.0, Json(self.1)).into_response()
    }
}

pub fn ok<T: Serialize>(data: T, request_id: impl Into<String>) -> EnvelopeReply<T> {
    EnvelopeReply(StatusCode::OK, Envelope::ok(data, request_id))
}

pub fn created<T: Serialize>(data: T, request_id: impl Into<String>) -> EnvelopeReply<T> {
    EnvelopeReply(StatusCode::CREATED, Envelope::ok(data, request_id))
}
