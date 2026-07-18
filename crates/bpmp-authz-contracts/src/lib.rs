//! BPMP authorization wire contracts and signed artifact codecs.

pub mod authorization {
    #[allow(clippy::doc_markdown)]
    pub mod v1 {
        include!(concat!(env!("OUT_DIR"), "/bpmp.authorization.v1.rs"));
    }
}

mod authorization_artifact;
mod authorization_proof;
mod signing;

pub use authorization_artifact::{
    AUTHORIZATION_BUNDLE_SCHEMA_VERSION, AuthorizationArtifactError, AuthorizationArtifactLimits,
    AuthorizationArtifactSigner, AuthorizationBundleCodec, AuthorizationKeyring,
    AuthorizationRevokeCodec,
};
pub use authorization_proof::{
    AUTHORIZATION_PROOF_SCHEMA_VERSION, ActorProofCodec, AuthorizationProofError,
    AuthorizationProofLimits, WorkloadProofCodec,
};
pub use signing::Ed25519Signer;
