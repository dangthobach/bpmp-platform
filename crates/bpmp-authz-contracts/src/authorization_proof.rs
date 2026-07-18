use prost::Message;
use sha2::{Digest, Sha256};
use thiserror::Error;

use crate::authorization::v1::{SignedActorContext, SignedWorkloadContext};
use crate::{AuthorizationArtifactSigner, AuthorizationKeyring};

pub const AUTHORIZATION_PROOF_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct AuthorizationProofLimits {
    proof_bytes: usize,
    roles: usize,
    capabilities: usize,
}

impl AuthorizationProofLimits {
    /// Creates deploy-time bounds for untrusted identity proofs.
    ///
    /// # Errors
    ///
    /// Every bound must be greater than zero.
    pub const fn new(
        max_proof_bytes: usize,
        max_roles: usize,
        max_capabilities: usize,
    ) -> Result<Self, AuthorizationProofError> {
        if max_proof_bytes == 0 || max_roles == 0 || max_capabilities == 0 {
            Err(AuthorizationProofError::InvalidLimits)
        } else {
            Ok(Self {
                proof_bytes: max_proof_bytes,
                roles: max_roles,
                capabilities: max_capabilities,
            })
        }
    }
}

pub struct ActorProofCodec;

impl ActorProofCodec {
    /// Canonicalizes and signs a command-bound actor context.
    ///
    /// # Errors
    ///
    /// Rejects invalid fields or configured proof limits.
    pub fn seal(
        mut proof: SignedActorContext,
        key_id: &str,
        signer: &dyn AuthorizationArtifactSigner,
        limits: AuthorizationProofLimits,
    ) -> Result<Vec<u8>, AuthorizationProofError> {
        validate_key_id(key_id)?.clone_into(&mut proof.signing_key_id);
        canonicalize_actor(&mut proof);
        validate_actor(&proof, limits)?;
        let digest = actor_digest(&proof);
        proof.content_hash = digest.to_vec();
        proof.signature = signer.sign(&digest);
        encode_bounded(&proof, limits)
    }

    /// Verifies and decodes a canonical actor context.
    ///
    /// # Errors
    ///
    /// Fails closed on malformed, oversized, non-canonical, or invalidly signed data.
    pub fn open(
        bytes: &[u8],
        keyring: &AuthorizationKeyring,
        limits: AuthorizationProofLimits,
    ) -> Result<SignedActorContext, AuthorizationProofError> {
        check_size(bytes, limits)?;
        let proof = SignedActorContext::decode(bytes)
            .map_err(|error| AuthorizationProofError::Decode(error.to_string()))?;
        if proof.encode_to_vec() != bytes {
            return Err(AuthorizationProofError::NonCanonicalProof);
        }
        validate_actor(&proof, limits)?;
        let mut canonical = proof.clone();
        canonicalize_actor(&mut canonical);
        if canonical.roles != proof.roles || canonical.capabilities != proof.capabilities {
            return Err(AuthorizationProofError::NonCanonicalProof);
        }
        verify_digest(&proof.content_hash, actor_digest(&proof))?;
        keyring
            .verify(
                &proof.signing_key_id,
                &actor_digest(&proof),
                &proof.signature,
            )
            .map_err(|error| AuthorizationProofError::Signature(error.to_string()))?;
        Ok(proof)
    }
}

pub struct WorkloadProofCodec;

impl WorkloadProofCodec {
    /// Signs a command-bound workload context.
    ///
    /// # Errors
    ///
    /// Rejects invalid fields or configured proof limits.
    pub fn seal(
        mut proof: SignedWorkloadContext,
        key_id: &str,
        signer: &dyn AuthorizationArtifactSigner,
        limits: AuthorizationProofLimits,
    ) -> Result<Vec<u8>, AuthorizationProofError> {
        validate_key_id(key_id)?.clone_into(&mut proof.signing_key_id);
        validate_workload(&proof)?;
        let digest = workload_digest(&proof);
        proof.content_hash = digest.to_vec();
        proof.signature = signer.sign(&digest);
        encode_bounded(&proof, limits)
    }

    /// Verifies and decodes a canonical workload context.
    ///
    /// # Errors
    ///
    /// Fails closed on malformed, oversized, or invalidly signed data.
    pub fn open(
        bytes: &[u8],
        keyring: &AuthorizationKeyring,
        limits: AuthorizationProofLimits,
    ) -> Result<SignedWorkloadContext, AuthorizationProofError> {
        check_size(bytes, limits)?;
        let proof = SignedWorkloadContext::decode(bytes)
            .map_err(|error| AuthorizationProofError::Decode(error.to_string()))?;
        if proof.encode_to_vec() != bytes {
            return Err(AuthorizationProofError::NonCanonicalProof);
        }
        validate_workload(&proof)?;
        verify_digest(&proof.content_hash, workload_digest(&proof))?;
        keyring
            .verify(
                &proof.signing_key_id,
                &workload_digest(&proof),
                &proof.signature,
            )
            .map_err(|error| AuthorizationProofError::Signature(error.to_string()))?;
        Ok(proof)
    }
}

fn canonicalize_actor(proof: &mut SignedActorContext) {
    proof.roles.sort_unstable();
    proof.roles.dedup();
    proof.capabilities.sort_unstable();
    proof.capabilities.dedup();
}

fn validate_actor(
    proof: &SignedActorContext,
    limits: AuthorizationProofLimits,
) -> Result<(), AuthorizationProofError> {
    validate_common(
        proof.schema_version,
        &proof.tenant_id,
        &proof.command_id,
        proof.issued_at_epoch_ms,
        proof.expires_at_epoch_ms,
        &proof.signing_key_id,
    )?;
    if proof.actor_id.is_empty() || proof.audience_workload_id.is_empty() {
        return Err(AuthorizationProofError::InvalidIdentity);
    }
    if proof.roles.len() > limits.roles {
        return Err(AuthorizationProofError::TooManyRoles {
            actual: proof.roles.len(),
            configured_limit: limits.roles,
        });
    }
    if proof.capabilities.len() > limits.capabilities {
        return Err(AuthorizationProofError::TooManyCapabilities {
            actual: proof.capabilities.len(),
            configured_limit: limits.capabilities,
        });
    }
    if proof.roles.iter().any(String::is_empty) || proof.capabilities.iter().any(String::is_empty) {
        return Err(AuthorizationProofError::InvalidIdentity);
    }
    Ok(())
}

fn validate_workload(proof: &SignedWorkloadContext) -> Result<(), AuthorizationProofError> {
    validate_common(
        proof.schema_version,
        &proof.tenant_id,
        &proof.command_id,
        proof.issued_at_epoch_ms,
        proof.expires_at_epoch_ms,
        &proof.signing_key_id,
    )?;
    if proof.workload_id.is_empty() {
        return Err(AuthorizationProofError::InvalidIdentity);
    }
    Ok(())
}

fn validate_common(
    schema_version: u32,
    tenant_id: &str,
    command_id: &str,
    issued_at_epoch_ms: u64,
    expires_at_epoch_ms: u64,
    key_id: &str,
) -> Result<(), AuthorizationProofError> {
    if schema_version != AUTHORIZATION_PROOF_SCHEMA_VERSION {
        return Err(AuthorizationProofError::UnsupportedSchema(schema_version));
    }
    if tenant_id.is_empty() || command_id.is_empty() || key_id.is_empty() {
        return Err(AuthorizationProofError::InvalidScope);
    }
    if issued_at_epoch_ms >= expires_at_epoch_ms {
        return Err(AuthorizationProofError::InvalidValidityWindow);
    }
    Ok(())
}

fn actor_digest(proof: &SignedActorContext) -> [u8; 32] {
    let mut unsigned = proof.clone();
    unsigned.content_hash.clear();
    unsigned.signature.clear();
    Sha256::digest(unsigned.encode_to_vec()).into()
}

fn workload_digest(proof: &SignedWorkloadContext) -> [u8; 32] {
    let mut unsigned = proof.clone();
    unsigned.content_hash.clear();
    unsigned.signature.clear();
    Sha256::digest(unsigned.encode_to_vec()).into()
}

fn verify_digest(content_hash: &[u8], digest: [u8; 32]) -> Result<(), AuthorizationProofError> {
    if content_hash == digest {
        Ok(())
    } else {
        Err(AuthorizationProofError::HashMismatch)
    }
}

fn encode_bounded<M: Message>(
    proof: &M,
    limits: AuthorizationProofLimits,
) -> Result<Vec<u8>, AuthorizationProofError> {
    let encoded = proof.encode_to_vec();
    check_size(&encoded, limits)?;
    Ok(encoded)
}

fn check_size(
    bytes: &[u8],
    limits: AuthorizationProofLimits,
) -> Result<(), AuthorizationProofError> {
    if bytes.len() > limits.proof_bytes {
        Err(AuthorizationProofError::ProofTooLarge {
            actual: bytes.len(),
            configured_limit: limits.proof_bytes,
        })
    } else {
        Ok(())
    }
}

fn validate_key_id(key_id: &str) -> Result<&str, AuthorizationProofError> {
    if key_id.is_empty() {
        Err(AuthorizationProofError::InvalidScope)
    } else {
        Ok(key_id)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum AuthorizationProofError {
    #[error("authorization proof limits must be greater than zero")]
    InvalidLimits,
    #[error("authorization proof exceeds configured byte limit {configured_limit}: {actual}")]
    ProofTooLarge {
        actual: usize,
        configured_limit: usize,
    },
    #[error("authorization proof has {actual} roles; configured limit is {configured_limit}")]
    TooManyRoles {
        actual: usize,
        configured_limit: usize,
    },
    #[error(
        "authorization proof has {actual} capabilities; configured limit is {configured_limit}"
    )]
    TooManyCapabilities {
        actual: usize,
        configured_limit: usize,
    },
    #[error("authorization proof schema version {0} is unsupported")]
    UnsupportedSchema(u32),
    #[error("authorization proof scope is invalid")]
    InvalidScope,
    #[error("authorization proof identity is invalid")]
    InvalidIdentity,
    #[error("authorization proof validity window is invalid")]
    InvalidValidityWindow,
    #[error("authorization proof cannot be decoded: {0}")]
    Decode(String),
    #[error("authorization proof encoding is not canonical")]
    NonCanonicalProof,
    #[error("authorization proof content hash does not match")]
    HashMismatch,
    #[error("authorization proof signature verification failed: {0}")]
    Signature(String),
}

#[cfg(test)]
mod tests {
    use crate::Ed25519Signer;
    use crate::authorization::v1::SignedActorContext;

    use super::*;

    fn limits() -> AuthorizationProofLimits {
        AuthorizationProofLimits::new(4096, 8, 8).unwrap()
    }

    fn signer_and_keys() -> (Ed25519Signer, AuthorizationKeyring) {
        let signer = Ed25519Signer::from_bytes(&[17; 32]);
        let mut keys = AuthorizationKeyring::new();
        keys.insert("actor-key", &signer.verifying_key_bytes())
            .unwrap();
        (signer, keys)
    }

    fn actor() -> SignedActorContext {
        SignedActorContext {
            schema_version: AUTHORIZATION_PROOF_SCHEMA_VERSION,
            tenant_id: "tenant-a".into(),
            actor_id: "actor-1".into(),
            roles: vec!["writer".into(), "admin".into(), "writer".into()],
            capabilities: vec!["workflow.start".into()],
            revoke_epoch: 3,
            issued_at_epoch_ms: 10,
            expires_at_epoch_ms: 20,
            audience_workload_id: "gateway".into(),
            command_id: "command-1".into(),
            signing_key_id: String::new(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        }
    }

    #[test]
    fn actor_proof_is_canonical_and_rotation_safe() {
        let (signer, keys) = signer_and_keys();
        let bytes = ActorProofCodec::seal(actor(), "actor-key", &signer, limits()).unwrap();
        let opened = ActorProofCodec::open(&bytes, &keys, limits()).unwrap();
        assert_eq!(opened.roles, ["admin", "writer"]);
        assert_eq!(opened.signing_key_id, "actor-key");
    }

    #[test]
    fn actor_proof_tampering_fails_closed() {
        let (signer, keys) = signer_and_keys();
        let mut bytes = ActorProofCodec::seal(actor(), "actor-key", &signer, limits()).unwrap();
        let last = bytes.len() - 1;
        bytes[last] ^= 1;
        assert!(ActorProofCodec::open(&bytes, &keys, limits()).is_err());
    }
}
