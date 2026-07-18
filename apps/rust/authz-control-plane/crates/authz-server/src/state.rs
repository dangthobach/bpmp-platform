//! Shared application state injected into all Axum handlers.

use authz_engine::{
    cache::EmergencyRevokeCache,
    evaluator::{pipeline::AuthzEvaluationPipeline, rebac::ReBacEngine},
};
use std::sync::Arc;

/// Application state shared via Axum's `State` extractor.
///
/// All fields are `Arc<T>` — cheap to clone across handlers.
#[derive(Clone)]
pub struct AppState {
    pub pool: sqlx::PgPool,
    pub pipeline: Arc<AuthzEvaluationPipeline>,
    pub emergency_revoke: Arc<EmergencyRevokeCache>,
    pub rebac_engine: Arc<ReBacEngine>,
    pub jwt_jwks_url: String,
    pub jwt_audience: String,
}
