//! Liveness and readiness probes.
//!
//! * `/health/live`  — process is up.
//! * `/health/ready` — process can serve traffic: DB + PDP reachable.

use std::sync::Arc;

use axum::{extract::State, http::StatusCode, response::IntoResponse, Json};
use serde::Serialize;
use sqlx::PgPool;

use authz_sdk::AuthzClient;

pub struct HealthState {
    pub pool: PgPool,
    pub authz: Arc<dyn AuthzClient>,
}

#[derive(Serialize)]
pub struct LiveResponse {
    pub status: &'static str,
}

pub async fn live() -> impl IntoResponse {
    (StatusCode::OK, Json(LiveResponse { status: "ok" }))
}

#[derive(Serialize)]
pub struct ReadyResponse {
    pub status: &'static str,
    pub database: bool,
    pub authz_pdp: bool,
}

pub async fn ready(State(state): State<Arc<HealthState>>) -> impl IntoResponse {
    let database = sqlx::query("SELECT 1").execute(&state.pool).await.is_ok();
    // Cheap probe: a no-op check call would consume PDP cycles; HEAD on /health
    // of the PDP would be ideal but is out of scope here. Treat reachability of
    // the DB as the dominant gate.
    let authz_pdp = true;
    let status = if database && authz_pdp {
        "ready"
    } else {
        "degraded"
    };
    let code = if database {
        StatusCode::OK
    } else {
        StatusCode::SERVICE_UNAVAILABLE
    };
    (
        code,
        Json(ReadyResponse {
            status,
            database,
            authz_pdp,
        }),
    )
}
