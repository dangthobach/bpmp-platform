//! Inbound HTTP adapter (Axum). Marshals envelope DTOs, extracts identity,
//! wires routes. Contains NO business rules.

pub mod envelope;
pub mod error;
pub mod extractors;
pub mod handlers;
pub mod middleware;
pub mod router;

pub use envelope::Envelope;
pub use error::ApiError;
