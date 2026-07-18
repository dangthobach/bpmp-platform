//! Warm-up loader for high-performance core fast paths.
//!
//! Populates the three in-memory structures that would otherwise be empty
//! (and therefore no-op) on a freshly started server:
//!
//! - `PermissionBitmapEngine` — `(user_id → RoaringBitmap)` of static grants
//! - `PermissionCuckooFilter` — per-tenant probabilistic membership filter
//! - `PolicyBundleCache`      — temporal policies + `TemporalIntervalTree`
//!
//! The loader runs once at startup. A refresh hook is exposed so that the
//! control plane (or an outbox subscriber) can re-warm a single tenant
//! after a role / permission / temporal-policy mutation.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use authz_core::AuthzError;
use sqlx::PgPool;
use tracing::{info, instrument, warn};

use crate::algorithms::{bitmap::PermissionBitmapEngine, cuckoo::PermissionCuckooFilter};
use crate::cache::policy_bundle::{PolicyBundle, PolicyBundleCache};

/// Hard cap on the number of grant rows pulled per warm-up cycle.
/// Tunable through `BundleLoader::with_grant_limit`.
const DEFAULT_GRANT_LIMIT: i64 = 1_000_000;

/// Output of `BundleLoader::load_initial`. Owns ready-to-share engines.
pub struct LoadedEngines {
    pub bitmap_engine: Arc<PermissionBitmapEngine>,
    pub cuckoo_filter: Arc<PermissionCuckooFilter>,
    pub policy_bundle_cache: Arc<PolicyBundleCache>,
}

/// Stateless loader — every method takes the DB pool as a parameter.
pub struct BundleLoader {
    pool: PgPool,
    grant_limit: i64,
}

impl BundleLoader {
    pub fn new(pool: PgPool) -> Self {
        Self {
            pool,
            grant_limit: DEFAULT_GRANT_LIMIT,
        }
    }

    pub fn with_grant_limit(mut self, limit: i64) -> Self {
        self.grant_limit = limit;
        self
    }

    /// Performs the initial warm-up and returns the populated engines.
    /// Called by `authz-server::app::run` before the HTTP listener binds.
    #[instrument(skip(self))]
    pub async fn load_initial(&self) -> Result<LoadedEngines, AuthzError> {
        let grants = authz_db::list_active_user_grants(&self.pool, self.grant_limit).await?;
        info!(
            grants = grants.len(),
            "Loaded user-permission grants for warm-up"
        );

        // 1) Build a stable global permission_code → bit index.
        let mut permission_index: HashMap<String, u32> = HashMap::new();
        for grant in &grants {
            let next_bit = permission_index.len() as u32;
            permission_index
                .entry(grant.permission_code.clone())
                .or_insert(next_bit);
        }
        let bitmap_engine = Arc::new(PermissionBitmapEngine::new(permission_index));

        // 2) Group permissions per user, then build one bitmap per user.
        let mut by_user: HashMap<authz_core::ids::UserId, Vec<String>> = HashMap::new();
        for grant in &grants {
            by_user
                .entry(grant.user_id)
                .or_default()
                .push(grant.permission_code.clone());
        }
        for (user_id, perms) in &by_user {
            bitmap_engine.build_for_user(*user_id, perms);
        }
        info!(users = by_user.len(), "Roaring bitmaps materialised");

        // 3) Populate the per-tenant Cuckoo filter (deduplicated).
        let cuckoo_filter = Arc::new(PermissionCuckooFilter::new());
        let mut seen: HashSet<(authz_core::ids::TenantId, authz_core::ids::UserId, String)> =
            HashSet::new();
        for grant in &grants {
            let key = (
                grant.tenant_id,
                grant.user_id,
                grant.permission_code.clone(),
            );
            if seen.insert(key) {
                cuckoo_filter.grant_permission(
                    grant.tenant_id,
                    grant.user_id,
                    &grant.permission_code,
                );
            }
        }
        info!("Cuckoo filter warmed");

        // 4) Build the policy bundles from active temporal policies.
        let policy_bundle_cache = Arc::new(PolicyBundleCache::new());
        let temporal_rows = authz_db::list_active_temporal_policies(&self.pool).await?;
        let mut by_tenant: HashMap<authz_core::ids::TenantId, HashMap<String, Vec<_>>> =
            HashMap::new();
        for (tenant_id, policy) in temporal_rows {
            by_tenant
                .entry(tenant_id)
                .or_default()
                .entry(policy.permission_id.to_string())
                .or_default()
                .push(policy);
        }
        for (tenant_id, temporal_policies) in by_tenant {
            let bundle = PolicyBundle {
                tenant_id,
                version: 1,
                temporal_policies,
                bundle_data: serde_json::Value::Null,
                temporal_index: HashMap::new(),
            };
            policy_bundle_cache.update(bundle).await;
        }
        info!("Policy bundles warmed");

        Ok(LoadedEngines {
            bitmap_engine,
            cuckoo_filter,
            policy_bundle_cache,
        })
    }

    /// Refreshes just the per-tenant temporal bundle. Intended to be invoked
    /// by the outbox subscriber when a `TemporalPolicyChanged` event arrives.
    #[instrument(skip(self, cache), fields(tenant_id = %tenant_id))]
    pub async fn refresh_temporal(
        &self,
        cache: &PolicyBundleCache,
        tenant_id: authz_core::ids::TenantId,
    ) -> Result<(), AuthzError> {
        let rows = authz_db::list_active_temporal_policies(&self.pool).await?;
        let mut temporal_policies: HashMap<String, Vec<_>> = HashMap::new();
        for (tid, policy) in rows.into_iter().filter(|(tid, _)| *tid == tenant_id) {
            let _ = tid; // tenant filter applied above
            temporal_policies
                .entry(policy.permission_id.to_string())
                .or_default()
                .push(policy);
        }
        let existing_version = cache.version(tenant_id).await.unwrap_or(0);
        let bundle = PolicyBundle {
            tenant_id,
            version: existing_version + 1,
            temporal_policies,
            bundle_data: serde_json::Value::Null,
            temporal_index: HashMap::new(),
        };
        cache.update(bundle).await;
        warn!("Temporal bundle refreshed");
        Ok(())
    }
}
