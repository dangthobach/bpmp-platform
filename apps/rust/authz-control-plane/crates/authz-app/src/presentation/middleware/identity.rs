//! JWT verification + Subject construction.
//!
//! This middleware accepts a Bearer token, verifies it against the configured
//! JWKS endpoint and audience, then inserts a validated [`AuthenticatedSubject`]
//! into request extensions.

use axum::{
    extract::Request,
    http::{header, StatusCode},
    middleware::Next,
    response::{IntoResponse, Response},
    Json,
};
use jsonwebtoken::{decode_header, jwk::JwkSet, DecodingKey, Validation};
use serde::Deserialize;
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::application::ports::authz_port::Subject;
use crate::presentation::middleware::request_id::RequestId;
use authz_sdk::EnvelopeResponse;

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
    let request_id = req
        .extensions()
        .get::<RequestId>()
        .map(|r| r.0.clone())
        .unwrap_or_default();

    let token = match extract_bearer(&req) {
        Some(t) => t,
        None => return unauthorized("missing bearer token", &request_id),
    };

    let claims = match decode_claims(&token).await {
        Ok(c) => c,
        Err(msg) => return unauthorized(&msg, &request_id),
    };

    let tenant_id = match Uuid::parse_str(&claims.tenant_id) {
        Ok(u) => u,
        Err(_) => return unauthorized("tenant_id claim is not a UUID", &request_id),
    };
    let user_id = match Uuid::parse_str(&claims.sub) {
        Ok(u) => u,
        Err(_) => return unauthorized("sub claim is not a UUID", &request_id),
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

fn unauthorized(reason: &str, request_id: &str) -> Response {
    let body = EnvelopeResponse::<()>::error("UNAUTHORIZED", reason, request_id);
    (StatusCode::UNAUTHORIZED, Json(body)).into_response()
}
