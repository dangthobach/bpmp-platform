//! The 5-Layer Authorization Evaluation Pipeline.
//!
//! This is the **central orchestrator** of the entire AuthZ platform.
//! It coordinates all 5 layers in sequence, short-circuiting on the first denial.
//!
//! ## Evaluation order
//! 1. Emergency revoke check (O(1) DashMap)
//! 2. Temporal gate (EC-1: pure in-memory, non-cached)
//! 3. RBAC check (role hierarchy → effective permissions)
//! 4. Resource ACL check (type-level first, then instance ACL)
//! 5. ABAC evaluation (JSON AST with JIT attribute support)
//! 6. ReBAC graph check (if AST contains relation nodes)
//! 7. Row filter translation (multi-backend)
//! 8. Decision logging (async, non-blocking on main path)
//! 9. Shadow evaluation (G6, async, non-blocking)

use chrono::Utc;
use std::sync::Arc;
use tracing::{debug, instrument, warn};

use authz_core::{
    ids::{AuditLogId, PermissionId, TenantId, UserId},
    models::{
        audit::{
            AstNodeTrace, AuthzDecisionLog, DecisionContext, EnvContextSnapshot, EvalLayers,
            EvalTrace, LayerTrace,
        },
        filter::RowFilterResult,
        policy::{AuthzDecision, ConditionNode, ValueSource},
        rbac::{FieldFilterConfig, PolicyEffect, ResolvedPermission},
        tenant::FailMode,
    },
    AuthzError,
};

use crate::{
    algorithms::{bitmap::PermissionBitmapEngine, cuckoo::PermissionCuckooFilter},
    cache::{EmergencyRevokeCache, PolicyBundleCache},
    context::AuthzContext,
    evaluator::{
        abac::{evaluate_abac, JitAttributeFetcher},
        rebac::ReBacEngine,
        temporal::evaluate_temporal_gate_with_bundle,
    },
    filter::translator::{FilterTranslatorRegistry, TranslatedFilter},
    shadow::ShadowEngine,
};

// ─── Request / Response ───────────────────────────────────────────────────────

/// Input to the AuthZ evaluation pipeline.
#[derive(Debug, Clone)]
pub struct AuthzRequest {
    pub tenant_id: TenantId,
    pub user_id: UserId,
    pub action: String,
    pub context: AuthzContext,
    /// Whether to include the full eval trace in the response (debug mode).
    pub include_trace: bool,
}

/// Output of the AuthZ evaluation pipeline.
#[derive(Debug, Clone)]
pub struct AuthzResponse {
    pub decision: AuthzDecision,
    /// Human-readable reason for denial (safe to return to caller).
    pub deny_reason: Option<String>,
    /// Row filter for the requesting backend (SQL/ES/Mongo predicate).
    pub row_filter: Option<RowFilterResult>,
    /// Field filter configuration (applied by the calling service).
    pub field_filter: Option<FieldFilterConfig>,
    /// Full eval trace (only included when `include_trace = true`).
    pub eval_trace: Option<EvalTrace>,
    /// Unique ID for correlating this decision with the audit log.
    pub decision_id: AuditLogId,
}

impl AuthzResponse {
    pub fn deny(reason: impl Into<String>, decision_id: AuditLogId) -> Self {
        Self {
            decision: AuthzDecision::Deny,
            deny_reason: Some(reason.into()),
            row_filter: None,
            field_filter: None,
            eval_trace: None,
            decision_id,
        }
    }

    pub fn allow(decision_id: AuditLogId) -> Self {
        Self {
            decision: AuthzDecision::Allow,
            deny_reason: None,
            row_filter: None,
            field_filter: None,
            eval_trace: None,
            decision_id,
        }
    }
}

// ─── Pipeline ─────────────────────────────────────────────────────────────────

/// The 5-Layer AuthZ Evaluation Pipeline.
///
/// Shared across all request handlers as `Arc<AuthzEvaluationPipeline>`.
/// All fields are thread-safe.
pub struct AuthzEvaluationPipeline {
    pool: sqlx::PgPool,
    emergency_revoke: Arc<EmergencyRevokeCache>,
    rebac_engine: Arc<ReBacEngine>,
    filter_registry: Arc<FilterTranslatorRegistry>,
    jit_fetcher: Arc<dyn JitAttributeFetcher>,
    fail_mode: FailMode,

    // Core Algorithms
    cuckoo_filter: Arc<PermissionCuckooFilter>,
    bitmap_engine: Arc<PermissionBitmapEngine>,
    policy_bundle_cache: Arc<PolicyBundleCache>,

    // Shadow Mode (G6)
    shadow_engine: Arc<ShadowEngine>,
}

impl AuthzEvaluationPipeline {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        pool: sqlx::PgPool,
        emergency_revoke: Arc<EmergencyRevokeCache>,
        rebac_engine: Arc<ReBacEngine>,
        filter_registry: Arc<FilterTranslatorRegistry>,
        jit_fetcher: Arc<dyn JitAttributeFetcher>,
        fail_mode: FailMode,
        cuckoo_filter: Arc<PermissionCuckooFilter>,
        bitmap_engine: Arc<PermissionBitmapEngine>,
        policy_bundle_cache: Arc<PolicyBundleCache>,
        shadow_engine: Arc<ShadowEngine>,
    ) -> Self {
        Self {
            pool,
            emergency_revoke,
            rebac_engine,
            filter_registry,
            jit_fetcher,
            fail_mode,
            cuckoo_filter,
            bitmap_engine,
            policy_bundle_cache,
            shadow_engine,
        }
    }

    /// Evaluates an authorization request through all 5 layers.
    ///
    /// Every denial short-circuits immediately — no unnecessary DB work.
    /// The decision is logged asynchronously after returning.
    #[instrument(skip_all, fields(
        tenant_id = %req.tenant_id,
        user_id = %req.user_id,
        action = %req.action,
        resource_type = %req.context.resource.resource_type,
    ))]
    pub async fn evaluate(&self, req: &AuthzRequest) -> Result<AuthzResponse, AuthzError> {
        let decision_id = AuditLogId::new();
        let mut trace_layers = EvalLayers::default();

        if let Err(e) = self.validate_subject(req).await {
            warn!(error = %e, "Access denied: inactive or unknown subject");
            let response = self.build_deny_response(
                decision_id,
                e.error_code(),
                trace_layers,
                req,
                req.include_trace,
            );
            self.log_decision_async(req, &response, None).await;
            return Ok(response);
        }

        // ── Layer 0: Emergency Revoke (O(1) check) ───────────────────────────
        if self.emergency_revoke.is_revoked(req.user_id) {
            warn!(user_id = %req.user_id, "Access denied: emergency revoke active");
            let response = self.build_deny_response(
                decision_id,
                "EMERGENCY_REVOKED",
                trace_layers,
                req,
                false,
            );
            self.log_decision_async(req, &response, None).await;
            return Ok(response);
        }

        // ── Fast Path: Cuckoo Filter Early Rejection ──────────────────────────
        // O(1) probabilistic check: If false, it's 100% certain user doesn't have it.
        // If true, we continue to normal evaluation (might be false positive ~0.1%).
        let perm_code = format!("{}:{}", req.context.resource.resource_type, req.action);
        if !self
            .cuckoo_filter
            .might_have_permission(req.tenant_id, req.user_id, &perm_code)
        {
            debug!(user_id = %req.user_id, action = %req.action, "[Cuckoo Filter] Fast Rejection applied");
            let response = self.build_deny_response(
                decision_id,
                "CUCKOO_FAST_REJECT",
                trace_layers,
                req,
                false,
            );
            self.log_decision_async(req, &response, None).await;
            return Ok(response);
        }

        // ── Fast Path: Roaring Bitmap ─────────────────────────────────────────
        // O(1) check: Does user have this exact static permission?
        // This is deliberately not a fast ALLOW. A grant may still be constrained
        // by DENY policies, temporal gates, ABAC, ReBAC, row filters or field masks.
        if self.bitmap_engine.has_permission(req.user_id, &perm_code) {
            debug!(
                user_id = %req.user_id,
                action = %req.action,
                "[Roaring Bitmap] Permission present; continuing full policy evaluation"
            );
        }

        // ── Layer 1: Temporal Gate (EC-1) ────────────────────────────────────
        // Tenant-wide pre-check is recorded here; per-permission temporal gates
        // are evaluated below once the matching permissions are known
        // (their PermissionId is required to look up the right bundle entry).
        self.note_temporal_layer(req, &mut trace_layers).await;

        // ── Layer 2: RBAC — Fetch effective permissions ───────────────────────
        let effective_perms = match authz_db::fetch_effective_permissions(
            &self.pool,
            req.user_id,
            req.tenant_id,
            &req.context.resource.resource_type,
        )
        .await
        {
            Ok(perms) => perms,
            Err(e) => {
                warn!(error = %e, "Failed to load effective permissions — applying fail mode");
                return self.handle_fail_mode(decision_id, req, trace_layers).await;
            }
        };

        // Check if any permission grants the requested action
        let matching_perms: Vec<_> = effective_perms
            .permissions
            .iter()
            .filter(|p| p.permission.action == req.action)
            .collect();

        if matching_perms.is_empty() {
            trace_layers.rbac = Some(LayerTrace {
                passed: false,
                reason: Some(format!(
                    "No permission found for action '{}' on resource type '{}'",
                    req.action, req.context.resource.resource_type
                )),
            });
            let response = self.build_deny_response(
                decision_id,
                "NO_MATCHING_PERMISSION",
                trace_layers,
                req,
                req.include_trace,
            );
            self.log_decision_async(req, &response, None).await;
            self.fire_shadow_eval(req, response.decision);
            return Ok(response);
        }

        trace_layers.rbac = Some(LayerTrace {
            passed: true,
            reason: Some(format!(
                "{} matching permission(s) found",
                matching_perms.len()
            )),
        });

        // ── Layer 3: ABAC Evaluation ──────────────────────────────────────────
        // Evaluate all matching permissions in priority order. Explicit DENY
        // overrides every ALLOW once its temporal/ABAC/ReBAC constraints match.
        let mut allow_passed = false;
        let mut abac_trace: Option<AstNodeTrace> = None;
        let mut matched_row_filter = None;
        let mut matched_field_filter = None;

        for perm in &matching_perms {
            // Per-permission temporal gate (uses bundle's O(log N) pre-filter)
            if !self
                .evaluate_temporal_for_permission(req, perm.permission.id, &mut trace_layers)
                .await?
            {
                continue;
            }

            let permission_matches = self
                .evaluate_permission_conditions(perm, req, &mut trace_layers, &mut abac_trace)
                .await?;

            if !permission_matches {
                continue;
            }

            if perm.policy_effect == PolicyEffect::Deny {
                trace_layers.rbac = Some(LayerTrace {
                    passed: false,
                    reason: Some(format!(
                        "Explicit DENY matched at priority {}",
                        perm.policy_priority
                    )),
                });
                let response = self.build_deny_response(
                    decision_id,
                    "EXPLICIT_DENY",
                    trace_layers,
                    req,
                    req.include_trace,
                );
                self.log_decision_async(req, &response, None).await;
                self.fire_shadow_eval(req, response.decision);
                return Ok(response);
            }

            if !allow_passed {
                allow_passed = true;
                matched_row_filter = perm.row_filter_exprs.first().cloned();
                matched_field_filter = perm.field_filter.clone();
            }
        }

        trace_layers.abac = abac_trace;
        if let Some(t) = trace_layers.rbac.as_mut() {
            t.passed = allow_passed;
        }

        if !allow_passed {
            let response = self.build_deny_response(
                decision_id,
                "POLICY_CONDITION_FAILED",
                trace_layers,
                req,
                req.include_trace,
            );
            self.log_decision_async(req, &response, None).await;
            self.fire_shadow_eval(req, response.decision);
            return Ok(response);
        }

        // ── Layer 5: Row Filter Translation ──────────────────────────────────
        let row_filter = if let Some(filter_expr) = matched_row_filter {
            match serde_json::from_value::<authz_core::models::policy::ConditionNode>(filter_expr) {
                Ok(filter_ast) => Some(self.translate_row_filter(&filter_ast, req).await?),
                Err(_) => None,
            }
        } else {
            None
        };

        // ── Decision: ALLOW ───────────────────────────────────────────────────
        let eval_trace = EvalTrace {
            decision: AuthzDecision::Allow,
            matched_policy: None,
            shadow_diverged: false,
            deny_reason: None,
            layers: trace_layers,
        };

        let mut response = AuthzResponse::allow(decision_id);
        response.row_filter = row_filter;
        response.field_filter = matched_field_filter;
        if req.include_trace {
            response.eval_trace = Some(eval_trace.clone());
        }

        // Async decision logging — does not block response
        self.log_decision_async(req, &response, Some(eval_trace))
            .await;

        // ── Shadow evaluation (G6, async, fire-and-forget) ───────────────────
        self.fire_shadow_eval(req, response.decision);

        debug!(
            user_id = %req.user_id,
            action = %req.action,
            "Authorization: ALLOW"
        );

        Ok(response)
    }

    // ── Private helpers ───────────────────────────────────────────────────────

    async fn validate_subject(&self, req: &AuthzRequest) -> Result<(), AuthzError> {
        let _tenant = authz_db::find_tenant_by_id(&self.pool, req.tenant_id).await?;
        let _user = authz_db::find_user_by_id(&self.pool, req.tenant_id, req.user_id).await?;
        Ok(())
    }

    async fn evaluate_permission_conditions(
        &self,
        perm: &ResolvedPermission,
        req: &AuthzRequest,
        trace_layers: &mut EvalLayers,
        abac_trace: &mut Option<AstNodeTrace>,
    ) -> Result<bool, AuthzError> {
        let Some(condition) = &perm.policy_condition else {
            return Ok(true);
        };

        let ast = match serde_json::from_value::<ConditionNode>(condition.clone()) {
            Ok(ast) => ast,
            Err(e) => {
                warn!(error = %e, "Failed to parse condition AST — skipping permission");
                return Ok(false);
            }
        };

        let result = evaluate_abac(&ast, &req.context, self.jit_fetcher.as_ref()).await?;
        *abac_trace = Some(result.trace.clone());
        if !result.allowed {
            return Ok(false);
        }

        if ast.has_relation_nodes() {
            return self.evaluate_rebac_layer(&ast, req, trace_layers).await;
        }

        Ok(true)
    }

    /// Records the tenant-level temporal trace. Per-permission gating happens
    /// inside the matching-permission loop where `PermissionId` is available.
    async fn note_temporal_layer(&self, req: &AuthzRequest, trace_layers: &mut EvalLayers) {
        let has_bundle = self
            .policy_bundle_cache
            .version(req.tenant_id)
            .await
            .is_some();
        trace_layers.temporal_gate = Some(LayerTrace {
            passed: true,
            reason: Some(
                if has_bundle {
                    "Temporal pre-check deferred to per-permission gate"
                } else {
                    "No temporal bundle loaded for tenant — pass-through"
                }
                .to_owned(),
            ),
        });
    }

    /// Per-permission temporal gate using the bundle's `TemporalIntervalTree`
    /// pre-filter (O(log N)) plus the full day / timezone / CIDR check.
    async fn evaluate_temporal_for_permission(
        &self,
        req: &AuthzRequest,
        permission_id: PermissionId,
        trace_layers: &mut EvalLayers,
    ) -> Result<bool, AuthzError> {
        let Some(bundle) = self.policy_bundle_cache.get(req.tenant_id).await else {
            return Ok(true);
        };
        let result =
            evaluate_temporal_gate_with_bundle(permission_id, &bundle, &req.context.env).await?;
        if !result.allowed {
            trace_layers.temporal_gate = Some(LayerTrace {
                passed: false,
                reason: result.reason.clone(),
            });
        }
        Ok(result.allowed)
    }

    /// ReBAC layer — walks the AST for every `Relation` leaf and invokes the
    /// graph engine. All relations are combined under AND semantics: if any
    /// relation check returns `false`, the permission is rejected.
    async fn evaluate_rebac_layer(
        &self,
        ast: &ConditionNode,
        req: &AuthzRequest,
        trace_layers: &mut EvalLayers,
    ) -> Result<bool, AuthzError> {
        let mut relations = Vec::new();
        collect_relation_leaves(ast, &mut relations);
        if relations.is_empty() {
            trace_layers.rebac = Some(LayerTrace {
                passed: true,
                reason: Some("No relation leaves in AST".to_owned()),
            });
            return Ok(true);
        }

        let subject = format!("user:{}", req.user_id);
        for (key, target) in &relations {
            let Some(object) = resolve_relation_target(target, req) else {
                trace_layers.rebac = Some(LayerTrace {
                    passed: false,
                    reason: Some(format!("Cannot resolve relation target '{target}'")),
                });
                return Ok(false);
            };
            let ok = self
                .rebac_engine
                .check(req.tenant_id, &subject, key, &object)
                .await?;
            if !ok {
                trace_layers.rebac = Some(LayerTrace {
                    passed: false,
                    reason: Some(format!("No reachable path: {subject} --{key}--> {object}")),
                });
                return Ok(false);
            }
        }

        trace_layers.rebac = Some(LayerTrace {
            passed: true,
            reason: Some(format!("{} relation(s) satisfied", relations.len())),
        });
        Ok(true)
    }

    /// Row filter translation via the multi-backend `FilterTranslatorRegistry`.
    /// Loads the matching `ResourceType` for the request's tenant and dispatches
    /// to SQL / Elasticsearch / MongoDB.
    async fn translate_row_filter(
        &self,
        filter_ast: &ConditionNode,
        req: &AuthzRequest,
    ) -> Result<RowFilterResult, AuthzError> {
        let resource_type = match authz_db::find_resource_type_by_code(
            &self.pool,
            req.tenant_id,
            &req.context.resource.resource_type,
        )
        .await?
        {
            Some(rt) => rt,
            None => {
                warn!(
                    resource_type = %req.context.resource.resource_type,
                    "ResourceType not found — emitting empty filter"
                );
                return Ok(RowFilterResult {
                    backend: req.context.backend,
                    sql_where: None,
                    es_filter: None,
                    mongo_filter: None,
                });
            }
        };

        let translated = self
            .filter_registry
            .dispatch(
                req.context.backend,
                filter_ast,
                &req.context,
                &resource_type,
            )
            .await?;

        let mut result = RowFilterResult {
            backend: req.context.backend,
            sql_where: None,
            es_filter: None,
            mongo_filter: None,
        };
        match translated {
            TranslatedFilter::Sql(sql) => result.sql_where = Some(sql),
            TranslatedFilter::Elasticsearch(es) => result.es_filter = Some(es),
            TranslatedFilter::Mongodb(m) => result.mongo_filter = Some(m),
        }
        Ok(result)
    }

    fn build_deny_response(
        &self,
        decision_id: AuditLogId,
        reason: &str,
        layers: EvalLayers,
        _req: &AuthzRequest,
        include_trace: bool,
    ) -> AuthzResponse {
        let eval_trace = EvalTrace {
            decision: AuthzDecision::Deny,
            matched_policy: None,
            shadow_diverged: false,
            deny_reason: Some(reason.to_owned()),
            layers,
        };

        AuthzResponse {
            decision: AuthzDecision::Deny,
            deny_reason: Some(reason.to_owned()),
            row_filter: None,
            field_filter: None,
            eval_trace: if include_trace {
                Some(eval_trace)
            } else {
                None
            },
            decision_id,
        }
    }

    /// Fire-and-forget shadow evaluation — does not block the caller.
    fn fire_shadow_eval(&self, req: &AuthzRequest, active_decision: AuthzDecision) {
        let context_snapshot = serde_json::json!({
            "user": req.context.user_attributes,
            "resource_type": req.context.resource.resource_type,
            "action": req.action,
        });
        self.shadow_engine.spawn_shadow_eval(
            req.tenant_id,
            req.user_id.into_uuid(),
            req.context.resource.resource_type.clone(),
            req.context.resource.resource_ref.clone(),
            req.action.clone(),
            active_decision,
            context_snapshot,
        );
    }

    async fn handle_fail_mode(
        &self,
        decision_id: AuditLogId,
        req: &AuthzRequest,
        trace_layers: EvalLayers,
    ) -> Result<AuthzResponse, AuthzError> {
        match self.fail_mode {
            FailMode::Deny => {
                warn!(
                    tenant_id = %req.tenant_id,
                    "Policy engine unavailable — fail-closed (DENY)"
                );
                Ok(self.build_deny_response(
                    decision_id,
                    "POLICY_ENGINE_UNAVAILABLE",
                    trace_layers,
                    req,
                    false,
                ))
            }
            FailMode::Open => {
                warn!(
                    tenant_id = %req.tenant_id,
                    "Policy engine unavailable — fail-open (ALLOW)"
                );
                Ok(AuthzResponse::allow(decision_id))
            }
        }
    }

    /// Logs the decision to the database asynchronously.
    /// Errors in logging are recorded but do NOT affect the response.
    async fn log_decision_async(
        &self,
        req: &AuthzRequest,
        response: &AuthzResponse,
        eval_trace: Option<EvalTrace>,
    ) {
        let ctx = &req.context;
        let log = AuthzDecisionLog {
            id: response.decision_id,
            tenant_id: ctx.tenant_id,
            user_id: ctx.user_id.into_uuid(),
            resource_type: ctx.resource.resource_type.clone(),
            resource_ref: ctx.resource.resource_ref.clone(),
            action: req.action.clone(),
            decision: response.decision,
            matched_policy_id: None,
            policy_version_id: None,
            eval_trace: eval_trace.unwrap_or(EvalTrace {
                decision: response.decision,
                matched_policy: None,
                shadow_diverged: false,
                deny_reason: response.deny_reason.clone(),
                layers: EvalLayers::default(),
            }),
            context: DecisionContext {
                user: ctx.user_attributes.clone(),
                resource: ctx.resource.attributes.clone(),
                env: EnvContextSnapshot {
                    request_time: ctx.env.request_time,
                    client_ip: ctx.env.client_ip.map(|ip| ip.to_string()),
                },
            },
            decided_at: Utc::now(),
            sidecar_id: None,
        };

        let pool = self.pool.clone();
        tokio::spawn(async move {
            if let Err(e) = authz_db::insert_decision_log(&pool, &log).await {
                tracing::error!(error = %e, "Failed to record decision log");
            }
        });
    }
}

// ── Free helpers ──────────────────────────────────────────────────────────────

/// Walks an AST and collects every `(relation_key, target_path)` pair
/// found on either side of a leaf comparison.
fn collect_relation_leaves(node: &ConditionNode, out: &mut Vec<(String, String)>) {
    match node {
        ConditionNode::And { conditions } | ConditionNode::Or { conditions } => {
            for child in conditions {
                collect_relation_leaves(child, out);
            }
        }
        ConditionNode::Leaf(leaf) => {
            for side in [&leaf.left, &leaf.right] {
                if let ValueSource::Relation { key, target } = side {
                    out.push((key.clone(), target.clone()));
                }
            }
        }
    }
}

/// Resolves the object ID referenced by a relation target path.
/// Supported forms:
///   - `resource.<field>`           → looks up `<field>` in resource attributes
///   - `resource`                    → uses `resource_ref` directly
///   - anything else                 → unresolved (returns `None`)
fn resolve_relation_target(target: &str, req: &AuthzRequest) -> Option<String> {
    let resource_ref = req.context.resource.resource_ref.as_deref();
    let resource_type = &req.context.resource.resource_type;

    if target == "resource" {
        return resource_ref.map(|r| format!("{resource_type}:{r}"));
    }
    if let Some(field) = target.strip_prefix("resource.") {
        return req
            .context
            .resource
            .attributes
            .get(field)
            .and_then(|v| v.as_str())
            .map(|s| s.to_owned());
    }
    None
}
