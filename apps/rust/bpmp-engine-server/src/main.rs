#![forbid(unsafe_code)]

mod config;

#[cfg(target_os = "linux")]
mod linux_runtime;

use std::path::PathBuf;

use clap::Parser;

#[derive(Debug, Parser)]
#[command(name = "bpmp-engine", about = "BPMP authoritative workflow engine")]
struct Arguments {
    /// Path to the complete versioned runtime configuration document.
    #[arg(long, env = "BPMP_ENGINE_CONFIG")]
    config: PathBuf,
}

#[cfg(target_os = "linux")]
#[tokio::main]
async fn main() -> anyhow::Result<()> {
    linux_runtime::run(Arguments::parse().config).await
}

#[cfg(not(target_os = "linux"))]
fn main() -> anyhow::Result<()> {
    let arguments = Arguments::parse();
    let _ = config::RuntimeConfig::load(&arguments.config)?;
    anyhow::bail!("bpmp-engine production runtime requires Linux because RocksDB is Linux-only")
}
