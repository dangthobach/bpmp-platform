//! Integration tests for the 5-Layer AuthZ Evaluation Pipeline.
//!
//! These tests use a real PostgreSQL database (via `#[sqlx::test]`).
//! Each test gets an isolated, freshly-migrated database that is torn
//! down automatically when the test completes.
//!
//! ## Prerequisites
//! Set `DATABASE_URL` (or `TEST_DATABASE_URL`) in the environment or a
//! `.env` file at the workspace root before running:
//! ```
//! DATABASE_URL=postgres://user:pass@localhost/authz_test
//! ```
//!
//! ## Test scenarios
//! 1. **allow_path**      — RBAC grant with no conditions → Allow (via Bitmap fast-path)
//! 2. **temporal_deny**   — RBAC grant + temporal policy outside window → Deny
//! 3. **tenant_isolation** — User from tenant A requests under tenant B → Deny (no perms in B)

use std::collections::HashMap;
use std::sync::Arc;

use async_trait::async_trait;
use chrono::{TimeZone, Utc};
use serde_json::Value as JsonValue;
use sqlx::PgPool;

use authz_core::{
    ids::{TenantId, UserId},
    models::{
        filter::{FilterBackend, SqlFilterResult},
        policy::{AuthzDecision, ConditionNode},
        resource::ResourceType,
        tenant::FailMode,
    },
    AuthzError,
};

use authz_engine::{
    algorithms::{bitmap::PermissionBitmapEngine, cuckoo::PermissionCuckooFilter},
    cache::{bundle_loader::BundleLoader, EmergencyRevokeCache, PolicyBundleCache},
    context::{AuthzContext, EnvContext, ResourceContext},
    evaluator::{
        abac::JitAttributeFetcher,
        pipeline::{AuthzEvaluationPipeline, AuthzRequest},
        rebac::{ReBacConfig, ReBacEngine},
    },
    filter::translator::{FilterTranslator, FilterTranslatorRegistry, TranslatedFilter},
    shadow::ShadowEngine,
};

// ─── Stub implementations ────────────────────────────────────────────────────

struct NoOpJitFetcher;

#[async_trait]
impl JitAttributeFetcher for NoOpJitFetcher {
    async fn fetch(&self, _: &str, _: &str, _: &str, _: &str) -> Result<JsonValue, AuthzError> {
        Ok(JsonValue::Null)
    }
}

struct NoOpTranslator;

#[async_trait]
impl FilterTranslator for NoOpTranslator {
    async fn translate(
        &self,
        _node: &ConditionNode,
        _ctx: &AuthzContext,
        _rt: &ResourceType,
    ) -> Result<TranslatedFilter, AuthzError> {
        Ok(TranslatedFilter::Sql(SqlFilterResult {
            predicate: "1=1".into(),
            params: HashMap::new(),
        }))
    }
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

fn build_test_pipeline(
    pool: PgPool,
    bitmap_engine: Arc<PermissionBitmapEngine>,
    cuckoo_filter: Arc<PermissionCuckooFilter>,
    policy_bundle_cache: Arc<PolicyBundleCache>,
) -> AuthzEvaluationPipeline {
    let rebac_engine = Arc::new(ReBacEngine::new(pool.clone(), ReBacConfig::default()));
    let filter_registry = Arc::new(FilterTranslatorRegistry::new(
        Box::new(NoOpTranslator),
        Box::new(NoOpTranslator),
        Box::new(NoOpTranslator),
    ));
    let shadow_engine = Arc::new(ShadowEngine::new(pool.clone()));
    AuthzEvaluationPipeline::new(
        pool,
        Arc::new(EmergencyRevokeCache::default()),
        rebac_engine,
        filter_registry,
        Arc::new(NoOpJitFetcher),
        FailMode::Deny,
        cuckoo_filter,
        bitmap_engine,
        policy_bundle_cache,
        shadow_engine,
    )
}

fn make_request(
    tenant_id: TenantId,
    user_id: UserId,
    action: &str,
    resource_type: &str,
) -> AuthzRequest {
    let ctx = AuthzContext {
        tenant_id,
        user_id,
        user_attributes: JsonValue::Object(Default::default()),
        user_attributes_version: 0,
        resource: ResourceContext {
            resource_type: resource_type.to_owned(),
            resource_ref: None,
            attributes: JsonValue::Object(Default::default()),
        },
        env: EnvContext::default(),
        backend: FilterBackend::Sql,
    };
    AuthzRequest {
        tenant_id,
        user_id,
        action: action.to_owned(),
        context: ctx,
        include_trace: false,
    }
}

async fn seed_tenant(pool: &PgPool, tenant_id: TenantId, code: &str, name: &str) {
    sqlx::query("INSERT INTO tenant (id, code, name) VALUES ($1, $2, $3)")
        .bind(tenant_id.into_uuid())
        .bind(code)
        .bind(name)
        .execute(pool)
        .await
        .unwrap();
}

async fn seed_user(pool: &PgPool, user_id: UserId, tenant_id: TenantId, username: &str) {
    sqlx::query("INSERT INTO user_account (id, tenant_id, username) VALUES ($1, $2, $3)")
        .bind(user_id.into_uuid())
        .bind(tenant_id.into_uuid())
        .bind(username)
        .execute(pool)
        .await
        .unwrap();
}

async fn seed_role(
    pool: &PgPool,
    role_id: uuid::Uuid,
    tenant_id: TenantId,
    code: &str,
    name: &str,
) {
    sqlx::query("INSERT INTO role (id, tenant_id, code, name) VALUES ($1, $2, $3, $4)")
        .bind(role_id)
        .bind(tenant_id.into_uuid())
        .bind(code)
        .bind(name)
        .execute(pool)
        .await
        .unwrap();
}

async fn seed_permission(
    pool: &PgPool,
    perm_id: uuid::Uuid,
    tenant_id: TenantId,
    code: &str,
    resource_type: &str,
    action: &str,
) {
    sqlx::query("INSERT INTO permission (id, tenant_id, code, resource_type, action, scope) VALUES ($1, $2, $3, $4, $5, 'all')")
        .bind(perm_id).bind(tenant_id.into_uuid()).bind(code).bind(resource_type).bind(action)
        .execute(pool).await.unwrap();
}

async fn seed_role_permission(pool: &PgPool, role_id: uuid::Uuid, perm_id: uuid::Uuid) {
    sqlx::query("INSERT INTO role_permission (role_id, permission_id) VALUES ($1, $2)")
        .bind(role_id)
        .bind(perm_id)
        .execute(pool)
        .await
        .unwrap();
}

async fn seed_user_role(pool: &PgPool, user_id: UserId, role_id: uuid::Uuid, tenant_id: TenantId) {
    sqlx::query("INSERT INTO user_role (user_id, role_id, tenant_id) VALUES ($1, $2, $3)")
        .bind(user_id.into_uuid())
        .bind(role_id)
        .bind(tenant_id.into_uuid())
        .execute(pool)
        .await
        .unwrap();
}

// ─── Test 1: Allow path via RBAC + Bitmap fast-path ─────────────────────────

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn test_allow_path_via_bitmap_fast_path(pool: PgPool) {
    let tenant_id = TenantId::new();
    let user_id = UserId::new();
    let role_id = uuid::Uuid::new_v4();
    let perm_id = uuid::Uuid::new_v4();

    seed_tenant(&pool, tenant_id, "acme", "Acme Corp").await;
    seed_user(&pool, user_id, tenant_id, "alice").await;
    seed_role(&pool, role_id, tenant_id, "reader", "Reader").await;
    seed_permission(
        &pool,
        perm_id,
        tenant_id,
        "contract:read",
        "contract",
        "read",
    )
    .await;
    seed_role_permission(&pool, role_id, perm_id).await;
    seed_user_role(&pool, user_id, role_id, tenant_id).await;

    authz_db::find_tenant_by_id(&pool, tenant_id)
        .await
        .expect("tenant query must match the canonical schema");
    authz_db::find_user_by_id(&pool, tenant_id, user_id)
        .await
        .expect("user query must match the canonical schema");
    let effective = authz_db::fetch_effective_permissions(&pool, user_id, tenant_id, "contract")
        .await
        .expect("effective permission query must match the canonical schema");
    assert_eq!(effective.permissions.len(), 1);

    // BundleLoader populates Bitmap + Cuckoo filter from DB
    let loader = BundleLoader::new(pool.clone());
    let engines = loader.load_initial().await.unwrap();

    let pipeline = build_test_pipeline(
        pool,
        engines.bitmap_engine,
        engines.cuckoo_filter,
        engines.policy_bundle_cache,
    );

    let resp = pipeline
        .evaluate(&make_request(tenant_id, user_id, "read", "contract"))
        .await
        .unwrap();
    assert_eq!(
        resp.decision,
        AuthzDecision::Allow,
        "Valid RBAC grant must be Allowed; deny_reason={:?}",
        resp.deny_reason
    );
}

// ─── Test 2: Temporal deny — permission blocked outside its time window ───────

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn test_temporal_deny_blocks_outside_window(pool: PgPool) {
    let tenant_id = TenantId::new();
    let user_id = UserId::new();
    let role_id = uuid::Uuid::new_v4();
    let perm_id = uuid::Uuid::new_v4();

    seed_tenant(&pool, tenant_id, "night_bank", "Night Bank").await;
    seed_user(&pool, user_id, tenant_id, "bob").await;
    seed_role(&pool, role_id, tenant_id, "night_reader", "Night Reader").await;
    seed_permission(&pool, perm_id, tenant_id, "report:read", "report", "read").await;
    seed_role_permission(&pool, role_id, perm_id).await;
    seed_user_role(&pool, user_id, role_id, tenant_id).await;

    // Temporal policy: only allowed 23:58-23:59 UTC (1-minute midnight window)
    sqlx::query(
        "INSERT INTO temporal_policy \
         (id, permission_id, name, allowed_days, allowed_from, allowed_until, timezone, is_active) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
    )
    .bind(uuid::Uuid::new_v4())
    .bind(perm_id)
    .bind("midnight-only")
    .bind(vec![1i16, 2, 3, 4, 5, 6, 7])
    .bind(chrono::NaiveTime::from_hms_opt(23, 58, 0).unwrap())
    .bind(chrono::NaiveTime::from_hms_opt(23, 59, 0).unwrap())
    .bind("UTC")
    .bind(true)
    .execute(&pool)
    .await
    .unwrap();

    // Empty Bitmap forces full pipeline evaluation (no bitmap fast-path bypass)
    let bitmap = Arc::new(PermissionBitmapEngine::new(HashMap::new()));
    let cuckoo = Arc::new(PermissionCuckooFilter::new());
    let bundle_cache = Arc::new(PolicyBundleCache::new());

    let loader = BundleLoader::new(pool.clone());
    loader
        .refresh_temporal(&bundle_cache, tenant_id)
        .await
        .unwrap();

    let pipeline = build_test_pipeline(pool, bitmap, cuckoo, bundle_cache);

    // Request at 09:00 UTC — well outside the 23:58-23:59 window
    let mut req = make_request(tenant_id, user_id, "read", "report");
    req.context.env.request_time = Utc.with_ymd_and_hms(2024, 6, 17, 9, 0, 0).unwrap();

    let resp = pipeline.evaluate(&req).await.unwrap();
    assert_eq!(
        resp.decision,
        AuthzDecision::Deny,
        "Request outside temporal window must be Denied"
    );
}

// ─── Test 3: Tenant isolation — user from A cannot access under B ─────────────

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn test_tenant_isolation_denies_cross_tenant_access(pool: PgPool) {
    let tenant_a = TenantId::new();
    let tenant_b = TenantId::new();
    let user_id = UserId::new();
    let role_id = uuid::Uuid::new_v4();
    let perm_id = uuid::Uuid::new_v4();

    // Tenant A: user has full admin permissions
    seed_tenant(&pool, tenant_a, "bank_a", "Bank A").await;
    seed_tenant(&pool, tenant_b, "bank_b", "Bank B").await;
    seed_user(&pool, user_id, tenant_a, "charlie").await;
    seed_role(&pool, role_id, tenant_a, "admin", "Admin").await;
    seed_permission(&pool, perm_id, tenant_a, "order:delete", "order", "delete").await;
    seed_role_permission(&pool, role_id, perm_id).await;
    seed_user_role(&pool, user_id, role_id, tenant_a).await;

    // Empty engines — forces RBAC lookup which is tenant-scoped in the DB query
    let bitmap = Arc::new(PermissionBitmapEngine::new(HashMap::new()));
    let cuckoo = Arc::new(PermissionCuckooFilter::new());
    let bundle_cache = Arc::new(PolicyBundleCache::new());

    let pipeline = build_test_pipeline(pool, bitmap, cuckoo, bundle_cache);

    // User belongs to tenant A but request claims tenant B — RBAC finds no permissions
    let resp = pipeline
        .evaluate(&make_request(tenant_b, user_id, "delete", "order"))
        .await
        .unwrap();

    assert_eq!(
        resp.decision,
        AuthzDecision::Deny,
        "Cross-tenant access must be Denied"
    );
    assert_eq!(
        resp.deny_reason.as_deref(),
        Some("USER_NOT_FOUND"),
        "Deny reason must identify that the subject is not active in the target tenant"
    );
}
