//! Fail-closed payload encryption contract used by durable storage adapters.

use bpmp_domain_core::KeyScope;
use thiserror::Error;
use zeroize::Zeroizing;

use aes_gcm::aead::{Aead, AeadCore, KeyInit, OsRng, Payload};
use aes_gcm::{Aes256Gcm, Nonce};

const AES_256_GCM: &str = "AES-256-GCM";
const AES_GCM_NONCE_BYTES: usize = 12;

pub struct ResolvedDataKey {
    pub key_scope: KeyScope,
    pub key_version: String,
    pub key_epoch: u64,
    pub key_bytes: Zeroizing<[u8; 32]>,
}

pub trait DataKeyResolverPort: Send + Sync {
    /// Resolves the current valid key for a new encrypted payload.
    ///
    /// # Errors
    ///
    /// Returns [`CryptoError`] when KMS/cache cannot provide a current key.
    fn resolve_for_encrypt(&self, key_scope: &KeyScope) -> Result<ResolvedDataKey, CryptoError>;

    /// Resolves the exact historical key referenced by a stored payload.
    ///
    /// # Errors
    ///
    /// Returns [`CryptoError`] for unavailable, revoked, or stale key material.
    fn resolve_for_decrypt(
        &self,
        key_scope: &KeyScope,
        key_version: &str,
        key_epoch: u64,
    ) -> Result<ResolvedDataKey, CryptoError>;
}

pub struct AesGcmPayloadCrypto<R> {
    resolver: R,
}

impl<R> AesGcmPayloadCrypto<R> {
    pub const fn new(resolver: R) -> Self {
        Self { resolver }
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct EncryptionContext<'a> {
    pub key_scope: &'a KeyScope,
    pub associated_data: &'a [u8],
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct EncryptedPayload {
    pub key_scope: KeyScope,
    pub key_version: String,
    pub key_epoch: u64,
    pub algorithm: String,
    pub nonce: Vec<u8>,
    pub ciphertext: Vec<u8>,
}

pub trait PayloadCryptoPort: Send + Sync {
    /// Encrypts plaintext before any durable write is constructed.
    ///
    /// # Errors
    ///
    /// Returns [`CryptoError`] when no current key can be resolved or encryption
    /// fails. Callers must abort the entire write and never persist plaintext.
    fn encrypt(
        &self,
        context: EncryptionContext<'_>,
        plaintext: &[u8],
    ) -> Result<EncryptedPayload, CryptoError>;

    /// Authenticates and decrypts a stored payload.
    ///
    /// # Errors
    ///
    /// Returns [`CryptoError`] for unavailable/revoked keys, stale epochs, or
    /// authentication failure. Callers must not return partial plaintext.
    fn decrypt(
        &self,
        associated_data: &[u8],
        payload: &EncryptedPayload,
    ) -> Result<Vec<u8>, CryptoError>;
}

impl<R: DataKeyResolverPort> PayloadCryptoPort for AesGcmPayloadCrypto<R> {
    fn encrypt(
        &self,
        context: EncryptionContext<'_>,
        plaintext: &[u8],
    ) -> Result<EncryptedPayload, CryptoError> {
        let key = self.resolver.resolve_for_encrypt(context.key_scope)?;
        if key.key_scope != *context.key_scope {
            return Err(CryptoError::InvalidMetadata);
        }
        let cipher = Aes256Gcm::new_from_slice(key.key_bytes.as_ref())
            .map_err(|_| CryptoError::InvalidMetadata)?;
        let nonce = Aes256Gcm::generate_nonce(&mut OsRng);
        let ciphertext = cipher
            .encrypt(
                &nonce,
                Payload {
                    msg: plaintext,
                    aad: context.associated_data,
                },
            )
            .map_err(|_| CryptoError::EncryptionFailed)?;
        Ok(EncryptedPayload {
            key_scope: key.key_scope,
            key_version: key.key_version,
            key_epoch: key.key_epoch,
            algorithm: AES_256_GCM.into(),
            nonce: nonce.to_vec(),
            ciphertext,
        })
    }

    fn decrypt(
        &self,
        associated_data: &[u8],
        payload: &EncryptedPayload,
    ) -> Result<Vec<u8>, CryptoError> {
        if payload.algorithm != AES_256_GCM {
            return Err(CryptoError::InvalidMetadata);
        }
        if payload.nonce.len() != AES_GCM_NONCE_BYTES {
            return Err(CryptoError::InvalidMetadata);
        }
        let key = self.resolver.resolve_for_decrypt(
            &payload.key_scope,
            &payload.key_version,
            payload.key_epoch,
        )?;
        if key.key_scope != payload.key_scope
            || key.key_version != payload.key_version
            || key.key_epoch != payload.key_epoch
        {
            return Err(CryptoError::InvalidMetadata);
        }
        let cipher = Aes256Gcm::new_from_slice(key.key_bytes.as_ref())
            .map_err(|_| CryptoError::InvalidMetadata)?;
        cipher
            .decrypt(
                Nonce::from_slice(&payload.nonce),
                Payload {
                    msg: &payload.ciphertext,
                    aad: associated_data,
                },
            )
            .map_err(|_| CryptoError::DecryptionFailed)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum CryptoError {
    #[error("no valid encryption key is available")]
    KeyUnavailable,
    #[error("encryption key has been revoked")]
    KeyRevoked,
    #[error("encrypted payload key epoch is stale")]
    StaleKeyEpoch,
    #[error("payload encryption failed")]
    EncryptionFailed,
    #[error("payload authentication or decryption failed")]
    DecryptionFailed,
    #[error("encrypted payload metadata is invalid")]
    InvalidMetadata,
}

#[cfg(test)]
mod tests {
    use super::*;

    struct StaticResolver {
        unavailable: bool,
    }

    impl StaticResolver {
        fn key(scope: &KeyScope) -> ResolvedDataKey {
            ResolvedDataKey {
                key_scope: scope.clone(),
                key_version: "test-v1".into(),
                key_epoch: 7,
                key_bytes: Zeroizing::new([19; 32]),
            }
        }
    }

    impl DataKeyResolverPort for StaticResolver {
        fn resolve_for_encrypt(
            &self,
            key_scope: &KeyScope,
        ) -> Result<ResolvedDataKey, CryptoError> {
            if self.unavailable {
                Err(CryptoError::KeyUnavailable)
            } else {
                Ok(Self::key(key_scope))
            }
        }

        fn resolve_for_decrypt(
            &self,
            key_scope: &KeyScope,
            _key_version: &str,
            _key_epoch: u64,
        ) -> Result<ResolvedDataKey, CryptoError> {
            self.resolve_for_encrypt(key_scope)
        }
    }

    #[test]
    fn aes_gcm_round_trip_binds_ciphertext_to_associated_data() {
        let crypto = AesGcmPayloadCrypto::new(StaticResolver { unavailable: false });
        let scope = KeyScope::new("tenant-a/subject-1").unwrap();
        let encrypted = crypto
            .encrypt(
                EncryptionContext {
                    key_scope: &scope,
                    associated_data: b"tenant-a/stream-1/1",
                },
                b"sensitive payload",
            )
            .unwrap();
        assert_ne!(encrypted.ciphertext, b"sensitive payload");
        assert_eq!(
            crypto.decrypt(b"tenant-a/stream-1/1", &encrypted).unwrap(),
            b"sensitive payload"
        );
        assert_eq!(
            crypto.decrypt(b"tenant-b/stream-1/1", &encrypted),
            Err(CryptoError::DecryptionFailed)
        );
    }

    #[test]
    fn unavailable_key_fails_closed_before_ciphertext_exists() {
        let crypto = AesGcmPayloadCrypto::new(StaticResolver { unavailable: true });
        let scope = KeyScope::new("tenant-a/operational").unwrap();
        assert_eq!(
            crypto.encrypt(
                EncryptionContext {
                    key_scope: &scope,
                    associated_data: b"aad",
                },
                b"plaintext",
            ),
            Err(CryptoError::KeyUnavailable)
        );
    }
}
