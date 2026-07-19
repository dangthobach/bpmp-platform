//! Service-to-service JWT authentication for the PDP HTTP API.

use axum::{
    extract::{Request, State},
    http::{header, StatusCode},
    middleware::Next,
    response::{IntoResponse, Response},
    Json,
};
use jsonwebtoken::{decode_header, jwk::JwkSet, DecodingKey, Validation};
use serde::{Deserialize, Serialize};

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

#[derive(Debug, Serialize)]
struct UnauthorizedBody {
    error_code: &'static str,
    message: String,
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
        return unauthorized("missing bearer token");
    };

    let principal = match verify_jwt(token, &state.jwt_jwks_url, &state.jwt_audience).await {
        Ok(principal) => principal,
        Err(reason) => return unauthorized(&reason),
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

fn unauthorized(message: &str) -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(UnauthorizedBody {
            error_code: "UNAUTHORIZED",
            message: message.to_owned(),
        }),
    )
        .into_response()
}
