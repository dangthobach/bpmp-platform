//! Tenant context middleware.
//!
//! Runs **after** `identity_middleware`. Extracts the tenant id from the
//! authenticated subject (single source of truth) and exposes it as
//! [`TenantContext`] for repositories and PEP calls.
//!
//! It also enforces a defence-in-depth check: if an explicit
//! `X-Tenant-Id` header is provided, it MUST match the JWT claim — otherwise
//! the request is rejected. This prevents trust-boundary-confusion attacks
//! where a benign caller blindly proxies a client-controlled header.

use authz_sdk::EnvelopeResponse;
use axum::{
    extract::Request,
    http::StatusCode,
    middleware::Next,
    response::{IntoResponse, Response},
    Json,
};
use uuid::Uuid;

use crate::presentation::middleware::identity::AuthenticatedSubject;
use crate::presentation::middleware::request_id::RequestId;

#[derive(Debug, Clone, Copy)]
pub struct TenantContext(pub Uuid);

pub async fn tenant_middleware(mut req: Request, next: Next) -> Response {
    let request_id = req
        .extensions()
        .get::<RequestId>()
        .map(|r| r.0.clone())
        .unwrap_or_default();

    let Some(sub) = req.extensions().get::<AuthenticatedSubject>() else {
        return forbid("identity not resolved", &request_id);
    };
    let jwt_tenant = sub.0.tenant_id;

    if let Some(hv) = req.headers().get("x-tenant-id") {
        let v = hv.to_str().unwrap_or_default();
        match Uuid::parse_str(v) {
            Ok(claimed) if claimed == jwt_tenant => {}
            _ => return forbid("tenant header mismatch", &request_id),
        }
    }

    req.extensions_mut().insert(TenantContext(jwt_tenant));
    next.run(req).await
}

fn forbid(reason: &str, request_id: &str) -> Response {
    let body = EnvelopeResponse::<()>::error("FORBIDDEN", reason, request_id);
    (StatusCode::FORBIDDEN, Json(body)).into_response()
}
