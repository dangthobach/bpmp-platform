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

use authz_http_middleware::{metadata_from_extensions, problem_response, RequestMetadata};
use axum::{extract::Request, http::StatusCode, middleware::Next, response::Response};
use uuid::Uuid;

use crate::presentation::middleware::identity::AuthenticatedSubject;

#[derive(Debug, Clone, Copy)]
pub struct TenantContext(pub Uuid);

pub async fn tenant_middleware(mut req: Request, next: Next) -> Response {
    let metadata = metadata_from_extensions(req.extensions());

    let Some(sub) = req.extensions().get::<AuthenticatedSubject>() else {
        return forbid(&metadata);
    };
    let jwt_tenant = sub.0.tenant_id;

    if let Some(hv) = req.headers().get("x-tenant-id") {
        let v = hv.to_str().unwrap_or_default();
        match Uuid::parse_str(v) {
            Ok(claimed) if claimed == jwt_tenant => {}
            _ => return forbid(&metadata),
        }
    }

    req.extensions_mut().insert(TenantContext(jwt_tenant));
    next.run(req).await
}

fn forbid(metadata: &RequestMetadata) -> Response {
    problem_response(
        StatusCode::FORBIDDEN,
        "forbidden",
        "Forbidden",
        metadata,
        false,
    )
}
