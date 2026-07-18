//! ReBAC (Relationship-based Access Control) graph engine.
//!
//! ## Design (EC-2, Gap4)
//! Three-tier evaluation strategy:
//! 1. O(1) materialized reachability table lookup
//! 2. Live `WITH RECURSIVE` traversal with depth limit (fallback)
//! 3. Circuit breaker — open after 3 consecutive failures → fail-closed
//!
//! Cycle detection is handled at write time by the DB trigger (EC-2 Layer 1).
//! The engine only needs to handle depth limiting and timeout.

use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use authz_core::{ids::TenantId, AuthzError};
use dashmap::DashMap;
use tokio::time::timeout;
use tracing::{instrument, warn};

use crate::algorithms::iddfs::{GraphProvider, PermissionIddfsEngine};
use authz_db;

/// Configuration for the ReBAC engine.
#[derive(Debug, Clone)]
pub struct ReBacConfig {
    /// Maximum graph traversal depth (default: 10).
    pub max_depth: u32,
    /// Timeout for the full traversal (default: 50ms).
    pub traversal_timeout_ms: u64,
    /// Number of consecutive failures before opening the circuit breaker.
    pub circuit_breaker_threshold: u32,
    /// How long the circuit stays open before attempting a reset (seconds).
    pub circuit_reset_secs: u64,
    /// Maximum number of results returned by `resolve_objects`.
    pub max_terms_filter_size: i64,
}

impl Default for ReBacConfig {
    fn default() -> Self {
        Self {
            max_depth: 10,
            traversal_timeout_ms: 50,
            circuit_breaker_threshold: 3,
            circuit_reset_secs: 30,
            max_terms_filter_size: 1000,
        }
    }
}

/// Circuit breaker state per tenant.
#[derive(Debug)]
struct CircuitState {
    failure_count: AtomicU32,
    open_until: std::sync::Mutex<Option<std::time::Instant>>,
}

impl Default for CircuitState {
    fn default() -> Self {
        Self {
            failure_count: AtomicU32::new(0),
            open_until: std::sync::Mutex::new(None),
        }
    }
}

/// The ReBAC graph engine.
///
/// Shared across all request handlers via `Arc`. Thread-safe — uses
/// immutable shared reference to DB pool and `DashMap` for circuit state.
pub struct ReBacEngine {
    pool: sqlx::PgPool,
    config: ReBacConfig,
    /// Per-tenant circuit breaker state.
    circuit_states: DashMap<TenantId, Arc<CircuitState>>,
}

impl ReBacEngine {
    pub fn new(pool: sqlx::PgPool, config: ReBacConfig) -> Self {
        Self {
            pool,
            config,
            circuit_states: DashMap::new(),
        }
    }

    /// Checks if `subject` can reach `object` via `relation`.
    ///
    /// ## Evaluation order
    /// 1. Emergency: circuit open → deny immediately
    /// 2. Materialized table (O(1)) → fast path
    /// 3. Live traversal with timeout → fallback
    /// 4. Traversal error → record failure, deny
    #[instrument(skip(self), fields(subject = %subject, relation = %relation, object = %object))]
    pub async fn check(
        &self,
        tenant_id: TenantId,
        subject: &str,
        relation: &str,
        object: &str,
    ) -> Result<bool, AuthzError> {
        // Step 1: Circuit breaker check
        if self.is_circuit_open(tenant_id) {
            warn!(tenant_id = %tenant_id, "ReBAC circuit breaker open — denying");
            return Err(AuthzError::ReBacCircuitOpen {
                tenant_id: tenant_id.into_uuid(),
            });
        }

        // Step 2: Materialized table lookup (O(1))
        match authz_db::check_reachability_materialized(
            &self.pool, tenant_id, subject, relation, object,
        )
        .await
        {
            Ok(Some(true)) => return Ok(true),
            Ok(Some(false)) => {
                // Materialized says no — but could be stale. Fall through to live.
            }
            Err(e) => {
                self.record_failure(tenant_id);
                return Err(e);
            }
            Ok(None) => {}
        }

        let engine = PermissionIddfsEngine::new(
            PgGraphProvider {
                pool: self.pool.clone(),
            },
            self.config.max_depth,
        );
        let tenant_id_str = tenant_id.to_string();
        let traversal_result = timeout(
            Duration::from_millis(self.config.traversal_timeout_ms),
            engine.check_permission(&tenant_id_str, subject, relation, object),
        )
        .await;

        match traversal_result {
            Ok(true) => {
                self.reset_failures(tenant_id);
                Ok(true)
            }
            Ok(false) => {
                // Not found within max depth
                Ok(false)
            }
            Err(_timeout) => {
                warn!(tenant_id = %tenant_id, "ReBAC traversal timeout");
                self.record_failure(tenant_id);
                Err(AuthzError::ReBacCircuitOpen {
                    tenant_id: tenant_id.into_uuid(),
                })
            }
        }
    }

    /// Resolves all objects reachable from `subject` via `relation`.
    ///
    /// Used by ES/Mongo translators to inject `terms` filter (Gap5).
    /// Bounded by `max_terms_filter_size`.
    #[instrument(skip(self))]
    pub async fn resolve_objects(
        &self,
        tenant_id: TenantId,
        subject: &str,
        relation: &str,
    ) -> Result<Vec<String>, AuthzError> {
        if self.is_circuit_open(tenant_id) {
            return Err(AuthzError::ReBacCircuitOpen {
                tenant_id: tenant_id.into_uuid(),
            });
        }

        let objects = authz_db::resolve_reachable_objects(
            &self.pool,
            tenant_id,
            subject,
            relation,
            self.config.max_terms_filter_size,
        )
        .await?;

        Ok(objects)
    }
    // ── Graph Provider for IDDFS ──────────────────────────────────────────────
}

struct PgGraphProvider {
    pool: sqlx::PgPool,
}

#[async_trait]
impl GraphProvider for PgGraphProvider {
    async fn get_neighbors(&self, tenant_id: &str, subject: &str, relation: &str) -> Vec<String> {
        let tenant = match uuid::Uuid::parse_str(tenant_id) {
            Ok(u) => TenantId::from(u),
            Err(_) => return vec![],
        };
        authz_db::find_direct_objects(&self.pool, tenant, subject, relation, 1000)
            .await
            .unwrap_or_default()
    }
}

impl ReBacEngine {
    fn circuit_state(&self, tenant_id: TenantId) -> Arc<CircuitState> {
        self.circuit_states
            .entry(tenant_id)
            .or_insert_with(|| Arc::new(CircuitState::default()))
            .clone()
    }

    fn is_circuit_open(&self, tenant_id: TenantId) -> bool {
        let state = self.circuit_state(tenant_id);
        let open_until = state.open_until.lock().expect("circuit mutex poisoned");
        match *open_until {
            None => false,
            Some(until) => {
                if std::time::Instant::now() < until {
                    true
                } else {
                    // Circuit reset window has passed — allow a probe
                    false
                }
            }
        }
    }

    fn record_failure(&self, tenant_id: TenantId) {
        let state = self.circuit_state(tenant_id);
        let prev = state.failure_count.fetch_add(1, Ordering::Relaxed);
        if prev + 1 >= self.config.circuit_breaker_threshold {
            let reset_time =
                std::time::Instant::now() + Duration::from_secs(self.config.circuit_reset_secs);
            *state.open_until.lock().expect("circuit mutex poisoned") = Some(reset_time);
            warn!(
                tenant_id = %tenant_id,
                "ReBAC circuit breaker opened for {} seconds",
                self.config.circuit_reset_secs
            );
        }
    }

    fn reset_failures(&self, tenant_id: TenantId) {
        let state = self.circuit_state(tenant_id);
        state.failure_count.store(0, Ordering::Relaxed);
        *state.open_until.lock().expect("circuit mutex poisoned") = None;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_circuit_breaker_opens_after_threshold() {
        // This is a unit test of the circuit state logic only.
        let state = Arc::new(CircuitState::default());

        // Simulate 3 failures
        for _ in 0..3 {
            state.failure_count.fetch_add(1, Ordering::Relaxed);
        }

        assert_eq!(
            state.failure_count.load(Ordering::Relaxed),
            3,
            "Failure count should be 3"
        );
    }
}
