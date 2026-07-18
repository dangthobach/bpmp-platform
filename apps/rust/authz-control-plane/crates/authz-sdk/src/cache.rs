//! Version-aware decision cache decorator.
//!
//! ## Invalidation strategy (no broadcast required)
//! Cache key includes `attributes_version` from the request. When user
//! attributes change in `authz-server`, the next sync to the PEP bumps the
//! version, producing a different key → the stale entry is unreachable.
//! It will be evicted by TTL / size limit eventually.
//!
//! Complexity:
//! * lookup  — O(1) (moka concurrent hash)
//! * insert  — O(1) amortized
//! * memory  — bounded by `max_capacity` (set by caller)

use std::time::Duration;

use async_trait::async_trait;
use moka::future::Cache;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::client::AuthzClient;
use crate::error::SdkError;
use crate::types::{
    CheckRequest, CheckResponse, ExplainRequest, ExplainResponse, FilterRequest, FilterResponse,
};

#[derive(Clone, Hash, Eq, PartialEq, Debug, Serialize, Deserialize)]
struct DecisionKey {
    tenant: Uuid,
    user: Uuid,
    version: i64,
    action: String,
    resource_type: String,
    resource_ref: Option<String>,
}

impl DecisionKey {
    fn from_check(req: &CheckRequest) -> Self {
        Self {
            tenant: req.tenant_id,
            user: req.user_id,
            version: req.user_attributes_version,
            action: req.action.clone(),
            resource_type: req.resource_type.clone(),
            resource_ref: req.resource_ref.clone(),
        }
    }
}

pub struct CachedAuthzClient<C: AuthzClient> {
    inner: C,
    cache: Cache<DecisionKey, CheckResponse>,
}

impl<C: AuthzClient> CachedAuthzClient<C> {
    pub fn new(inner: C, max_capacity: u64, ttl: Duration) -> Self {
        let cache = Cache::builder()
            .max_capacity(max_capacity)
            .time_to_live(ttl)
            .build();
        Self { inner, cache }
    }

    pub fn invalidate_user(&self, tenant: Uuid, user: Uuid) {
        // Best-effort: moka has no prefix scan. Use a sentinel-version-bump
        // strategy at the source instead. This method exists for tests/admin.
        let _ = (tenant, user);
    }
}

#[async_trait]
impl<C: AuthzClient + 'static> AuthzClient for CachedAuthzClient<C> {
    async fn check(&self, req: &CheckRequest) -> Result<CheckResponse, SdkError> {
        let key = DecisionKey::from_check(req);
        if let Some(hit) = self.cache.get(&key).await {
            metrics::counter!("authz_sdk_cache_hits_total").increment(1);
            return Ok(hit);
        }
        metrics::counter!("authz_sdk_cache_misses_total").increment(1);
        let resp = self.inner.check(req).await?;
        self.cache.insert(key, resp.clone()).await;
        Ok(resp)
    }

    async fn filter(&self, req: &FilterRequest) -> Result<FilterResponse, SdkError> {
        // Filter responses are large and per-query; do not cache by default.
        self.inner.filter(req).await
    }

    async fn explain(&self, req: &ExplainRequest) -> Result<ExplainResponse, SdkError> {
        self.inner.explain(req).await
    }
}
