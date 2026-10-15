#![forbid(unsafe_code)]

mod config;

#[cfg(target_os = "linux")]
mod governance_runtime;
#[cfg(target_os = "linux")]
mod linux_runtime;
#[cfg(target_os = "linux")]
mod raft_runtime;
#[cfg(target_os = "linux")]
mod remote_worker_runtime;

use std::path::PathBuf;

use clap::Parser;

#[derive(Debug, Parser)]
#[command(name = "bpmp-engine", about = "BPMP authoritative workflow engine")]
struct Arguments {
    /// Path to the complete versioned runtime configuration document.
    #[arg(long, env = "BPMP_ENGINE_CONFIG")]
    config: PathBuf,
    /// Check an existing offline `RocksDB` without starting Raft or dispatch workers.
    #[arg(long, conflicts_with = "storage_checkpoint")]
    storage_check: bool,
    /// Create a native checkpoint of an existing offline database in a new directory.
    #[arg(long)]
    storage_checkpoint: Option<PathBuf>,
}

#[cfg(target_os = "linux")]
#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let arguments = Arguments::parse();
    if arguments.storage_check || arguments.storage_checkpoint.is_some() {
        return linux_runtime::storage_maintenance(
            &arguments.config,
            arguments.storage_checkpoint.as_deref(),
        );
    }
    linux_runtime::run(arguments.config).await
}

#[cfg(not(target_os = "linux"))]
fn main() -> anyhow::Result<()> {
    let arguments = Arguments::parse();
    let _ = config::RuntimeConfig::load(&arguments.config)?;
    anyhow::bail!("this repository's RocksDB production adapter currently requires Linux")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maintenance_modes_parse_explicit_configuration() {
        let check = Arguments::try_parse_from([
            "bpmp-engine",
            "--config",
            "engine.json",
            "--storage-check",
        ])
        .unwrap();
        assert!(check.storage_check);
        let backup = Arguments::try_parse_from([
            "bpmp-engine",
            "--config",
            "engine.json",
            "--storage-checkpoint",
            "/backup/new",
        ])
        .unwrap();
        assert_eq!(
            backup.storage_checkpoint,
            Some(PathBuf::from("/backup/new"))
        );
    }

    #[test]
    fn maintenance_modes_cannot_be_combined() {
        assert!(
            Arguments::try_parse_from([
                "bpmp-engine",
                "--config",
                "engine.json",
                "--storage-check",
                "--storage-checkpoint",
                "/backup/new"
            ])
            .is_err()
        );
    }
}
