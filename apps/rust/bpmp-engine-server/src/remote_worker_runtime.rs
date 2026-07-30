use std::collections::{BTreeMap, BTreeSet};

use anyhow::{Context, Result};
use bpmp_authz_contracts::{
    AuthorizationArtifactSigner, AuthorizationKeyring, AuthorizationProofLimits, Ed25519Signer,
    WorkloadProofCodec,
};
use bpmp_contracts::engine::v1::{RemoteWorkerRegistration, SignedRemoteAssignmentToken};
use bpmp_domain_core::TenantId;
use bpmp_engine::{
    RemoteAssignmentTokenPort, RemoteAssignmentTokenRequest, RemoteWorkerTransportError,
    RemoteWorkerVerifierPort, VerifiedRemoteWorker,
};
use prost::Message;
use sha2::{Digest, Sha256};

use crate::config::{RemoteWorkerAuthorizationConfig, RemoteWorkerIdentityConfig};

const REMOTE_ASSIGNMENT_TOKEN_SCHEMA_VERSION: u32 = 1;

struct TrustedRemoteIdentity {
    workload_id: String,
    tenant_ids: BTreeSet<String>,
}

pub struct ConfiguredRemoteWorkerVerifier {
    workload_keys: AuthorizationKeyring,
    proof_limits: AuthorizationProofLimits,
    allowed_protocol_versions: BTreeSet<String>,
    identities: BTreeMap<[u8; 32], TrustedRemoteIdentity>,
    clock_skew_ms: u64,
    max_registration_proof_ttl_ms: u64,
}

impl ConfiguredRemoteWorkerVerifier {
    pub fn new(
        config: &RemoteWorkerAuthorizationConfig,
        workload_keys: AuthorizationKeyring,
        proof_limits: AuthorizationProofLimits,
        clock_skew_seconds: u64,
    ) -> Result<Self> {
        let mut identities = BTreeMap::new();
        for identity in &config.identities {
            let fingerprint =
                decode_sha256(&identity.certificate_sha256_hex).with_context(|| {
                    format!(
                        "decode remote worker certificate fingerprint for {}",
                        identity.workload_id
                    )
                })?;
            let trusted = trusted_identity(identity);
            if identities.insert(fingerprint, trusted).is_some() {
                anyhow::bail!("duplicate remote worker certificate fingerprint");
            }
        }
        let clock_skew_ms = clock_skew_seconds
            .checked_mul(1_000)
            .context("remote worker clock skew overflows milliseconds")?;
        Ok(Self {
            workload_keys,
            proof_limits,
            allowed_protocol_versions: config.allowed_protocol_versions.iter().cloned().collect(),
            identities,
            clock_skew_ms,
            max_registration_proof_ttl_ms: config.max_registration_proof_ttl_ms,
        })
    }
}

impl RemoteWorkerVerifierPort for ConfiguredRemoteWorkerVerifier {
    fn verify(
        &self,
        peer_certificate_der: &[u8],
        session_id: &str,
        registration: &RemoteWorkerRegistration,
        now_epoch_ms: u64,
    ) -> Result<VerifiedRemoteWorker, RemoteWorkerTransportError> {
        if peer_certificate_der.is_empty()
            || session_id.trim().is_empty()
            || registration.worker_id.trim().is_empty()
            || registration.tenant_id.trim().is_empty()
            || !self
                .allowed_protocol_versions
                .contains(&registration.protocol_version)
        {
            return Err(RemoteWorkerTransportError::Unauthenticated);
        }
        let fingerprint: [u8; 32] = Sha256::digest(peer_certificate_der).into();
        let trusted = self
            .identities
            .get(&fingerprint)
            .ok_or(RemoteWorkerTransportError::Unauthenticated)?;
        if trusted.workload_id != registration.worker_id
            || !trusted.tenant_ids.contains(&registration.tenant_id)
        {
            return Err(RemoteWorkerTransportError::Unauthenticated);
        }
        let proof = registration
            .workload_proof
            .as_ref()
            .ok_or(RemoteWorkerTransportError::Unauthenticated)?;
        let context =
            WorkloadProofCodec::open(&proof.signed_proof, &self.workload_keys, self.proof_limits)
                .map_err(|_| RemoteWorkerTransportError::Unauthenticated)?;
        let latest_issued_at = now_epoch_ms
            .checked_add(self.clock_skew_ms)
            .ok_or(RemoteWorkerTransportError::Unauthenticated)?;
        let latest_accepted_expiry = context
            .expires_at_epoch_ms
            .checked_add(self.clock_skew_ms)
            .ok_or(RemoteWorkerTransportError::Unauthenticated)?;
        let proof_ttl = context
            .expires_at_epoch_ms
            .checked_sub(context.issued_at_epoch_ms)
            .ok_or(RemoteWorkerTransportError::Unauthenticated)?;
        if context.tenant_id != registration.tenant_id
            || context.workload_id != registration.worker_id
            || context.command_id != session_id
            || context.issued_at_epoch_ms > latest_issued_at
            || latest_accepted_expiry < now_epoch_ms
            || proof_ttl > self.max_registration_proof_ttl_ms
        {
            return Err(RemoteWorkerTransportError::Unauthenticated);
        }
        Ok(VerifiedRemoteWorker {
            tenant_id: TenantId::new(registration.tenant_id.clone())
                .map_err(|_| RemoteWorkerTransportError::Unauthenticated)?,
            worker_id: registration.worker_id.clone(),
        })
    }
}

pub struct SignedRemoteAssignmentTokenIssuer {
    signing_key_id: String,
    signer: Ed25519Signer,
}

impl SignedRemoteAssignmentTokenIssuer {
    pub fn new(signing_key_id: String, signing_key: [u8; 32]) -> Result<Self> {
        if signing_key_id.trim().is_empty() {
            anyhow::bail!("remote assignment signing key id is empty");
        }
        Ok(Self {
            signing_key_id,
            signer: Ed25519Signer::from_bytes(&signing_key),
        })
    }
}

impl RemoteAssignmentTokenPort for SignedRemoteAssignmentTokenIssuer {
    fn issue(
        &self,
        request: &RemoteAssignmentTokenRequest,
    ) -> Result<Vec<u8>, RemoteWorkerTransportError> {
        if request.assignment_id.trim().is_empty()
            || request.task_id.trim().is_empty()
            || request.worker_id.trim().is_empty()
            || request.session_id.trim().is_empty()
            || request.lease_until_epoch_ms == 0
        {
            return Err(RemoteWorkerTransportError::InvalidFrame(
                "remote assignment token request is invalid".into(),
            ));
        }
        let mut token = SignedRemoteAssignmentToken {
            schema_version: REMOTE_ASSIGNMENT_TOKEN_SCHEMA_VERSION,
            assignment_id: request.assignment_id.clone(),
            tenant_id: request.tenant_id.to_string(),
            task_id: request.task_id.clone(),
            worker_id: request.worker_id.clone(),
            session_id: request.session_id.clone(),
            lease_expires_at_epoch_ms: request.lease_until_epoch_ms,
            signing_key_id: self.signing_key_id.clone(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        };
        let digest: [u8; 32] = Sha256::digest(token.encode_to_vec()).into();
        token.content_hash = digest.to_vec();
        token.signature = self.signer.sign(&digest);
        Ok(token.encode_to_vec())
    }
}

fn trusted_identity(identity: &RemoteWorkerIdentityConfig) -> TrustedRemoteIdentity {
    TrustedRemoteIdentity {
        workload_id: identity.workload_id.clone(),
        tenant_ids: identity.tenant_ids.iter().cloned().collect(),
    }
}

fn decode_sha256(value: &str) -> Result<[u8; 32]> {
    if value.len() != 64 {
        anyhow::bail!("SHA-256 fingerprint must contain 64 hexadecimal characters");
    }
    let mut decoded = [0_u8; 32];
    for (index, slot) in decoded.iter_mut().enumerate() {
        let offset = index * 2;
        *slot = u8::from_str_radix(&value[offset..offset + 2], 16)
            .context("invalid SHA-256 fingerprint hexadecimal")?;
    }
    Ok(decoded)
}

#[cfg(test)]
mod tests {
    use bpmp_authz_contracts::authorization::v1::{SignedWorkloadContext, WorkloadProof};
    use bpmp_authz_contracts::{AUTHORIZATION_PROOF_SCHEMA_VERSION, WorkloadProofCodec};

    use super::*;

    fn verifier_fixture(
        certificate: &[u8],
        signer: &Ed25519Signer,
    ) -> ConfiguredRemoteWorkerVerifier {
        let mut keys = AuthorizationKeyring::new();
        keys.insert("worker-key-v1", &signer.verifying_key_bytes())
            .unwrap();
        ConfiguredRemoteWorkerVerifier::new(
            &RemoteWorkerAuthorizationConfig {
                allowed_protocol_versions: vec!["1.0".into()],
                identities: vec![RemoteWorkerIdentityConfig {
                    workload_id: "worker-a".into(),
                    certificate_sha256_hex: Sha256::digest(certificate)
                        .iter()
                        .map(|byte| format!("{byte:02x}"))
                        .collect(),
                    tenant_ids: vec!["tenant-a".into()],
                }],
                assignment_signing_key: "unused".into(),
                assignment_signing_key_id: "assignment-v1".into(),
                max_registration_proof_ttl_ms: 5_000,
            },
            keys,
            AuthorizationProofLimits::new(16_384, 8, 8).unwrap(),
            5,
        )
        .unwrap()
    }

    fn registration(
        signer: &Ed25519Signer,
        session_id: &str,
        expires_at_epoch_ms: u64,
    ) -> RemoteWorkerRegistration {
        let proof = WorkloadProofCodec::seal(
            SignedWorkloadContext {
                schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
                tenant_id: "tenant-a".into(),
                workload_id: "worker-a".into(),
                command_id: session_id.into(),
                issued_at_epoch_ms: 1_000,
                expires_at_epoch_ms,
                signing_key_id: String::new(),
                content_hash: Vec::new(),
                signature: Vec::new(),
            },
            "worker-key-v1",
            signer,
            AuthorizationProofLimits::new(16_384, 8, 8).unwrap(),
        )
        .unwrap();
        RemoteWorkerRegistration {
            worker_id: "worker-a".into(),
            protocol_version: "1.0".into(),
            capabilities: vec!["invoice".into()],
            initial_credit: 1,
            workload_proof: Some(WorkloadProof {
                signed_proof: proof,
            }),
            tenant_id: "tenant-a".into(),
        }
    }

    #[test]
    fn verifier_binds_certificate_workload_tenant_and_session() {
        let certificate = b"test-certificate-der";
        let signer = Ed25519Signer::from_bytes(&[7; 32]);
        let verifier = verifier_fixture(certificate, &signer);
        let verified = verifier
            .verify(
                certificate,
                "session-a",
                &registration(&signer, "session-a", 2_000),
                1_500,
            )
            .unwrap();
        assert_eq!(verified.worker_id, "worker-a");

        assert_eq!(
            verifier.verify(
                b"other-certificate",
                "session-a",
                &registration(&signer, "session-a", 2_000),
                1_500,
            ),
            Err(RemoteWorkerTransportError::Unauthenticated)
        );
        assert_eq!(
            verifier.verify(
                certificate,
                "other-session",
                &registration(&signer, "session-a", 2_000),
                1_500,
            ),
            Err(RemoteWorkerTransportError::Unauthenticated)
        );
        assert_eq!(
            verifier.verify(
                certificate,
                "session-a",
                &registration(&signer, "session-a", 7_000),
                1_500,
            ),
            Err(RemoteWorkerTransportError::Unauthenticated)
        );
    }

    #[test]
    fn assignment_token_is_signed_and_lease_bound() {
        let issuer =
            SignedRemoteAssignmentTokenIssuer::new("assignment-v1".into(), [9; 32]).unwrap();
        let bytes = issuer
            .issue(&RemoteAssignmentTokenRequest {
                assignment_id: "assignment-a".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                task_id: "task-a".into(),
                worker_id: "worker-a".into(),
                session_id: "session-a".into(),
                lease_until_epoch_ms: 2_000,
            })
            .unwrap();
        let token = SignedRemoteAssignmentToken::decode(bytes.as_slice()).unwrap();
        assert_eq!(token.schema_version, REMOTE_ASSIGNMENT_TOKEN_SCHEMA_VERSION);
        assert_eq!(token.lease_expires_at_epoch_ms, 2_000);
        assert_eq!(token.content_hash.len(), 32);
        assert_eq!(token.signature.len(), 64);
    }
}
