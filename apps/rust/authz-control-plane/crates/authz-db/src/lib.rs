//! `authz-db` — Database access layer using sqlx.
//!
//! Provides:
//! - Generic [`Repository<T>`] trait with type-safe CRUD operations
//! - Concrete sqlx implementations for all domain entities
//! - Migration runner

pub mod pool;
pub mod repositories;

pub use pool::{create_pool, run_migrations, DbPool, DbPoolConfig};
pub use repositories::*;
