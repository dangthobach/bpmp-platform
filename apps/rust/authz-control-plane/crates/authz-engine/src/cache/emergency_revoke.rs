//! Emergency revoke cache (G4).
//!
//! A fast in-memory set of user IDs that have been emergency-revoked.
//! Checked O(1) before any policy evaluation.
//!
//! In production, this is backed by Redis. Here we use DashMap as the
//! in-process implementation — ready to swap with a Redis adapter.

use authz_core::ids::UserId;
use std::time::{Duration, Instant};
use tracing::info;

/// An entry in the revoke set with its expiry time.
#[derive(Debug)]
struct RevokeEntry {
    expires_at: Instant,
}

/// In-memory emergency revoke cache.
///
/// Thread-safe via DashMap — concurrent reads and writes are lock-free.
/// TTL-based entries expire naturally without a cleanup job.
pub struct EmergencyRevokeCache {
    /// Maps UserId → expiry Instant.
    entries: dashmap::DashMap<UserId, RevokeEntry>,
    /// Default TTL for revoke entries (configurable per deployment).
    default_ttl: Duration,
}

impl EmergencyRevokeCache {
    /// Creates a new cache with the given default TTL.
    pub fn new(default_ttl_secs: u64) -> Self {
        Self {
            entries: dashmap::DashMap::new(),
            default_ttl: Duration::from_secs(default_ttl_secs),
        }
    }

    /// Revokes a user with the default TTL.
    ///
    /// Idempotent: re-revoking resets the TTL.
    pub fn revoke(&self, user_id: UserId) {
        info!(user_id = %user_id, ttl_secs = self.default_ttl.as_secs(), "Emergency revoke applied");
        self.entries.insert(
            user_id,
            RevokeEntry {
                expires_at: Instant::now() + self.default_ttl,
            },
        );
    }

    /// Revokes a user with a custom TTL.
    pub fn revoke_with_ttl(&self, user_id: UserId, ttl: Duration) {
        self.entries.insert(
            user_id,
            RevokeEntry {
                expires_at: Instant::now() + ttl,
            },
        );
    }

    /// Checks if a user is currently revoked.
    ///
    /// O(1) DashMap lookup. Expired entries are lazily removed on check.
    pub fn is_revoked(&self, user_id: UserId) -> bool {
        match self.entries.get(&user_id) {
            None => false,
            Some(entry) => {
                if entry.expires_at > Instant::now() {
                    true
                } else {
                    // Entry expired — remove lazily
                    drop(entry);
                    self.entries.remove(&user_id);
                    false
                }
            }
        }
    }

    /// Explicitly lifts an emergency revoke for a user.
    pub fn clear_revoke(&self, user_id: UserId) {
        self.entries.remove(&user_id);
        info!(user_id = %user_id, "Emergency revoke cleared");
    }

    /// Returns the number of currently active revokes.
    pub fn active_count(&self) -> usize {
        let now = Instant::now();
        self.entries.iter().filter(|e| e.expires_at > now).count()
    }
}

impl Default for EmergencyRevokeCache {
    fn default() -> Self {
        Self::new(86_400) // 24 hours
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use authz_core::ids::UserId;

    #[test]
    fn test_revoke_and_check() {
        let cache = EmergencyRevokeCache::new(60);
        let user_id = UserId::new();
        assert!(!cache.is_revoked(user_id));
        cache.revoke(user_id);
        assert!(cache.is_revoked(user_id));
    }

    #[test]
    fn test_clear_revoke() {
        let cache = EmergencyRevokeCache::new(60);
        let user_id = UserId::new();
        cache.revoke(user_id);
        assert!(cache.is_revoked(user_id));
        cache.clear_revoke(user_id);
        assert!(!cache.is_revoked(user_id));
    }

    #[test]
    fn test_expired_entry_removed_lazily() {
        let cache = EmergencyRevokeCache::new(0); // 0-second TTL = expired immediately
        let user_id = UserId::new();
        // Force insert with expired time
        cache.entries.insert(
            user_id,
            RevokeEntry {
                expires_at: Instant::now() - Duration::from_secs(1),
            },
        );
        // Should be treated as not revoked
        assert!(!cache.is_revoked(user_id));
        assert!(cache.entries.get(&user_id).is_none());
    }
}
