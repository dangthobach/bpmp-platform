use std::{collections::HashSet, fs, path::Path};

use bpmp_authz_contracts::{
    authorization::v1::SignedWorkloadContext, AuthorizationKeyring, AuthorizationProofLimits,
    WorkloadProofCodec,
};
use serde::Deserialize;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct WorkloadKeyFile {
    #[serde(default)]
    allowed_workload_id: Option<String>,
    #[serde(default)]
    workloads: Vec<WorkloadKeySet>,
    max_proof_bytes: usize,
    max_proof_ttl_ms: u64,
    clock_skew_ms: u64,
    #[serde(default)]
    keys: Vec<WorkloadKeyEntry>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct WorkloadKeySet {
    workload_id: String,
    keys: Vec<WorkloadKeyEntry>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct WorkloadKeyEntry {
    key_id: String,
    public_key_path: String,
}

pub struct BranchScopeWorkloadVerifier {
    workloads: Vec<WorkloadKeyring>,
    max_ttl_ms: u64,
    clock_skew_ms: u64,
    limits: AuthorizationProofLimits,
}

struct WorkloadKeyring {
    workload_id: String,
    keys: AuthorizationKeyring,
}

impl BranchScopeWorkloadVerifier {
    pub fn load(path: &Path) -> anyhow::Result<Self> {
        let config: WorkloadKeyFile = serde_json::from_slice(&fs::read(path)?)?;
        let legacy_id = config.allowed_workload_id.as_deref().unwrap_or_default();
        let legacy_configured = !legacy_id.trim().is_empty() || !config.keys.is_empty();
        anyhow::ensure!(
            (legacy_configured ^ !config.workloads.is_empty())
                && config.max_proof_bytes > 0
                && config.max_proof_ttl_ms > 0
                && config.clock_skew_ms <= config.max_proof_ttl_ms,
            "branch scope workload key config is incomplete"
        );
        let limits = AuthorizationProofLimits::new(config.max_proof_bytes, 1, 1)?;
        let key_sets = if config.workloads.is_empty() {
            vec![WorkloadKeySet {
                workload_id: legacy_id.to_owned(),
                keys: config.keys,
            }]
        } else {
            anyhow::ensure!(
                config.allowed_workload_id.is_none() && config.keys.is_empty(),
                "legacy and per-workload branch scope keys cannot be mixed"
            );
            config.workloads
        };
        let mut seen_workloads = HashSet::new();
        let mut workloads = Vec::with_capacity(key_sets.len());
        for key_set in key_sets {
            anyhow::ensure!(
                !key_set.workload_id.trim().is_empty()
                    && !key_set.keys.is_empty()
                    && seen_workloads.insert(key_set.workload_id.clone()),
                "branch scope workload ID must be unique and have at least one key"
            );
            let mut keys = AuthorizationKeyring::new();
            for entry in key_set.keys {
                anyhow::ensure!(
                    !entry.key_id.trim().is_empty(),
                    "branch scope key ID is required"
                );
                let key: [u8; 32] = fs::read(&entry.public_key_path)?.try_into().map_err(|_| {
                    anyhow::anyhow!(
                        "branch scope public key {} must contain exactly 32 bytes",
                        entry.public_key_path
                    )
                })?;
                keys.insert(entry.key_id, &key)?;
            }
            workloads.push(WorkloadKeyring {
                workload_id: key_set.workload_id,
                keys,
            });
        }
        Ok(Self {
            workloads,
            max_ttl_ms: config.max_proof_ttl_ms,
            clock_skew_ms: config.clock_skew_ms,
            limits,
        })
    }

    pub fn verify(
        &self,
        proof_bytes: &[u8],
        expected_command_id: &str,
        now_epoch_ms: u64,
    ) -> anyhow::Result<SignedWorkloadContext> {
        for workload in &self.workloads {
            let Ok(proof) = WorkloadProofCodec::open(proof_bytes, &workload.keys, self.limits)
            else {
                continue;
            };
            let earliest = proof.issued_at_epoch_ms.saturating_sub(self.clock_skew_ms);
            let latest = proof.expires_at_epoch_ms.saturating_add(self.clock_skew_ms);
            if proof.workload_id == workload.workload_id
                && proof.command_id == expected_command_id
                && proof.expires_at_epoch_ms > proof.issued_at_epoch_ms
                && proof.expires_at_epoch_ms - proof.issued_at_epoch_ms <= self.max_ttl_ms
                && now_epoch_ms >= earliest
                && now_epoch_ms < latest
            {
                return Ok(proof);
            }
        }
        anyhow::bail!("branch scope workload proof is outside its authorized scope")
    }
}
