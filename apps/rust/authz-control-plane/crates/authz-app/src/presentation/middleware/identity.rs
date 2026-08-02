//! JWT verification + Subject construction.
//!
//! This middleware accepts a Bearer token, verifies it against the configured
//! JWKS endpoint and audience, then inserts a validated [`AuthenticatedSubject`]
//! into request extensions.

use authz_http_middleware::{metadata_from_extensions, problem_response, RequestMetadata};
use axum::{
    extract::Request,
    http::{header, StatusCode},
    middleware::Next,
    response::Response,
};
use jsonwebtoken::{decode_header, jwk::JwkSet, DecodingKey, Validation};
use serde::Deserialize;
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::application::ports::authz_port::Subject;

#[derive(Debug, Clone)]
pub struct AuthenticatedSubject(pub Subject);

#[derive(Debug, Deserialize)]
struct Claims {
    sub: String,
    tenant_id: String,
    #[allow(dead_code)]
    exp: usize,
    #[serde(default)]
    attributes: JsonValue,
    #[serde(default)]
    attributes_version: i64,
}

pub async fn identity_middleware(mut req: Request, next: Next) -> Response {
    let metadata = metadata_from_extensions(req.extensions());
    let request_id = metadata.request_id.clone();

    let token = match extract_bearer(&req) {
        Some(t) => t,
        None => return unauthorized(&metadata),
    };

    let claims = match decode_claims(&token).await {
        Ok(c) => c,
        Err(_) => return unauthorized(&metadata),
    };

    let tenant_id = match Uuid::parse_str(&claims.tenant_id) {
        Ok(u) => u,
        Err(_) => return unauthorized(&metadata),
    };
    let user_id = match Uuid::parse_str(&claims.sub) {
        Ok(u) => u,
        Err(_) => return unauthorized(&metadata),
    };

    let subject = Subject {
        tenant_id,
        user_id,
        attributes: claims.attributes,
        attributes_version: claims.attributes_version,
        request_id: request_id.clone(),
    };
    req.extensions_mut().insert(AuthenticatedSubject(subject));
    next.run(req).await
}

fn extract_bearer(req: &Request) -> Option<String> {
    req.headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|s| s.strip_prefix("Bearer "))
        .map(|s| s.to_owned())
}

async fn decode_claims(token: &str) -> Result<Claims, String> {
    let jwks_url =
        std::env::var("JWT_JWKS_URL").map_err(|_| "JWT_JWKS_URL is required".to_owned())?;
    let audience =
        std::env::var("JWT_AUDIENCE").map_err(|_| "JWT_AUDIENCE is required".to_owned())?;

    let header = decode_header(token).map_err(|e| e.to_string())?;
    let kid = header
        .kid
        .as_deref()
        .ok_or_else(|| "missing JWT kid header".to_owned())?;

    let jwks = reqwest::get(&jwks_url)
        .await
        .map_err(|e| format!("failed to fetch JWKS: {e}"))?
        .error_for_status()
        .map_err(|e| format!("JWKS endpoint rejected request: {e}"))?
        .json::<JwkSet>()
        .await
        .map_err(|e| format!("invalid JWKS response: {e}"))?;

    let jwk = jwks
        .find(kid)
        .ok_or_else(|| "JWT kid not found in JWKS".to_owned())?;
    let key = DecodingKey::from_jwk(jwk).map_err(|e| format!("invalid JWK: {e}"))?;
    let mut validation = Validation::new(header.alg);
    validation.set_audience(&[audience]);
    let data = jsonwebtoken::decode::<Claims>(token, &key, &validation)
        .map_err(|e| format!("invalid token: {e}"))?;
    Ok(data.claims)
}

fn unauthorized(metadata: &RequestMetadata) -> Response {
    problem_response(
        StatusCode::UNAUTHORIZED,
        "unauthorized",
        "Unauthorized",
        metadata,
        false,
    )
}
