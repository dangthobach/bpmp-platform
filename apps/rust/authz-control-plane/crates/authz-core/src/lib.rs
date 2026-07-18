//! `authz-core` — Domain models, types, and error definitions.
//!
//! This crate is the foundation of the AuthZ platform, containing:
//! - Typed ID newtypes for all domain entities
//! - Domain model structs (tenant, RBAC, resource, policy, ReBAC, filter, audit)
//! - JSON AST types for ABAC conditions and row filters
//! - Unified [`AuthzError`] error type
//!
//! No I/O, no async — pure domain logic only.

pub mod errors;
pub mod ids;
pub mod models;

pub use errors::AuthzError;
pub use ids::*;
