//! Policy bundle hot-swap cache (G4: Data Plane).
//!
//! Holds the compiled policy bundle in memory.
//! Updated atomically via `tokio::sync::RwLock` — reads never block writes
//! for more than a single pointer swap.

use authz_core::ids::TenantId;
use authz_core::models::filter::TemporalPolicy;
use chrono::Timelike;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;
use tracing::{info, warn};

use crate::algorithms::interval_tree::{TemporalInterval, TemporalIntervalTree};

/// The in-memory policy bundle for a single tenant.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PolicyBundle {
    pub tenant_id: TenantId,
    pub version: u64,
    /// Temporal policies keyed by permission_id string.
    pub temporal_policies: HashMap<String, Vec<TemporalPolicy>>,
    /// Additional bundle data (row filters, field filters) can be added here.
    pub bundle_data: serde_json::Value,

    /// Per-permission `TemporalIntervalTree` indexing time-of-day windows
    /// `[allowed_from, allowed_until]` measured in seconds-from-midnight.
    ///
    /// The tree is rebuilt whenever `temporal_policies` change.
    /// Pre-filter cost drops from O(N) linear scan to O(log N) overlap query
    /// when a permission has many policies (e.g. shift schedules).
    #[serde(skip, default)]
    pub temporal_index: HashMap<String, TemporalIntervalTree>,
}

impl PolicyBundle {
    /// Rebuilds `temporal_index` from the current `temporal_policies` map.
    /// Must be called after constructing or mutating `temporal_policies`.
    pub fn rebuild_temporal_index(&mut self) {
        let mut index = HashMap::with_capacity(self.temporal_policies.len());
        for (perm_id, policies) in &self.temporal_policies {
            let mut tree = TemporalIntervalTree::new();
            for policy in policies.iter().filter(|p| p.is_active) {
                let from = naive_time_to_seconds(policy.allowed_from);
                let until = naive_time_to_seconds(policy.allowed_until);
                tree.insert(TemporalInterval::new(from, until));
            }
            index.insert(perm_id.clone(), tree);
        }
        self.temporal_index = index;
    }

    /// Returns `true` when the bundle has at least one time-of-day window
    /// covering `now_seconds` for the given permission.
    ///
    /// Used as an O(log N) pre-filter inside `evaluate_temporal_gate` before
    /// running the full per-policy day / timezone / CIDR check.
    pub fn has_time_window(&self, permission_id: &str, now_seconds: i64) -> bool {
        match self.temporal_index.get(permission_id) {
            None => true, // No index → fall back to linear scan (safe default).
            Some(tree) => !tree
                .find_overlapping(TemporalInterval::new(now_seconds, now_seconds))
                .is_empty(),
        }
    }
}

fn naive_time_to_seconds(t: chrono::NaiveTime) -> i64 {
    (t.num_seconds_from_midnight() as i64).min(86_399)
}

/// Atomic hot-swap cache for policy bundles.
///
/// In production, bundles are pushed from the Control Plane via an async channel.
/// In this implementation, we use a Tokio RwLock for thread-safe atomic updates.
pub struct PolicyBundleCache {
    /// Per-tenant bundle store. Key = TenantId string for easier concurrent access.
    bundles: Arc<RwLock<HashMap<String, PolicyBundle>>>,
}

impl PolicyBundleCache {
    pub fn new() -> Self {
        Self {
            bundles: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Updates the policy bundle for a tenant.
    ///
    /// Idempotent: if the incoming version is not newer, the update is ignored.
    pub async fn update(&self, mut new_bundle: PolicyBundle) {
        let tenant_key = new_bundle.tenant_id.to_string();
        let new_version = new_bundle.version;

        let mut bundles = self.bundles.write().await;

        if let Some(existing) = bundles.get(&tenant_key) {
            if new_bundle.version <= existing.version {
                warn!(
                    tenant_id = %new_bundle.tenant_id,
                    incoming_version = new_version,
                    current_version = existing.version,
                    "Ignoring stale policy bundle update"
                );
                return;
            }
        }

        new_bundle.rebuild_temporal_index();

        info!(
            tenant_id = %new_bundle.tenant_id,
            version = new_version,
            "Policy bundle updated"
        );
        bundles.insert(tenant_key, new_bundle);
    }

    /// Retrieves the current policy bundle for a tenant.
    ///
    /// Returns `None` if no bundle is loaded (triggers fallback in the pipeline).
    pub async fn get(&self, tenant_id: TenantId) -> Option<PolicyBundle> {
        let bundles = self.bundles.read().await;
        bundles.get(&tenant_id.to_string()).cloned()
    }

    /// Returns the current version number for a tenant's bundle.
    pub async fn version(&self, tenant_id: TenantId) -> Option<u64> {
        let bundles = self.bundles.read().await;
        bundles.get(&tenant_id.to_string()).map(|b| b.version)
    }
}

impl Default for PolicyBundleCache {
    fn default() -> Self {
        Self::new()
    }
}
