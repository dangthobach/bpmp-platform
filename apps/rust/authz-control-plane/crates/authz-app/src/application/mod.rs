//! Use-cases (CQRS-lite): commands mutate aggregates, queries return read models.
//!
//! Application services depend only on traits in [`ports`] — infrastructure
//! provides the adapters. This keeps the layer testable without a DB or HTTP.

pub mod commands;
pub mod errors;
pub mod ports;
pub mod queries;

pub use errors::AppError;
