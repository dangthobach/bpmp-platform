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
}
