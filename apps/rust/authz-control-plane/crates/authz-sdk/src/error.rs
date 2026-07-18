//! Unified SDK error type.

use thiserror::Error;

#[derive(Debug, Error)]
#[non_exhaustive]
pub enum SdkError {
    #[error("transport error: {0}")]
    Transport(#[from] reqwest::Error),

    #[error("PDP returned non-success: status={status} code={code} message={message}")]
    PdpError {
        status: u16,
        code: String,
        message: String,
    },

    #[error("decode error: {0}")]
    Decode(#[from] serde_json::Error),

    #[error("timeout after {0:?}")]
    Timeout(std::time::Duration),

    #[error("circuit breaker open for PDP")]
    CircuitOpen,

    #[error("invalid configuration: {0}")]
    Config(String),
}

impl SdkError {
    /// Machine-readable error code — useful for metrics labels.
    pub fn code(&self) -> &'static str {
        match self {
            SdkError::Transport(_) => "TRANSPORT",
            SdkError::PdpError { .. } => "PDP_ERROR",
            SdkError::Decode(_) => "DECODE",
            SdkError::Timeout(_) => "TIMEOUT",
            SdkError::CircuitOpen => "CIRCUIT_OPEN",
            SdkError::Config(_) => "CONFIG",
        }
    }
}
