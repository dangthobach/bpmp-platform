//! Compose middlewares, routes, health, metrics scrape endpoint.

use std::sync::Arc;

use authz_http_middleware::Config as TransportConfig;
use axum::{
    middleware,
    routing::{get, post},
    Router,
};

use crate::bootstrap::AppContainer;
use crate::presentation::handlers::{health, organization};
use crate::presentation::middleware::{identity::identity_middleware, tenant::tenant_middleware};

pub fn build_router(app: AppContainer, transport: TransportConfig) -> anyhow::Result<Router> {
    build_router_with_internal_routes(app, transport, true)
}

pub fn build_public_router(
    app: AppContainer,
    transport: TransportConfig,
) -> anyhow::Result<Router> {
    build_router_with_internal_routes(app, transport, false)
}

fn build_router_with_internal_routes(
    app: AppContainer,
    transport: TransportConfig,
    include_internal_routes: bool,
) -> anyhow::Result<Router> {
    let health_state = Arc::new(health::HealthState {
        pool: app.pool.clone(),
        authz: app.authz_client.clone(),
    });

    let health_routes = Router::new()
        .route("/health/live", get(health::live))
        .route("/health/ready", get(health::ready))
        .with_state(health_state);

    let api_routes = Router::new()
        .route(
            "/api/v1/organizations",
            post(organization::create_organization),
        )
        .route(
            "/api/v1/organizations",
            get(organization::list_organizations),
        )
        .route(
            "/api/v1/organizations/:org_id",
            get(organization::get_organization),
        )
        .route(
            "/api/v1/branch-scopes/:branch_id",
            get(organization::resolve_branch_scope),
        )
        .route(
            "/api/v1/organizations/:org_id/nodes",
            post(organization::add_node),
        )
        .route(
            "/api/v1/organizations/:org_id/nodes/:node_id/move",
            post(organization::move_node),
        )
        .layer(middleware::from_fn(tenant_middleware))
        .layer(middleware::from_fn(identity_middleware))
        .with_state(app.clone());

    let mut router = Router::new().merge(health_routes).merge(api_routes);
    if include_internal_routes {
        let internal_routes = Router::new()
            .route(
                "/internal/v1/branch-scopes/:branch_id",
                get(organization::resolve_branch_scope_internal),
            )
            .route(
                "/internal/v1/branch-scopes/descendants",
                post(organization::resolve_branch_descendants_internal),
            )
            .with_state(app);
        router = router.merge(internal_routes);
    }
    authz_http_middleware::apply(router, transport).map_err(anyhow::Error::msg)
}
