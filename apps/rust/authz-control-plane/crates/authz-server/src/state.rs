//! Shared application state injected into all Axum handlers.

use authz_engine::{
    cache::EmergencyRevokeCache,
    evaluator::{pipeline::AuthzEvaluationPipeline, rebac::ReBacEngine},
};
use bpmp_authz_contracts::Ed25519Signer;
use std::collections::HashMap;
use std::sync::Arc;

#[derive(Clone)]
pub struct PolicyBundleSigning {
    pub key_id: String,
    pub signer: Arc<Ed25519Signer>,
}

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
    pub owner_manifest_key_ids: HashMap<String, Vec<String>>,
    pub policy_bundle_signing: Option<PolicyBundleSigning>,
    pub policy_bundle_max_bytes: usize,
}
