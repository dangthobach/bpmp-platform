//! Uniform API envelope used by every PEP built on this SDK.
//!
//! Wire shape:
//! ```json
//! {
//!   "data": T | null,
//!   "error_code": "string",
//!   "message": "string",
//!   "request_id": "uuid",
//!   "timestamp": 1715000000
//! }
//! ```
//!
//! ## Why a single envelope
//! * Clients write 1 happy-path parser and 1 error handler.
//! * Frontend can map `error_code` → i18n keys deterministically.
//! * Observability: `request_id` chains across PEP → PDP → DB easily.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EnvelopeResponse<T> {
    /// Domain payload. `None` on error responses.
    pub data: Option<T>,
    /// Machine-readable error code; `"OK"` on success.
    pub error_code: String,
    /// Human-readable message; empty on success.
    pub message: String,
    /// Echo of the per-request correlation id.
    pub request_id: String,
    /// Unix seconds (UTC) when the envelope was built.
    pub timestamp: i64,
}

impl<T> EnvelopeResponse<T> {
    pub fn ok(data: T, request_id: impl Into<String>) -> Self {
        Self {
            data: Some(data),
            error_code: "OK".to_owned(),
            message: String::new(),
            request_id: request_id.into(),
            timestamp: chrono::Utc::now().timestamp(),
        }
    }

    pub fn error(
        code: impl Into<String>,
        message: impl Into<String>,
        request_id: impl Into<String>,
    ) -> EnvelopeResponse<()> {
        EnvelopeResponse {
            data: None,
            error_code: code.into(),
            message: message.into(),
            request_id: request_id.into(),
            timestamp: chrono::Utc::now().timestamp(),
        }
    }
}
