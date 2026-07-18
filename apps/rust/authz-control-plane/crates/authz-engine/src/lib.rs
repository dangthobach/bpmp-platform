//! `authz-engine` — The authorization policy evaluation engine.
//!
//! This crate contains the core business logic:
//! - ABAC AST evaluator with node-by-node tracing
//! - ReBAC graph engine with circuit breaker and depth limit
//! - Temporal gate evaluator
//! - Multi-backend filter translators (SQL, Elasticsearch, MongoDB)
//! - Policy evaluation pipeline (5-layer orchestrator)
//! - In-memory policy bundle cache with atomic hot-swap
//! - Shadow mode parallel evaluation

pub mod algorithms;
pub mod cache;
pub mod context;
pub mod evaluator;
pub mod filter;
pub mod shadow;

pub use context::{AuthzContext, EnvContext, ResourceContext};
pub use evaluator::pipeline::{AuthzEvaluationPipeline, AuthzRequest, AuthzResponse};
