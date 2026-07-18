use authz_core::ids::UserId;
use dashmap::DashMap;
use roaring::RoaringBitmap;
use std::collections::HashMap;

/// An engine that leverages Roaring Bitmaps for extremely fast, O(1) permission evaluation.
///
/// Bitmaps are highly compressed data structures that allow bulk bitwise operations (AND, OR)
/// to be executed via SIMD instructions. This replaces O(N) string comparisons with O(1) bit checks.
pub struct PermissionBitmapEngine {
    /// Stable map of permission code -> bit position (e.g. "document:read" -> 1)
    permission_index: HashMap<String, u32>,
    /// Cache of UserId -> Compressed Bitmap of permissions they possess
    user_bitmaps: DashMap<UserId, RoaringBitmap>,
}

impl Default for PermissionBitmapEngine {
    fn default() -> Self {
        Self::new(HashMap::new())
    }
}

impl PermissionBitmapEngine {
    /// Creates a new engine with a predefined, immutable permission index.
    pub fn new(permission_index: HashMap<String, u32>) -> Self {
        Self {
            permission_index,
            user_bitmaps: DashMap::new(),
        }
    }

    /// Builds and caches the bitmap for a user based on their granted permissions.
    ///
    /// The `run_optimize()` call is crucial here to compress consecutive runs
    /// and save significant memory.
    pub fn build_for_user(&self, user_id: UserId, permissions: &[String]) {
        let mut bitmap = RoaringBitmap::new();
        for perm in permissions {
            if let Some(&bit) = self.permission_index.get(perm) {
                bitmap.insert(bit);
            }
        }

        // Ensure minimum memory footprint by compressing consecutive runs
        bitmap.optimize();

        self.user_bitmaps.insert(user_id, bitmap);
    }

    /// O(1) fast path check: does the user have this single permission?
    pub fn has_permission(&self, user_id: UserId, permission_code: &str) -> bool {
        if let Some(&bit) = self.permission_index.get(permission_code) {
            if let Some(bitmap) = self.user_bitmaps.get(&user_id) {
                return bitmap.contains(bit);
            }
        }
        false
    }

    /// Evaluates if the user has ALL of the requested permissions using SIMD AND.
    pub fn has_all_permissions(&self, user_id: UserId, codes: &[&str]) -> bool {
        let mut required = RoaringBitmap::new();
        for &code in codes {
            if let Some(&bit) = self.permission_index.get(code) {
                required.insert(bit);
            } else {
                // If a requested permission doesn't even exist in the system, they can't have it
                return false;
            }
        }

        if let Some(user_bitmap) = self.user_bitmaps.get(&user_id) {
            // Is `required` a subset of `user_bitmap`?
            return required.is_subset(&user_bitmap);
        }

        false
    }

    /// Evaluates if the user has ANY of the requested permissions using SIMD OR/Intersection.
    pub fn has_any_permission(&self, user_id: UserId, codes: &[&str]) -> bool {
        let mut required = RoaringBitmap::new();
        for &code in codes {
            if let Some(&bit) = self.permission_index.get(code) {
                required.insert(bit);
            }
        }

        if let Some(user_bitmap) = self.user_bitmaps.get(&user_id) {
            // Does the intersection of both sets have at least one element?
            return !user_bitmap.is_disjoint(&required);
        }

        false
    }

    /// Removes a user's bitmap from the cache (e.g. on role revocation).
    pub fn invalidate_user(&self, user_id: UserId) {
        self.user_bitmaps.remove(&user_id);
    }
}
