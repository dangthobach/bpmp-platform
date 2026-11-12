pub mod catalog;
pub mod policy;
pub mod rbac;
pub mod revoke;
pub mod tenant;

use crate::state::AppState;
use axum::{
    routing::{delete, get, post, put},
    Router,
};

pub fn admin_routes() -> Router<AppState> {
    Router::new()
        // Tenant endpoints
        .route(
            "/tenants",
            post(tenant::create_tenant).get(tenant::list_tenants),
        )
        .route(
            "/tenants/:id",
            get(tenant::get_tenant)
                .put(tenant::update_tenant)
                .delete(tenant::delete_tenant),
        )
        .route("/tenants/:id/status", put(tenant::update_tenant_status))
        // Policy endpoints
        .route("/policies", post(policy::upload_policy))
        .route("/policies/:id/promote", post(policy::promote_policy))
        // Emergency Revoke endpoints
        .route("/revoke", post(revoke::revoke_user))
        .route("/revoke/:user_id", delete(revoke::clear_revoke))
        // RBAC endpoints (roles, permissions, user_roles)
        .route("/roles", post(rbac::create_role))
        .route("/permissions", post(rbac::create_permission))
        .route("/users/:user_id/roles", post(rbac::assign_role))
        // Versioned authorization catalog control-plane APIs.
        .route(
            "/catalog/versions",
            get(catalog::list_versions).post(catalog::create_draft),
        )
        .route("/catalog/versions/:version_id", put(catalog::update_draft))
        .route("/catalog/entries", get(catalog::list_owner_entries))
        .route("/catalog/roles", get(catalog::list_roles))
        .route("/catalog/audit", get(catalog::list_audit))
        .route(
            "/catalog/acknowledgements",
            get(catalog::list_acknowledgements),
        )
        .route(
            "/catalog/versions/:version_id/validate",
            post(catalog::validate_version),
        )
        .route(
            "/catalog/versions/:version_id/publish",
            post(catalog::publish_version),
        )
        .route(
            "/catalog/versions/:version_id/bundle",
            get(catalog::download_bundle),
        )
        .route(
            "/catalog/owner-manifests",
            post(catalog::register_owner_manifest),
        )
}
