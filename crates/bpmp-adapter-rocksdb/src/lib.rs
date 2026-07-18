//! Linux production adapter for the authoritative `RocksDB` event log.

#[cfg(target_os = "linux")]
mod rocks;

#[cfg(target_os = "linux")]
pub use rocks::{RocksDbConfig, RocksDbWorkflowStore};
