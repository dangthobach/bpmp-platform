//! Pure domain layer — no I/O, no framework, no async runtime imports.
//!
//! Aggregates own their invariants. Application services orchestrate
//! aggregates and ports; they MUST NOT recreate domain rules.

pub mod errors;
pub mod organization;

pub use errors::DomainError;
