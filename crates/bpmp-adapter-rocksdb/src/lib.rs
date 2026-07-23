//! Linux production adapter for the authoritative `RocksDB` event log.

#[cfg(target_os = "linux")]
mod raft_log;

#[cfg(target_os = "linux")]
mod rocks;

#[cfg(target_os = "linux")]
pub use raft_log::RocksDbRaftLogStorage;
#[cfg(target_os = "linux")]
pub use rocks::{RocksDbAtomicStateStorage, RocksDbConfig, RocksDbWorkflowStore};
