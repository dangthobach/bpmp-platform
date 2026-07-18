//! JWT verification against an injected, bounded JWKS snapshot.

use std::collections::BTreeSet;
use std::sync::RwLock;

use jsonwebtoken::jwk::JwkSet;
use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header};
use serde::Deserialize;
use thiserror::Error;

#[derive(Debug, Clone)]
pub struct JwtVerificationConfig {
    pub issuers: BTreeSet<String>,
    pub audiences: BTreeSet<String>,
    pub allowed_algorithms: Vec<Algorithm>,
    pub max_token_bytes: usize,
    pub max_jwks_keys: usize,
    pub max_roles: usize,
    pub max_capabilities: usize,
    pub clock_skew_seconds: u64,
}

impl JwtVerificationConfig {
    /// Validates deployment-provided JWT policy.
    ///
    /// # Errors
    ///
    /// Rejects empty trust sets, unbounded values, and symmetric algorithms.
    pub fn validate(&self) -> Result<(), JwtVerificationError> {
        if self.issuers.is_empty()
            || self.audiences.is_empty()
            || self.allowed_algorithms.is_empty()
            || self.max_token_bytes == 0
            || self.max_jwks_keys == 0
            || self.max_roles == 0
            || self.max_capabilities == 0
        {
            return Err(JwtVerificationError::InvalidConfiguration);
        }
        if self.allowed_algorithms.iter().any(|algorithm| {
            matches!(
                algorithm,
                Algorithm::HS256 | Algorithm::HS384 | Algorithm::HS512
            )
        }) {
            return Err(JwtVerificationError::SymmetricAlgorithmRejected);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct VerifiedActorClaims {
    pub tenant_id: String,
    pub actor_id: String,
    pub roles: Vec<String>,
    pub capabilities: Vec<String>,
    pub revoke_epoch: u64,
    pub issued_at_epoch_seconds: u64,
    pub expires_at_epoch_seconds: u64,
}

pub struct JwtIdentityVerifier {
    config: JwtVerificationConfig,
    jwks: RwLock<JwkSet>,
}

impl JwtIdentityVerifier {
    /// Creates a verifier from validated policy and an initial JWKS document.
    ///
    /// # Errors
    ///
    /// Rejects malformed or oversized JWKS snapshots.
    pub fn new(
        config: JwtVerificationConfig,
        jwks_json: &[u8],
    ) -> Result<Self, JwtVerificationError> {
        config.validate()?;
        let jwks = parse_jwks(jwks_json, config.max_jwks_keys)?;
        Ok(Self {
            config,
            jwks: RwLock::new(jwks),
        })
    }

    /// Atomically replaces the trusted JWKS snapshot after validation.
    ///
    /// # Errors
    ///
    /// Rejects malformed/oversized snapshots or a poisoned cache lock.
    pub fn replace_jwks(&self, jwks_json: &[u8]) -> Result<(), JwtVerificationError> {
        let incoming = parse_jwks(jwks_json, self.config.max_jwks_keys)?;
        *self
            .jwks
            .write()
            .map_err(|_| JwtVerificationError::LockPoisoned)? = incoming;
        Ok(())
    }

    /// Verifies signature and claims using an explicitly injected epoch time.
    ///
    /// # Errors
    ///
    /// Fails closed on oversized tokens, unknown keys/algorithms, invalid
    /// signatures, scope mismatch, or invalid temporal claims.
    pub fn verify(
        &self,
        token: &str,
        evaluated_at_epoch_seconds: u64,
    ) -> Result<VerifiedActorClaims, JwtVerificationError> {
        if token.len() > self.config.max_token_bytes {
            return Err(JwtVerificationError::TokenTooLarge);
        }
        let header = decode_header(token).map_err(|_| JwtVerificationError::MalformedToken)?;
        if !self.config.allowed_algorithms.contains(&header.alg) {
            return Err(JwtVerificationError::AlgorithmRejected);
        }
        let key_id = header.kid.ok_or(JwtVerificationError::MissingKeyId)?;
        let jwks = self
            .jwks
            .read()
            .map_err(|_| JwtVerificationError::LockPoisoned)?;
        let jwk = jwks.find(&key_id).ok_or(JwtVerificationError::UnknownKey)?;
        let key = DecodingKey::from_jwk(jwk).map_err(|_| JwtVerificationError::InvalidKey)?;
        let mut validation = Validation::new(header.alg);
        validation
            .algorithms
            .clone_from(&self.config.allowed_algorithms);
        validation.validate_exp = false;
        validation.validate_nbf = false;
        validation.validate_aud = false;
        validation.required_spec_claims.clear();
        let claims = decode::<ActorJwtClaims>(token, &key, &validation)
            .map_err(|_| JwtVerificationError::SignatureOrClaimsInvalid)?
            .claims;
        validate_claims(&self.config, claims, evaluated_at_epoch_seconds)
    }
}

#[derive(Debug, Deserialize)]
struct ActorJwtClaims {
    iss: String,
    sub: String,
    aud: Audience,
    exp: u64,
    nbf: Option<u64>,
    iat: u64,
    tenant_id: String,
    #[serde(default)]
    roles: Vec<String>,
    #[serde(default)]
    capabilities: Vec<String>,
    #[serde(default)]
    revoke_epoch: u64,
}

#[derive(Debug, Deserialize)]
#[serde(untagged)]
enum Audience {
    One(String),
    Many(Vec<String>),
}

impl Audience {
    fn matches(&self, allowed: &BTreeSet<String>) -> bool {
        match self {
            Self::One(value) => allowed.contains(value),
            Self::Many(values) => values.iter().any(|value| allowed.contains(value)),
        }
    }
}

fn validate_claims(
    config: &JwtVerificationConfig,
    mut claims: ActorJwtClaims,
    evaluated_at: u64,
) -> Result<VerifiedActorClaims, JwtVerificationError> {
    if !config.issuers.contains(&claims.iss)
        || !claims.aud.matches(&config.audiences)
        || claims.sub.trim().is_empty()
        || claims.tenant_id.trim().is_empty()
    {
        return Err(JwtVerificationError::ScopeMismatch);
    }
    let latest_valid_time = claims
        .exp
        .checked_add(config.clock_skew_seconds)
        .ok_or(JwtVerificationError::InvalidTemporalClaims)?;
    let earliest_valid_time = claims
        .nbf
        .unwrap_or(claims.iat)
        .saturating_sub(config.clock_skew_seconds);
    if evaluated_at < earliest_valid_time
        || evaluated_at >= latest_valid_time
        || claims.iat >= claims.exp
    {
        return Err(JwtVerificationError::InvalidTemporalClaims);
    }
    canonicalize(&mut claims.roles);
    canonicalize(&mut claims.capabilities);
    if claims.roles.len() > config.max_roles
        || claims.capabilities.len() > config.max_capabilities
        || claims.roles.iter().any(String::is_empty)
        || claims.capabilities.iter().any(String::is_empty)
    {
        return Err(JwtVerificationError::InvalidAuthorizationClaims);
    }
    Ok(VerifiedActorClaims {
        tenant_id: claims.tenant_id,
        actor_id: claims.sub,
        roles: claims.roles,
        capabilities: claims.capabilities,
        revoke_epoch: claims.revoke_epoch,
        issued_at_epoch_seconds: claims.iat,
        expires_at_epoch_seconds: claims.exp,
    })
}

fn canonicalize(values: &mut Vec<String>) {
    values.sort_unstable();
    values.dedup();
}

fn parse_jwks(bytes: &[u8], max_keys: usize) -> Result<JwkSet, JwtVerificationError> {
    let jwks: JwkSet =
        serde_json::from_slice(bytes).map_err(|_| JwtVerificationError::MalformedJwks)?;
    if jwks.keys.is_empty() || jwks.keys.len() > max_keys {
        return Err(JwtVerificationError::JwksKeyLimit);
    }
    Ok(jwks)
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum JwtVerificationError {
    #[error("JWT verification configuration is invalid")]
    InvalidConfiguration,
    #[error("symmetric JWT algorithms are not accepted for workload-facing actor identity")]
    SymmetricAlgorithmRejected,
    #[error("JWKS document is malformed")]
    MalformedJwks,
    #[error("JWKS key count is outside the configured bound")]
    JwksKeyLimit,
    #[error("JWT exceeds the configured byte limit")]
    TokenTooLarge,
    #[error("JWT is malformed")]
    MalformedToken,
    #[error("JWT signing algorithm is not allowed")]
    AlgorithmRejected,
    #[error("JWT header is missing kid")]
    MissingKeyId,
    #[error("JWT references an unknown key")]
    UnknownKey,
    #[error("JWKS key cannot verify this token")]
    InvalidKey,
    #[error("JWT signature or encoded claims are invalid")]
    SignatureOrClaimsInvalid,
    #[error("JWT issuer, audience, tenant, or subject is invalid")]
    ScopeMismatch,
    #[error("JWT temporal claims are invalid")]
    InvalidTemporalClaims,
    #[error("JWT roles or capabilities are invalid")]
    InvalidAuthorizationClaims,
    #[error("JWKS cache lock is poisoned")]
    LockPoisoned,
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config() -> JwtVerificationConfig {
        JwtVerificationConfig {
            issuers: ["https://identity.example".into()].into(),
            audiences: ["bpmp".into()].into(),
            allowed_algorithms: vec![Algorithm::RS256],
            max_token_bytes: 4096,
            max_jwks_keys: 4,
            max_roles: 32,
            max_capabilities: 64,
            clock_skew_seconds: 5,
        }
    }

    #[test]
    fn rejects_symmetric_algorithm_configuration() {
        let mut invalid = config();
        invalid.allowed_algorithms = vec![Algorithm::HS256];
        assert_eq!(
            invalid.validate(),
            Err(JwtVerificationError::SymmetricAlgorithmRejected)
        );
    }

    #[test]
    fn rejects_empty_or_oversized_jwks() {
        assert!(matches!(
            JwtIdentityVerifier::new(config(), br#"{"keys":[]}"#),
            Err(JwtVerificationError::JwksKeyLimit)
        ));
        let mut bounded = config();
        bounded.max_jwks_keys = 1;
        assert!(matches!(
            JwtIdentityVerifier::new(
                bounded,
                br#"{"keys":[{"kty":"RSA","n":"AQ","e":"AQAB"},{"kty":"RSA","n":"Ag","e":"AQAB"}]}"#,
            ),
            Err(JwtVerificationError::JwksKeyLimit)
        ));
    }
}
