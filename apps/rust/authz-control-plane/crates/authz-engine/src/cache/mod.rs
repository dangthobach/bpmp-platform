//! In-memory policy bundle cache with atomic hot-swap.
//!
//! The policy bundle is loaded from DB at startup and refreshed
//! when the control plane signals an update.
//! Uses `tokio::sync::RwLock` for lock-free reads in the hot path.

pub mod bundle_loader;
pub mod emergency_revoke;
pub mod policy_bundle;

pub use bundle_loader::{BundleLoader, LoadedEngines};
pub use emergency_revoke::EmergencyRevokeCache;
pub use policy_bundle::PolicyBundleCache;
