use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use prost::Message;
use sha2::{Digest, Sha256};
use thiserror::Error;

use crate::wir::v1::WorkflowIntermediateRepresentation;

/// Immutable wire version for `bpmp.wir.v1`.
pub const WIR_SCHEMA_VERSION: u32 = 1;

pub trait WirArtifactSigner {
    fn sign(&self, digest: &[u8; 32]) -> Vec<u8>;
}

pub trait WirArtifactVerifier {
    /// Verifies an artifact signature against its canonical digest.
    ///
    /// # Errors
    ///
    /// Returns [`ArtifactError::InvalidSignature`] when verification fails.
    fn verify(&self, digest: &[u8; 32], signature: &[u8]) -> Result<(), ArtifactError>;
}

pub struct Ed25519Signer(SigningKey);

impl Ed25519Signer {
    pub fn from_bytes(bytes: &[u8; 32]) -> Self {
        Self(SigningKey::from_bytes(bytes))
    }

    pub fn verifying_key_bytes(&self) -> [u8; 32] {
        self.0.verifying_key().to_bytes()
    }
}

impl WirArtifactSigner for Ed25519Signer {
    fn sign(&self, digest: &[u8; 32]) -> Vec<u8> {
        self.0.sign(digest).to_bytes().to_vec()
    }
}

pub struct Ed25519Verifier(VerifyingKey);

impl Ed25519Verifier {
    /// Constructs a verifier from an Ed25519 public key.
    ///
    /// # Errors
    ///
    /// Returns [`ArtifactError::InvalidPublicKey`] for malformed key bytes.
    pub fn from_bytes(bytes: &[u8; 32]) -> Result<Self, ArtifactError> {
        VerifyingKey::from_bytes(bytes)
            .map(Self)
            .map_err(|_| ArtifactError::InvalidPublicKey)
    }
}

impl WirArtifactVerifier for Ed25519Verifier {
    fn verify(&self, digest: &[u8; 32], signature: &[u8]) -> Result<(), ArtifactError> {
        let signature =
            Signature::try_from(signature).map_err(|_| ArtifactError::InvalidSignature)?;
        self.0
            .verify(digest, &signature)
            .map_err(|_| ArtifactError::InvalidSignature)
    }
}

pub struct WirCodec;

impl WirCodec {
    /// Canonicalizes, hashes, signs, and serializes a WIR artifact.
    ///
    /// # Errors
    ///
    /// Returns an error when the WIR schema version is unsupported.
    pub fn seal(
        mut wir: WorkflowIntermediateRepresentation,
        signer: &dyn WirArtifactSigner,
    ) -> Result<Vec<u8>, ArtifactError> {
        validate_schema(wir.schema_version)?;
        canonicalize(&mut wir);
        let digest = digest_unsigned(&wir);
        wir.content_hash = digest.to_vec();
        wir.signature = signer.sign(&digest);
        Ok(wir.encode_to_vec())
    }

    /// Decodes and verifies a signed WIR artifact before returning it.
    ///
    /// # Errors
    ///
    /// Returns an error for malformed bytes, unsupported schema, non-canonical
    /// ordering, hash mismatch, or invalid signature.
    pub fn open(
        bytes: &[u8],
        verifier: &dyn WirArtifactVerifier,
    ) -> Result<WorkflowIntermediateRepresentation, ArtifactError> {
        let wir = WorkflowIntermediateRepresentation::decode(bytes)
            .map_err(|error| ArtifactError::Decode(error.to_string()))?;
        validate_schema(wir.schema_version)?;
        let mut canonical = wir.clone();
        canonicalize(&mut canonical);
        if canonical != wir {
            return Err(ArtifactError::NonCanonicalArtifact);
        }
        let digest = digest_unsigned(&wir);
        if wir.content_hash.as_slice() != digest {
            return Err(ArtifactError::HashMismatch);
        }
        verifier.verify(&digest, &wir.signature)?;
        Ok(wir)
    }
}

fn validate_schema(schema_version: u32) -> Result<(), ArtifactError> {
    if schema_version == WIR_SCHEMA_VERSION {
        Ok(())
    } else {
        Err(ArtifactError::UnsupportedSchema {
            expected: WIR_SCHEMA_VERSION,
            actual: schema_version,
        })
    }
}

fn canonicalize(wir: &mut WorkflowIntermediateRepresentation) {
    wir.nodes
        .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    wir.decision_tables
        .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    sort_properties(&mut wir.properties);
    wir.case_models
        .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    for model in &mut wir.case_models {
        model
            .stages
            .sort_unstable_by(|left, right| left.id.cmp(&right.id));
        model
            .milestones
            .sort_unstable_by(|left, right| left.id.cmp(&right.id));
        model
            .sentries
            .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    }
    for node in &mut wir.nodes {
        sort_properties(&mut node.properties);
        node.boundary_events
            .sort_unstable_by(|left, right| left.id.cmp(&right.id));
        match &mut node.kind {
            Some(crate::wir::v1::node::Kind::ExclusiveGateway(gateway)) => gateway
                .transitions
                .sort_unstable_by(|left, right| left.target_node_id.cmp(&right.target_node_id)),
            Some(crate::wir::v1::node::Kind::InclusiveGateway(gateway)) => gateway
                .transitions
                .sort_unstable_by(|left, right| left.target_node_id.cmp(&right.target_node_id)),
            Some(crate::wir::v1::node::Kind::ParallelGateway(gateway)) => {
                gateway.target_node_ids.sort_unstable();
            }
            _ => {}
        }
    }
    for table in &mut wir.decision_tables {
        table
            .rules
            .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    }
}

fn sort_properties(properties: &mut [crate::wir::v1::ExtensionProperty]) {
    properties.sort_unstable_by(|left, right| {
        (
            left.namespace_uri.as_str(),
            left.element_name.as_str(),
            left.name.as_str(),
        )
            .cmp(&(
                right.namespace_uri.as_str(),
                right.element_name.as_str(),
                right.name.as_str(),
            ))
    });
}

fn digest_unsigned(wir: &WorkflowIntermediateRepresentation) -> [u8; 32] {
    let mut unsigned = wir.clone();
    unsigned.content_hash.clear();
    unsigned.signature.clear();
    Sha256::digest(unsigned.encode_to_vec()).into()
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum ArtifactError {
    #[error("unsupported WIR schema version {actual}; expected {expected}")]
    UnsupportedSchema { expected: u32, actual: u32 },
    #[error("WIR artifact cannot be decoded: {0}")]
    Decode(String),
    #[error("WIR artifact ordering is not canonical")]
    NonCanonicalArtifact,
    #[error("WIR artifact content hash does not match its payload")]
    HashMismatch,
    #[error("WIR artifact signature is invalid")]
    InvalidSignature,
    #[error("Ed25519 public key is invalid")]
    InvalidPublicKey,
}

#[cfg(test)]
mod tests {
    use crate::wir::v1::{EndNode, Node, WorkflowIntermediateRepresentation, node};

    use super::*;

    fn artifact() -> WorkflowIntermediateRepresentation {
        WorkflowIntermediateRepresentation {
            schema_version: WIR_SCHEMA_VERSION,
            workflow_type: "order".into(),
            workflow_version: "1".into(),
            start_node_id: "end".into(),
            nodes: vec![Node {
                id: "end".into(),
                kind: Some(node::Kind::End(EndNode {})),
                data_contract: None,
                sla_milliseconds: 0,
                compensation_handler_id: String::new(),
                properties: Vec::new(),
                multi_instance: None,
                boundary_events: Vec::new(),
                owner_scope_id: String::new(),
            }],
            content_hash: Vec::new(),
            signature: Vec::new(),
            decision_tables: Vec::new(),
            tenant_id: "tenant-a".into(),
            case_models: Vec::new(),
            properties: Vec::new(),
        }
    }

    #[test]
    fn signed_artifact_round_trips_and_tampering_fails_closed() {
        let signer = Ed25519Signer::from_bytes(&[5; 32]);
        let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
        let encoded = WirCodec::seal(artifact(), &signer).unwrap();
        assert_eq!(
            WirCodec::open(&encoded, &verifier).unwrap().workflow_type,
            "order"
        );

        let mut tampered = encoded;
        let last = tampered.len() - 1;
        tampered[last] ^= 1;
        assert!(WirCodec::open(&tampered, &verifier).is_err());
    }
}
