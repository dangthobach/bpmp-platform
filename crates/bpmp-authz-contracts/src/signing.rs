use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};

pub struct Ed25519Signer(SigningKey);

impl Ed25519Signer {
    pub fn from_bytes(bytes: &[u8; 32]) -> Self {
        Self(SigningKey::from_bytes(bytes))
    }

    pub fn verifying_key_bytes(&self) -> [u8; 32] {
        self.0.verifying_key().to_bytes()
    }

    pub(crate) fn sign_digest(&self, digest: &[u8; 32]) -> Vec<u8> {
        self.0.sign(digest).to_bytes().to_vec()
    }
}

pub(crate) struct Ed25519Verifier(VerifyingKey);

impl Ed25519Verifier {
    pub(crate) fn from_bytes(bytes: &[u8; 32]) -> Result<Self, ()> {
        VerifyingKey::from_bytes(bytes).map(Self).map_err(|_| ())
    }

    pub(crate) fn verify_digest(&self, digest: &[u8; 32], signature: &[u8]) -> Result<(), ()> {
        let signature = Signature::try_from(signature).map_err(|_| ())?;
        self.0.verify(digest, &signature).map_err(|_| ())
    }
}
