//! Service-to-service JWT authentication for the PDP HTTP API.

use authz_http_middleware::{metadata_from_extensions, problem_response};
use axum::{
    extract::{Request, State},
    http::{header, StatusCode},
    middleware::Next,
    response::Response,
};
use jsonwebtoken::{decode_header, jwk::JwkSet, DecodingKey, Validation};
use serde::Deserialize;

use crate::state::AppState;

#[derive(Debug, Deserialize)]
struct ServiceClaims {
    sub: String,
    #[allow(dead_code)]
    exp: usize,
}

#[derive(Debug, Clone)]
pub struct ServicePrincipal {
    pub subject: String,
}

pub async fn require_service_jwt(
    State(state): State<AppState>,
    mut req: Request,
    next: Next,
) -> Response {
    let Some(token) = req
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.strip_prefix("Bearer "))
    else {
        return unauthorized(&req);
    };

    let principal = match verify_jwt(token, &state.jwt_jwks_url, &state.jwt_audience).await {
        Ok(principal) => principal,
        Err(_) => return unauthorized(&req),
    };
    req.extensions_mut().insert(principal);

    next.run(req).await
}

pub async fn verify_jwt(
    token: &str,
    jwks_url: &str,
    audience: &str,
) -> Result<ServicePrincipal, String> {
    if jwks_url.trim().is_empty() {
        return Err("JWT_JWKS_URL is required".to_owned());
    }
    if audience.trim().is_empty() {
        return Err("JWT_AUDIENCE is required".to_owned());
    }

    let header = decode_header(token).map_err(|e| format!("invalid token header: {e}"))?;
    let kid = header
        .kid
        .as_deref()
        .ok_or_else(|| "missing JWT kid header".to_owned())?;

    let jwks = reqwest::get(jwks_url)
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
    let token = jsonwebtoken::decode::<ServiceClaims>(token, &key, &validation)
        .map_err(|e| format!("invalid token: {e}"))?;
    if token.claims.sub.trim().is_empty() {
        return Err("missing JWT sub claim".to_owned());
    }
    Ok(ServicePrincipal {
        subject: token.claims.sub,
    })
}

fn unauthorized(request: &Request) -> Response {
    let metadata = metadata_from_extensions(request.extensions());
    problem_response(
        StatusCode::UNAUTHORIZED,
        "unauthorized",
        "Unauthorized",
        &metadata,
        false,
    )
}
