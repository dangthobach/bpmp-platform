#![forbid(unsafe_code)]

mod config;
#[cfg(target_os = "linux")]
mod engine;
#[cfg(target_os = "linux")]
mod key_lifecycle;
#[cfg(target_os = "linux")]
mod policy;
#[cfg(target_os = "linux")]
mod runtime;
#[cfg(target_os = "linux")]
mod service;
#[cfg(target_os = "linux")]
mod store;

use std::path::PathBuf;

use clap::Parser;

#[derive(Debug, Parser)]
#[command(name = "governance-service")]
struct Arguments {
    #[arg(long, env = "BPMP_GOVERNANCE_CONFIG")]
    config: PathBuf,
}

#[cfg(target_os = "linux")]
#[tokio::main]
async fn main() -> anyhow::Result<()> {
    runtime::run(Arguments::parse().config).await
}

#[cfg(not(target_os = "linux"))]
fn main() -> anyhow::Result<()> {
    let arguments = Arguments::parse();
    let _ = config::RuntimeConfig::load(&arguments.config)?;
    anyhow::bail!("governance-service production runtime requires Linux for Kafka hot reload")
}
