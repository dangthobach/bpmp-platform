//! Shadow mode evaluation engine (G6).
//!
//! Spawns an async task to evaluate the SHADOW policy version in parallel
//! with the ACTIVE version. The task:
//! 1. Evaluates the shadow version
//! 2. Compares the decision to the active version's decision
//! 3. Records any divergence in `policy_shadow_log`
//!
//! The main request path is NOT blocked — shadow evaluation is fire-and-forget.

use authz_core::{
    ids::{PolicyVersionId, TenantId},
    models::policy::AuthzDecision,
};
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use tracing::{error, warn};
use uuid::Uuid;

/// Shadow evaluation engine — triggers parallel evaluation and records divergence.
pub struct ShadowEngine {
    pool: PgPool,
}

impl ShadowEngine {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    /// Spawns a background task to evaluate the shadow policy version.
    ///
    /// ## Non-blocking
    /// Uses `tokio::spawn` — the caller returns immediately with the active decision.
    /// The shadow task runs in the background and logs divergence if detected.
    #[allow(clippy::too_many_arguments)]
    pub fn evaluate_shadow_async(
        &self,
        shadow_version_id: PolicyVersionId,
        _tenant_id: TenantId,
        user_id: Uuid,
        resource_ref: Option<String>,
        action: String,
        active_decision: AuthzDecision,
        context_snapshot: JsonValue,
        // The closure that performs the shadow evaluation
        evaluate_fn: impl Fn() -> AuthzDecision + Send + 'static,
        pool: PgPool,
    ) {
        tokio::spawn(async move {
            let shadow_decision = evaluate_fn();

            if shadow_decision != active_decision {
                warn!(
                    shadow_version_id = %shadow_version_id,
                    user_id = %user_id,
                    action = %action,
                    active = ?active_decision,
                    shadow = ?shadow_decision,
                    "Shadow policy divergence detected"
                );

                if let Err(e) = authz_db::insert_shadow_log(
                    &pool,
                    shadow_version_id,
                    Some(user_id),
                    resource_ref.as_deref(),
                    &action,
                    shadow_decision,
                    active_decision,
                    Some(&context_snapshot),
                )
                .await
                {
                    error!(
                        error = %e,
                        "Failed to record shadow divergence log"
                    );
                }
            }
        });
    }

    /// Fire-and-forget shadow policy evaluation (fully async).
    ///
    /// Looks up the SHADOW policy version for the given tenant + resource type,
    /// derives a decision from the policy snapshot, and records divergence in
    /// `policy_shadow_log` when the shadow decision differs from the active one.
    ///
    /// ## Non-blocking
    /// Returns immediately; all work runs inside a detached `tokio::spawn`.
    #[allow(clippy::too_many_arguments)]
    pub fn spawn_shadow_eval(
        &self,
        tenant_id: TenantId,
        user_id: Uuid,
        resource_type: String,
        resource_ref: Option<String>,
        action: String,
        active_decision: AuthzDecision,
        context_snapshot: JsonValue,
    ) {
        let pool = self.pool.clone();
        tokio::spawn(async move {
            let shadow_ver = match authz_db::find_shadow_policy_version(
                &pool,
                tenant_id,
                &resource_type,
            )
            .await
            {
                Ok(Some(v)) => v,
                Ok(None) => return, // No SHADOW version configured for this tenant/resource
                Err(e) => {
                    error!(error = %e, "Shadow: failed to query shadow policy version");
                    return;
                }
            };

            // Derive the shadow decision from the policy snapshot.
            // The snapshot's top-level "effect" field ("ALLOW"|"DENY") is used;
            // full ABAC re-evaluation can be added here when needed.
            let shadow_decision = shadow_ver
                .snapshot
                .get("effect")
                .and_then(|v| v.as_str())
                .map(|s| {
                    if s.eq_ignore_ascii_case("DENY") {
                        AuthzDecision::Deny
                    } else {
                        AuthzDecision::Allow
                    }
                })
                .unwrap_or(AuthzDecision::Allow);

            if shadow_decision != active_decision {
                warn!(
                    shadow_version_id = %shadow_ver.id,
                    user_id = %user_id,
                    action = %action,
                    active = ?active_decision,
                    shadow = ?shadow_decision,
                    "Shadow policy divergence detected"
                );

                if let Err(e) = authz_db::insert_shadow_log(
                    &pool,
                    shadow_ver.id,
                    Some(user_id),
                    resource_ref.as_deref(),
                    &action,
                    shadow_decision,
                    active_decision,
                    Some(&context_snapshot),
                )
                .await
                {
                    error!(error = %e, "Shadow: failed to record divergence log");
                }
            }
        });
    }
}
