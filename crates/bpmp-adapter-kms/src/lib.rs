//! Production KMS adapters and the bounded, revocation-aware DEK cache.

use std::collections::{BTreeMap, VecDeque};
use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;
use std::time::{Duration, Instant};

use base64::Engine as _;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use bpmp_domain_core::KeyScope;
use bpmp_payload_crypto::{CryptoError, DataKeyResolverPort, ResolvedDataKey};
use reqwest::StatusCode;
use reqwest::blocking::{Client, RequestBuilder};
use serde::{Deserialize, Serialize};
use thiserror::Error;
use zeroize::{Zeroize, Zeroizing};

const DATA_KEY_BYTES: usize = 32;

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct CacheKey {
    scope: KeyScope,
    version: String,
    epoch: u64,
}

#[derive(Debug)]
struct CacheEntry {
    key: Zeroizing<[u8; DATA_KEY_BYTES]>,
    expires_at: Instant,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct WrappedDataKey {
    pub scope: KeyScope,
    pub version: String,
    pub epoch: u64,
    pub ciphertext: String,
}

/// Provider-neutral operations required by the engine's data-key resolver.
pub trait KeyManagementPort: Send + Sync {
    /// Loads the current wrapped DEK for new writes.
    ///
    /// # Errors
    /// Returns an error when the provider cannot supply an authenticated record.
    fn current(&self, scope: &KeyScope) -> Result<WrappedDataKey, KmsError>;

    /// Loads the immutable wrapped DEK version referenced by durable ciphertext.
    ///
    /// # Errors
    /// Returns an error when the version is missing, revoked, or malformed.
    fn version(
        &self,
        scope: &KeyScope,
        version: &str,
        epoch: u64,
    ) -> Result<WrappedDataKey, KmsError>;

    /// Unwraps one DEK. Implementations must never log plaintext or credentials.
    ///
    /// # Errors
    /// Returns an error when KMS rejects the ciphertext or returns invalid key material.
    fn unwrap(&self, wrapped: &WrappedDataKey)
    -> Result<Zeroizing<[u8; DATA_KEY_BYTES]>, KmsError>;
}

/// Cache-side half of the governance revocation barrier.
pub trait RevocationBarrierPort: Send + Sync {
    /// Fences all cached and future lookups at or below `epoch`.
    ///
    /// # Errors
    /// Returns an error if the barrier cannot be installed atomically in this process.
    fn revoke_through(&self, scope: &KeyScope, epoch: u64) -> Result<(), KmsError>;
}

#[derive(Debug)]
struct CacheState {
    entries: BTreeMap<CacheKey, CacheEntry>,
    insertion_order: VecDeque<CacheKey>,
    current: BTreeMap<KeyScope, CacheKey>,
    revoked_through: BTreeMap<KeyScope, u64>,
}

/// Bounded DEK cache keyed by `(scope, version, epoch)` with a local revocation fence.
pub struct CachedDataKeyResolver<K> {
    kms: K,
    capacity: usize,
    ttl: Duration,
    state: Mutex<CacheState>,
}

impl<K> CachedDataKeyResolver<K> {
    /// Creates a resolver. Zero capacity or TTL are rejected because they usually
    /// indicate an accidentally incomplete production configuration.
    ///
    /// # Errors
    /// Returns [`KmsError::InvalidConfiguration`] for a zero capacity or TTL.
    pub fn new(kms: K, capacity: usize, ttl: Duration) -> Result<Self, KmsError> {
        if capacity == 0 || ttl.is_zero() {
            return Err(KmsError::InvalidConfiguration(
                "cache capacity and TTL must be positive",
            ));
        }
        Ok(Self {
            kms,
            capacity,
            ttl,
            state: Mutex::new(CacheState {
                entries: BTreeMap::new(),
                insertion_order: VecDeque::new(),
                current: BTreeMap::new(),
                revoked_through: BTreeMap::new(),
            }),
        })
    }

    /// Installs a revocation barrier before governance destroys wrapped DEKs.
    /// After return, this resolver cannot serve any cached key at or below `epoch`.
    ///
    /// # Errors
    /// Returns [`KmsError::CachePoisoned`] if another thread panicked while holding the cache.
    pub fn revoke_through(&self, scope: &KeyScope, epoch: u64) -> Result<(), KmsError> {
        let mut state = self.state.lock().map_err(|_| KmsError::CachePoisoned)?;
        state
            .revoked_through
            .entry(scope.clone())
            .and_modify(|current| *current = (*current).max(epoch))
            .or_insert(epoch);
        state
            .entries
            .retain(|key, _| key.scope != *scope || key.epoch > epoch);
        state
            .insertion_order
            .retain(|key| key.scope != *scope || key.epoch > epoch);
        if state
            .current
            .get(scope)
            .is_some_and(|key| key.epoch <= epoch)
        {
            state.current.remove(scope);
        }
        Ok(())
    }

    fn cached(&self, key: &CacheKey) -> Result<Option<ResolvedDataKey>, CryptoError> {
        let mut state = self.state.lock().map_err(|_| CryptoError::KeyUnavailable)?;
        if state
            .revoked_through
            .get(&key.scope)
            .is_some_and(|epoch| key.epoch <= *epoch)
        {
            return Err(CryptoError::KeyRevoked);
        }
        let now = Instant::now();
        let Some(entry) = state.entries.get(key) else {
            return Ok(None);
        };
        if entry.expires_at <= now {
            state.entries.remove(key);
            state.insertion_order.retain(|candidate| candidate != key);
            state.current.retain(|_, candidate| candidate != key);
            return Ok(None);
        }
        Ok(Some(ResolvedDataKey {
            key_scope: key.scope.clone(),
            key_version: key.version.clone(),
            key_epoch: key.epoch,
            key_bytes: Zeroizing::new(*entry.key),
        }))
    }

    fn insert(
        &self,
        wrapped: &WrappedDataKey,
        bytes: &[u8; DATA_KEY_BYTES],
    ) -> Result<(), KmsError> {
        let key = CacheKey {
            scope: wrapped.scope.clone(),
            version: wrapped.version.clone(),
            epoch: wrapped.epoch,
        };
        let mut state = self.state.lock().map_err(|_| KmsError::CachePoisoned)?;
        if state
            .revoked_through
            .get(&key.scope)
            .is_some_and(|epoch| key.epoch <= *epoch)
        {
            return Err(KmsError::Revoked);
        }
        if !state.entries.contains_key(&key) {
            state.insertion_order.push_back(key.clone());
        }
        state.entries.insert(
            key.clone(),
            CacheEntry {
                key: Zeroizing::new(*bytes),
                expires_at: Instant::now() + self.ttl,
            },
        );
        state.current.insert(key.scope.clone(), key.clone());
        while state.entries.len() > self.capacity {
            if let Some(evicted) = state.insertion_order.pop_front() {
                state.entries.remove(&evicted);
                state.current.retain(|_, current| current != &evicted);
            }
        }
        Ok(())
    }

    fn resolve(&self, wrapped: WrappedDataKey) -> Result<ResolvedDataKey, CryptoError>
    where
        K: KeyManagementPort,
    {
        let cache_key = CacheKey {
            scope: wrapped.scope.clone(),
            version: wrapped.version.clone(),
            epoch: wrapped.epoch,
        };
        if let Some(cached) = self.cached(&cache_key)? {
            return Ok(cached);
        }
        let bytes = self
            .kms
            .unwrap(&wrapped)
            .map_err(|error| map_kms_error(&error))?;
        self.insert(&wrapped, &bytes)
            .map_err(|error| map_kms_error(&error))?;
        Ok(ResolvedDataKey {
            key_scope: wrapped.scope,
            key_version: wrapped.version,
            key_epoch: wrapped.epoch,
            key_bytes: bytes,
        })
    }
}

impl<K: Send + Sync> RevocationBarrierPort for CachedDataKeyResolver<K> {
    fn revoke_through(&self, scope: &KeyScope, epoch: u64) -> Result<(), KmsError> {
        Self::revoke_through(self, scope, epoch)
    }
}

impl<K: KeyManagementPort> DataKeyResolverPort for CachedDataKeyResolver<K> {
    fn resolve_for_encrypt(&self, scope: &KeyScope) -> Result<ResolvedDataKey, CryptoError> {
        let current_key = {
            let state = self.state.lock().map_err(|_| CryptoError::KeyUnavailable)?;
            state.current.get(scope).cloned()
        };
        if let Some(key) = current_key
            && let Some(cached) = self.cached(&key)?
        {
            return Ok(cached);
        }
        let wrapped = self
            .kms
            .current(scope)
            .map_err(|error| map_kms_error(&error))?;
        if wrapped.scope != *scope {
            return Err(CryptoError::InvalidMetadata);
        }
        self.resolve(wrapped)
    }

    fn resolve_for_decrypt(
        &self,
        scope: &KeyScope,
        version: &str,
        epoch: u64,
    ) -> Result<ResolvedDataKey, CryptoError> {
        let key = CacheKey {
            scope: scope.clone(),
            version: version.to_owned(),
            epoch,
        };
        if let Some(cached) = self.cached(&key)? {
            return Ok(cached);
        }
        let wrapped = self
            .kms
            .version(scope, version, epoch)
            .map_err(|error| map_kms_error(&error))?;
        if wrapped.scope != *scope || wrapped.version != version || wrapped.epoch != epoch {
            return Err(CryptoError::InvalidMetadata);
        }
        self.resolve(wrapped)
    }
}

fn map_kms_error(error: &KmsError) -> CryptoError {
    match error {
        KmsError::Revoked | KmsError::NotFound => CryptoError::KeyRevoked,
        KmsError::InvalidResponse(_) => CryptoError::InvalidMetadata,
        KmsError::InvalidConfiguration(_)
        | KmsError::Io(_)
        | KmsError::Http(_)
        | KmsError::CachePoisoned => CryptoError::KeyUnavailable,
    }
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VaultTransitConfig {
    pub address: String,
    pub token_path: PathBuf,
    pub namespace: Option<String>,
    pub transit_mount: String,
    pub kv_mount: String,
    pub master_key_name: String,
    pub request_timeout_ms: u64,
    pub cache_capacity: usize,
    pub cache_ttl_ms: u64,
}

impl VaultTransitConfig {
    /// Validates security-sensitive Vault addressing and resource bounds.
    ///
    /// # Errors
    /// Returns [`KmsError::InvalidConfiguration`] for incomplete or non-TLS configuration.
    pub fn validate(&self) -> Result<(), KmsError> {
        if !self.address.starts_with("https://")
            || self.token_path.as_os_str().is_empty()
            || !valid_vault_segment(&self.transit_mount)
            || !valid_vault_segment(&self.kv_mount)
            || !valid_vault_segment(&self.master_key_name)
            || self.request_timeout_ms == 0
            || self.cache_capacity == 0
            || self.cache_ttl_ms == 0
            || self
                .namespace
                .as_ref()
                .is_some_and(|value| value.trim().is_empty())
        {
            return Err(KmsError::InvalidConfiguration(
                "Vault requires HTTPS, valid mount/key segments, token path, timeout, and cache bounds",
            ));
        }
        Ok(())
    }
}

fn valid_vault_segment(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
}

/// Vault Transit supplies wrapping/unwrapping; KV v2 durably stores wrapped DEKs.
/// The engine identity only needs KV read and Transit decrypt capabilities.
pub struct VaultTransitKeyManagement {
    client: Client,
    config: VaultTransitConfig,
}

impl VaultTransitKeyManagement {
    /// Builds a TLS-only Vault client and reads its token from the configured file.
    ///
    /// # Errors
    /// Returns an error for invalid configuration, unreadable credentials, or client setup.
    pub fn load(config: VaultTransitConfig) -> Result<Self, KmsError> {
        config.validate()?;
        let token = fs::read_to_string(&config.token_path)
            .map_err(|error| {
                tracing::warn!(
                    path = %config.token_path.display(),
                    %error,
                    "vault token file unreadable during initialization"
                );
                KmsError::Io(error)
            })?
            .trim()
            .to_owned();
        if token.is_empty() {
            return Err(KmsError::InvalidConfiguration("Vault token is empty"));
        }
        let client = Client::builder()
            .timeout(Duration::from_millis(config.request_timeout_ms))
            .https_only(true)
            .build()
            .map_err(KmsError::Http)?;
        drop(Zeroizing::new(token));
        Ok(Self { client, config })
    }

    /// Wraps this provider in the configured bounded, expiring cache.
    ///
    /// # Errors
    /// Returns an error when cache bounds are invalid.
    pub fn into_cached_resolver(self) -> Result<CachedDataKeyResolver<Self>, KmsError> {
        let capacity = self.config.cache_capacity;
        let ttl = Duration::from_millis(self.config.cache_ttl_ms);
        CachedDataKeyResolver::new(self, capacity, ttl)
    }

    /// Generates and persists a new wrapped DEK. Governance credentials need
    /// Transit `update` on `datakey/plaintext` and KV v2 `create`/`update`.
    /// `cas_version` must be zero for first creation or the current KV version
    /// for rotation, preventing concurrent writers from losing an epoch.
    ///
    /// # Errors
    /// Returns an error for invalid epochs, Vault failures, CAS conflicts, or malformed responses.
    pub fn create_or_rotate(
        &self,
        scope: &KeyScope,
        epoch: u64,
        cas_version: u64,
    ) -> Result<WrappedDataKey, KmsError> {
        if epoch == 0 {
            return Err(KmsError::InvalidConfiguration(
                "data-key epoch must be positive",
            ));
        }
        let data_key_url = format!(
            "{}/v1/{}/datakey/plaintext/{}",
            self.config.address.trim_end_matches('/'),
            self.config.transit_mount,
            self.config.master_key_name
        );
        let response = self
            .request(
                self.client
                    .post(data_key_url)
                    .json(&GenerateDataKeyRequest { bits: 256 }),
            )?
            .send()
            .map_err(KmsError::Http)?
            .error_for_status()
            .map_err(KmsError::Http)?;
        let mut envelope: GenerateDataKeyEnvelope = response.json().map_err(KmsError::Http)?;
        let mut plaintext = STANDARD
            .decode(&envelope.data.plaintext)
            .map_err(|_| KmsError::InvalidResponse("invalid generated DEK base64"))?;
        let valid_length = plaintext.len() == DATA_KEY_BYTES;
        plaintext.zeroize();
        envelope.data.plaintext.zeroize();
        if !valid_length {
            return Err(KmsError::InvalidResponse(
                "generated DEK must contain exactly 32 bytes",
            ));
        }

        let stored = StoredDataKey {
            key_scope: scope.as_str().to_owned(),
            key_epoch: epoch,
            wrapped_dek: envelope.data.ciphertext.clone(),
        };
        let response = self
            .request(
                self.client
                    .post(self.record_url(scope))
                    .json(&KvWriteRequest {
                        options: KvWriteOptions { cas: cas_version },
                        data: &stored,
                    }),
            )?
            .send()
            .map_err(KmsError::Http)?
            .error_for_status()
            .map_err(KmsError::Http)?;
        let written: KvWriteEnvelope = response.json().map_err(KmsError::Http)?;
        if written.data.version == 0 {
            return Err(KmsError::InvalidResponse("Vault returned KV version zero"));
        }
        Ok(WrappedDataKey {
            scope: scope.clone(),
            version: written.data.version.to_string(),
            epoch,
            ciphertext: envelope.data.ciphertext,
        })
    }

    /// Irreversibly removes every KV version of a scope's wrapped DEK. Callers
    /// must first obtain revocation-barrier acknowledgement from every engine.
    ///
    /// # Errors
    /// Returns an error when Vault does not acknowledge the metadata deletion.
    pub fn shred(&self, scope: &KeyScope) -> Result<(), KmsError> {
        let reference = URL_SAFE_NO_PAD.encode(scope.as_str().as_bytes());
        let url = format!(
            "{}/v1/{}/metadata/{}",
            self.config.address.trim_end_matches('/'),
            self.config.kv_mount,
            reference
        );
        let response = self
            .request(self.client.delete(url))?
            .send()
            .map_err(KmsError::Http)?;
        if response.status() == StatusCode::NOT_FOUND {
            return Ok(());
        }
        response.error_for_status().map_err(KmsError::Http)?;
        Ok(())
    }

    fn request(&self, request: RequestBuilder) -> Result<RequestBuilder, KmsError> {
        let token = Zeroizing::new(
            fs::read_to_string(&self.config.token_path)
                .map_err(|error| {
                    tracing::warn!(
                        path = %self.config.token_path.display(),
                        %error,
                        "vault token file unreadable; may be mid-rotation"
                    );
                    KmsError::Io(error)
                })?
                .trim()
                .to_owned(),
        );
        if token.is_empty() {
            return Err(KmsError::InvalidConfiguration("Vault token is empty"));
        }
        let request = request.header("X-Vault-Token", token.as_str());
        Ok(match &self.config.namespace {
            Some(namespace) => request.header("X-Vault-Namespace", namespace),
            None => request,
        })
    }

    fn record_url(&self, scope: &KeyScope) -> String {
        let reference = URL_SAFE_NO_PAD.encode(scope.as_str().as_bytes());
        format!(
            "{}/v1/{}/data/{}",
            self.config.address.trim_end_matches('/'),
            self.config.kv_mount,
            reference
        )
    }

    fn read_record(
        &self,
        scope: &KeyScope,
        version: Option<&str>,
    ) -> Result<WrappedDataKey, KmsError> {
        let mut request = self.client.get(self.record_url(scope));
        if let Some(version) = version {
            request = request.query(&[("version", version)]);
        }
        let response = self.request(request)?.send().map_err(KmsError::Http)?;
        if response.status() == StatusCode::NOT_FOUND {
            return Err(KmsError::NotFound);
        }
        let response = response.error_for_status().map_err(KmsError::Http)?;
        let envelope: KvReadEnvelope = response.json().map_err(KmsError::Http)?;
        let stored_scope = KeyScope::new(envelope.data.data.key_scope)
            .map_err(|_| KmsError::InvalidResponse("invalid stored key scope"))?;
        if stored_scope != *scope || envelope.data.metadata.version == 0 {
            return Err(KmsError::InvalidResponse("Vault KV metadata mismatch"));
        }
        Ok(WrappedDataKey {
            scope: stored_scope,
            version: envelope.data.metadata.version.to_string(),
            epoch: envelope.data.data.key_epoch,
            ciphertext: envelope.data.data.wrapped_dek,
        })
    }
}

impl KeyManagementPort for VaultTransitKeyManagement {
    fn current(&self, scope: &KeyScope) -> Result<WrappedDataKey, KmsError> {
        self.read_record(scope, None)
    }

    fn version(
        &self,
        scope: &KeyScope,
        version: &str,
        epoch: u64,
    ) -> Result<WrappedDataKey, KmsError> {
        if version.parse::<u64>().is_err() {
            return Err(KmsError::InvalidResponse("invalid KV version"));
        }
        let record = self.read_record(scope, Some(version))?;
        if record.epoch != epoch {
            return Err(KmsError::InvalidResponse("stored key epoch mismatch"));
        }
        Ok(record)
    }

    fn unwrap(
        &self,
        wrapped: &WrappedDataKey,
    ) -> Result<Zeroizing<[u8; DATA_KEY_BYTES]>, KmsError> {
        let url = format!(
            "{}/v1/{}/decrypt/{}",
            self.config.address.trim_end_matches('/'),
            self.config.transit_mount,
            self.config.master_key_name
        );
        let response = self
            .request(self.client.post(url).json(&DecryptRequest {
                ciphertext: &wrapped.ciphertext,
            }))?
            .send()
            .map_err(KmsError::Http)?;
        if response.status() == StatusCode::NOT_FOUND {
            return Err(KmsError::NotFound);
        }
        let response = response.error_for_status().map_err(KmsError::Http)?;
        let envelope: DecryptEnvelope = response.json().map_err(KmsError::Http)?;
        let mut decoded = STANDARD
            .decode(envelope.data.plaintext)
            .map_err(|_| KmsError::InvalidResponse("invalid DEK base64"))?;
        let result = decoded
            .as_slice()
            .try_into()
            .map(Zeroizing::new)
            .map_err(|_| KmsError::InvalidResponse("DEK must contain exactly 32 bytes"));
        decoded.zeroize();
        result
    }
}

#[derive(Debug, Deserialize)]
struct KvReadEnvelope {
    data: KvReadData,
}

#[derive(Debug, Deserialize)]
struct KvReadData {
    data: StoredDataKey,
    metadata: KvMetadata,
}

#[derive(Debug, Deserialize)]
struct KvMetadata {
    version: u64,
}

#[derive(Debug, Deserialize, Serialize)]
struct StoredDataKey {
    key_scope: String,
    key_epoch: u64,
    wrapped_dek: String,
}

#[derive(Serialize)]
struct DecryptRequest<'a> {
    ciphertext: &'a str,
}

#[derive(Debug, Deserialize)]
struct DecryptEnvelope {
    data: DecryptData,
}

#[derive(Debug, Deserialize)]
struct DecryptData {
    plaintext: String,
}

#[derive(Serialize)]
struct GenerateDataKeyRequest {
    bits: u16,
}

#[derive(Deserialize)]
struct GenerateDataKeyEnvelope {
    data: GeneratedDataKey,
}

#[derive(Deserialize)]
struct GeneratedDataKey {
    plaintext: String,
    ciphertext: String,
}

#[derive(Serialize)]
struct KvWriteRequest<'a> {
    options: KvWriteOptions,
    data: &'a StoredDataKey,
}

#[derive(Serialize)]
struct KvWriteOptions {
    cas: u64,
}

#[derive(Deserialize)]
struct KvWriteEnvelope {
    data: KvMetadata,
}

#[derive(Debug, Error)]
pub enum KmsError {
    #[error("invalid KMS configuration: {0}")]
    InvalidConfiguration(&'static str),
    #[error("KMS record is unavailable or was destroyed")]
    NotFound,
    #[error("KMS key epoch has been revoked")]
    Revoked,
    #[error("invalid KMS response: {0}")]
    InvalidResponse(&'static str),
    #[error("read KMS credential: {0}")]
    Io(std::io::Error),
    #[error("KMS request failed: {0}")]
    Http(reqwest::Error),
    #[error("KMS cache lock is poisoned")]
    CachePoisoned,
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicUsize, Ordering};

    use super::*;

    struct FakeKms {
        unwraps: AtomicUsize,
    }

    impl FakeKms {
        fn wrapped(scope: &KeyScope, version: &str, epoch: u64) -> WrappedDataKey {
            WrappedDataKey {
                scope: scope.clone(),
                version: version.into(),
                epoch,
                ciphertext: "wrapped".into(),
            }
        }
    }

    impl KeyManagementPort for FakeKms {
        fn current(&self, scope: &KeyScope) -> Result<WrappedDataKey, KmsError> {
            Ok(Self::wrapped(scope, "2", 2))
        }

        fn version(
            &self,
            scope: &KeyScope,
            version: &str,
            epoch: u64,
        ) -> Result<WrappedDataKey, KmsError> {
            Ok(Self::wrapped(scope, version, epoch))
        }

        fn unwrap(
            &self,
            _wrapped: &WrappedDataKey,
        ) -> Result<Zeroizing<[u8; DATA_KEY_BYTES]>, KmsError> {
            self.unwraps.fetch_add(1, Ordering::Relaxed);
            Ok(Zeroizing::new([7; DATA_KEY_BYTES]))
        }
    }

    #[test]
    fn cache_is_bounded_and_reuses_exact_key() {
        let resolver = CachedDataKeyResolver::new(
            FakeKms {
                unwraps: AtomicUsize::new(0),
            },
            1,
            Duration::from_secs(60),
        )
        .unwrap();
        let first = KeyScope::new("tenant-a/subject-1").unwrap();
        let second = KeyScope::new("tenant-a/subject-2").unwrap();

        resolver.resolve_for_encrypt(&first).unwrap();
        resolver.resolve_for_encrypt(&first).unwrap();
        assert_eq!(resolver.kms.unwraps.load(Ordering::Relaxed), 1);
        resolver.resolve_for_encrypt(&second).unwrap();
        resolver.resolve_for_encrypt(&first).unwrap();
        assert_eq!(resolver.kms.unwraps.load(Ordering::Relaxed), 3);
    }

    #[test]
    fn revocation_barrier_invalidates_cache_and_rejects_stale_epoch() {
        let resolver = CachedDataKeyResolver::new(
            FakeKms {
                unwraps: AtomicUsize::new(0),
            },
            4,
            Duration::from_secs(60),
        )
        .unwrap();
        let scope = KeyScope::new("tenant-a/subject-1").unwrap();
        resolver.resolve_for_encrypt(&scope).unwrap();
        resolver.revoke_through(&scope, 2).unwrap();

        assert!(matches!(
            resolver.resolve_for_decrypt(&scope, "2", 2),
            Err(CryptoError::KeyRevoked)
        ));
        assert!(matches!(
            resolver.resolve_for_encrypt(&scope),
            Err(CryptoError::KeyRevoked)
        ));
    }

    #[test]
    fn vault_transit_config_validation_enforces_security_invariants() {
        let valid = VaultTransitConfig {
            address: "https://vault.example.com:8200".into(),
            token_path: PathBuf::from("/etc/vault/token"),
            namespace: Some("tenant-a".into()),
            transit_mount: "transit".into(),
            kv_mount: "kv".into(),
            master_key_name: "bpmp-master".into(),
            request_timeout_ms: 5000,
            cache_capacity: 100,
            cache_ttl_ms: 60_000,
        };
        assert!(valid.validate().is_ok());

        // HTTP rejected (HTTPS required)
        let mut invalid = valid.clone();
        invalid.address = "http://vault.example.com:8200".into();
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Empty token path rejected
        let mut invalid = valid.clone();
        invalid.token_path = PathBuf::new();
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Invalid transit mount segment rejected (e.g. contains slash or traversal)
        let mut invalid = valid.clone();
        invalid.transit_mount = "transit/sub".into();
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Invalid kv mount segment rejected
        let mut invalid = valid.clone();
        invalid.kv_mount = "../kv".into();
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Invalid master key segment rejected
        let mut invalid = valid.clone();
        invalid.master_key_name = "key with spaces".into();
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Zero request timeout rejected
        let mut invalid = valid.clone();
        invalid.request_timeout_ms = 0;
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Zero cache capacity rejected
        let mut invalid = valid.clone();
        invalid.cache_capacity = 0;
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Zero cache TTL rejected
        let mut invalid = valid.clone();
        invalid.cache_ttl_ms = 0;
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // Empty namespace string rejected
        let mut invalid = valid.clone();
        invalid.namespace = Some("   ".into());
        assert!(matches!(
            invalid.validate(),
            Err(KmsError::InvalidConfiguration(_))
        ));

        // None namespace accepted
        let mut valid_no_ns = valid.clone();
        valid_no_ns.namespace = None;
        assert!(valid_no_ns.validate().is_ok());
    }

    #[test]
    fn cache_expires_entries_after_configured_ttl() {
        let resolver = CachedDataKeyResolver::new(
            FakeKms {
                unwraps: AtomicUsize::new(0),
            },
            4,
            Duration::from_millis(10),
        )
        .unwrap();
        let scope = KeyScope::new("tenant-a/subject-ttl").unwrap();

        resolver.resolve_for_encrypt(&scope).unwrap();
        assert_eq!(resolver.kms.unwraps.load(Ordering::Relaxed), 1);

        // Immediate resolution reuses cached key
        resolver.resolve_for_encrypt(&scope).unwrap();
        assert_eq!(resolver.kms.unwraps.load(Ordering::Relaxed), 1);

        // Sleep past TTL
        std::thread::sleep(Duration::from_millis(25));

        // Expired entry triggers re-fetch / unwrap
        resolver.resolve_for_encrypt(&scope).unwrap();
        assert_eq!(resolver.kms.unwraps.load(Ordering::Relaxed), 2);
    }

    #[test]
    fn revocation_barrier_is_monotonic_and_idempotent() {
        let resolver = CachedDataKeyResolver::new(
            FakeKms {
                unwraps: AtomicUsize::new(0),
            },
            4,
            Duration::from_secs(60),
        )
        .unwrap();
        let scope = KeyScope::new("tenant-a/subject-monotonic").unwrap();

        // Install fence at epoch 5
        resolver.revoke_through(&scope, 5).unwrap();

        // Calling with lower epoch 3 does not regress the fence
        resolver.revoke_through(&scope, 3).unwrap();

        // Epoch 4 and 5 remain revoked
        assert!(matches!(
            resolver.resolve_for_decrypt(&scope, "1", 4),
            Err(CryptoError::KeyRevoked)
        ));
        assert!(matches!(
            resolver.resolve_for_decrypt(&scope, "1", 5),
            Err(CryptoError::KeyRevoked)
        ));

        // Epoch 6 is above the fence and can resolve
        let decrypted_key = resolver.resolve_for_decrypt(&scope, "1", 6).unwrap();
        assert_eq!(decrypted_key.key_epoch, 6);
    }

    struct MismatchedKms;
    impl KeyManagementPort for MismatchedKms {
        fn current(&self, _scope: &KeyScope) -> Result<WrappedDataKey, KmsError> {
            Ok(WrappedDataKey {
                scope: KeyScope::new("different-tenant/other-subject").unwrap(),
                version: "1".into(),
                epoch: 1,
                ciphertext: "wrapped".into(),
            })
        }

        fn version(
            &self,
            _scope: &KeyScope,
            version: &str,
            epoch: u64,
        ) -> Result<WrappedDataKey, KmsError> {
            Ok(WrappedDataKey {
                scope: KeyScope::new("different-tenant/other-subject").unwrap(),
                version: format!("{version}-mismatch"),
                epoch: epoch + 1,
                ciphertext: "wrapped".into(),
            })
        }

        fn unwrap(
            &self,
            _wrapped: &WrappedDataKey,
        ) -> Result<Zeroizing<[u8; DATA_KEY_BYTES]>, KmsError> {
            Ok(Zeroizing::new([9; DATA_KEY_BYTES]))
        }
    }

    #[test]
    fn cached_resolver_fails_closed_on_metadata_mismatch() {
        let resolver =
            CachedDataKeyResolver::new(MismatchedKms, 4, Duration::from_secs(60)).unwrap();
        let scope = KeyScope::new("tenant-a/subject-1").unwrap();

        // Scope mismatch on current() fails closed
        assert!(matches!(
            resolver.resolve_for_encrypt(&scope),
            Err(CryptoError::InvalidMetadata)
        ));

        // Metadata mismatch on version() fails closed
        assert!(matches!(
            resolver.resolve_for_decrypt(&scope, "1", 1),
            Err(CryptoError::InvalidMetadata)
        ));
    }

    #[test]
    fn kms_error_mapping_preserves_crypto_invariants() {
        assert!(matches!(
            map_kms_error(&KmsError::Revoked),
            CryptoError::KeyRevoked
        ));
        assert!(matches!(
            map_kms_error(&KmsError::NotFound),
            CryptoError::KeyRevoked
        ));
        assert!(matches!(
            map_kms_error(&KmsError::InvalidResponse("bad")),
            CryptoError::InvalidMetadata
        ));
        assert!(matches!(
            map_kms_error(&KmsError::InvalidConfiguration("bad")),
            CryptoError::KeyUnavailable
        ));
        assert!(matches!(
            map_kms_error(&KmsError::CachePoisoned),
            CryptoError::KeyUnavailable
        ));
    }
}

