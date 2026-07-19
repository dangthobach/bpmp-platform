//! Generated durable contracts and integrity-checked WIR artifact codec.

pub use bpmp_authz_contracts::authorization;

pub mod configuration {
    #[allow(clippy::doc_markdown)]
    pub mod v1 {
        include!(concat!(env!("OUT_DIR"), "/bpmp.configuration.v1.rs"));
    }
}

pub mod engine {
    #[allow(
        clippy::default_trait_access,
        clippy::doc_markdown,
        clippy::missing_errors_doc
    )]
    pub mod v1 {
        include!(concat!(env!("OUT_DIR"), "/bpmp.engine.v1.rs"));
    }
}

pub mod storage {
    #[allow(clippy::doc_markdown)]
    pub mod v1 {
        include!(concat!(env!("OUT_DIR"), "/bpmp.storage.v1.rs"));
    }
}

pub mod wir {
    #[allow(clippy::doc_markdown)]
    pub mod v1 {
        include!(concat!(env!("OUT_DIR"), "/bpmp.wir.v1.rs"));
    }
}

mod wir_artifact;

pub use bpmp_authz_contracts::{
    AUTHORIZATION_BUNDLE_SCHEMA_VERSION, AUTHORIZATION_PROOF_SCHEMA_VERSION, ActorProofCodec,
    AuthorizationArtifactError, AuthorizationArtifactLimits, AuthorizationArtifactSigner,
    AuthorizationBundleCodec, AuthorizationKeyring, AuthorizationProofError,
    AuthorizationProofLimits, AuthorizationRevokeCodec, WorkloadProofCodec,
};

pub use wir_artifact::{
    ArtifactError, Ed25519Signer, Ed25519Verifier, WIR_SCHEMA_VERSION, WirArtifactSigner,
    WirArtifactVerifier, WirCodec,
};
