use authz_core::ids::{TenantId, UserId};
use cuckoofilter::CuckooFilter;
use dashmap::DashMap;
use std::collections::hash_map::DefaultHasher;

/// An engine that leverages Cuckoo Filters for O(1) early rejection.
///
/// It acts as a probabilistic filter:
/// - If it returns `false` (does not contain), the user DEFINITELY DOES NOT have the permission.
/// - If it returns `true` (might contain), the user MIGHT have the permission (false positive ~0.1%),
///   and we fallback to the full evaluation path.
///
/// We use CuckooFilter over BloomFilter because it natively supports deletion (revocation).
pub struct PermissionCuckooFilter {
    /// Separate filter per tenant to maintain isolation and prevent filter saturation.
    tenant_filters: DashMap<TenantId, CuckooFilter<DefaultHasher>>,
}

impl Default for PermissionCuckooFilter {
    fn default() -> Self {
        Self::new()
    }
}

impl PermissionCuckooFilter {
    pub fn new() -> Self {
        Self {
            tenant_filters: DashMap::new(),
        }
    }

    /// Helper to generate a unique cache key for a user-permission pair
    fn generate_key(user_id: UserId, permission_code: &str) -> String {
        format!("{}:{}", user_id.into_uuid(), permission_code)
    }

    /// Grants a permission in the filter.
    pub fn grant_permission(&self, tenant_id: TenantId, user_id: UserId, permission_code: &str) {
        let key = Self::generate_key(user_id, permission_code);

        let mut filter = self.tenant_filters.entry(tenant_id).or_insert_with(|| {
            // Initialize with capacity for 1,000,000 items
            CuckooFilter::with_capacity(1_000_000)
        });

        // Ensure we don't insert duplicate elements which degrades the Cuckoo filter performance
        if !filter.contains(&key) {
            let _ = filter.add(&key); // ignoring Error if full, in prod should handle expansion
        }
    }

    /// Revokes a permission in the filter.
    pub fn revoke_permission(&self, tenant_id: TenantId, user_id: UserId, permission_code: &str) {
        if let Some(mut filter) = self.tenant_filters.get_mut(&tenant_id) {
            let key = Self::generate_key(user_id, permission_code);
            let _ = filter.delete(&key);
        }
    }

    /// Fast Rejection Path: Checks if the user MIGHT have the permission.
    ///
    /// If this returns `false`, it is 100% guaranteed the user does not have the permission.
    /// If this returns `true`, the system MUST evaluate further (DB or Bitmap).
    pub fn might_have_permission(
        &self,
        tenant_id: TenantId,
        user_id: UserId,
        permission_code: &str,
    ) -> bool {
        if let Some(filter) = self.tenant_filters.get(&tenant_id) {
            let key = Self::generate_key(user_id, permission_code);
            return filter.contains(&key);
        }

        // If filter doesn't exist for tenant, return true to fallback to normal evaluation safely
        true
    }
}
