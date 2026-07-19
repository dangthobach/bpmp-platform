use authz_core::{
    ids::TenantId,
    models::tenant::{FailMode, TenantConfig},
    AuthzError,
};
use authz_db::repositories::tenant_write::{
    delete_tenant, insert_tenant, update_tenant, update_tenant_status, CreateTenant,
    TenantMutationAudit, TenantStatus, UpdateTenant,
};
use sqlx::PgPool;

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn tenant_update_combines_row_lock_and_expected_version(pool: PgPool) {
    let tenant_id = TenantId::new();
    sqlx::query("INSERT INTO tenant (id, code, name) VALUES ($1, 'lock-test', 'Lock Test')")
        .bind(tenant_id.into_uuid())
        .execute(&pool)
        .await
        .unwrap();

    let next_version = update_tenant_status(
        &pool,
        tenant_id,
        TenantStatus::Suspended,
        0,
        TenantMutationAudit {
            actor_ref: "test-service",
            request_id: "request-1",
        },
    )
    .await
    .unwrap();
    assert_eq!(next_version, 1);

    let error = update_tenant_status(
        &pool,
        tenant_id,
        TenantStatus::Active,
        0,
        TenantMutationAudit {
            actor_ref: "test-service",
            request_id: "request-2",
        },
    )
    .await
    .unwrap_err();
    assert!(matches!(
        error,
        AuthzError::VersionConflict {
            expected_version: 0,
            actual_version: 1,
            ..
        }
    ));
}

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn soft_deleted_tenant_is_hidden_from_normal_reads(pool: PgPool) {
    let tenant_id = TenantId::new();
    sqlx::query("INSERT INTO tenant (id, code, name) VALUES ($1, 'deleted', 'Deleted')")
        .bind(tenant_id.into_uuid())
        .execute(&pool)
        .await
        .unwrap();

    sqlx::query("UPDATE tenant SET is_deleted = true WHERE id = $1")
        .bind(tenant_id.into_uuid())
        .execute(&pool)
        .await
        .unwrap();

    let error = authz_db::find_tenant_by_id(&pool, tenant_id)
        .await
        .unwrap_err();
    assert!(matches!(error, AuthzError::TenantNotFound { .. }));

    let row: (i64, bool, Option<chrono::DateTime<chrono::Utc>>) =
        sqlx::query_as("SELECT version, is_deleted, deleted_at FROM tenant WHERE id = $1")
            .bind(tenant_id.into_uuid())
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(row.0, 1);
    assert!(row.1);
    assert!(row.2.is_some());
}

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn tenant_crud_is_dynamic_versioned_and_audited(pool: PgPool) {
    let tenant_id = TenantId::new();
    let config = TenantConfig {
        fail_mode: FailMode::Deny,
        rebac_max_depth: 12,
        shadow_mode_enabled: true,
    };
    let created = insert_tenant(
        &pool,
        CreateTenant {
            tenant_id,
            code: "dynamic-tenant",
            name: "Dynamic Tenant",
            config: &config,
        },
        audit("create-request"),
    )
    .await
    .unwrap();
    assert_eq!(created.code, "dynamic-tenant");
    assert_eq!(created.config, config);
    assert!(created.is_active);
    assert_eq!(created.metadata.version, 0);

    let listed = authz_db::list_tenants_for_admin(&pool, None, 10)
        .await
        .unwrap();
    assert_eq!(listed.len(), 1);
    assert_eq!(listed[0].id, tenant_id);

    let updated = update_tenant(
        &pool,
        tenant_id,
        UpdateTenant {
            code: None,
            name: Some("Dynamic Tenant Updated"),
            config: None,
            is_active: Some(false),
            expected_version: 0,
        },
        audit("update-request"),
    )
    .await
    .unwrap();
    assert_eq!(updated.name, "Dynamic Tenant Updated");
    assert!(!updated.is_active);
    assert_eq!(updated.metadata.version, 1);
    assert!(matches!(
        authz_db::find_tenant_by_id(&pool, tenant_id)
            .await
            .unwrap_err(),
        AuthzError::TenantInactive { .. }
    ));

    let deleted_version = delete_tenant(&pool, tenant_id, 1, audit("delete-request"))
        .await
        .unwrap();
    assert_eq!(deleted_version, 2);
    assert!(matches!(
        authz_db::get_tenant_for_admin(&pool, tenant_id)
            .await
            .unwrap_err(),
        AuthzError::TenantNotFound { .. }
    ));
    let audits: Vec<(String, i64)> = sqlx::query_as(
        "SELECT operation, entity_version FROM tenant_audit_log \
         WHERE tenant_id = $1 ORDER BY entity_version",
    )
    .bind(tenant_id.into_uuid())
    .fetch_all(&pool)
    .await
    .unwrap();
    assert_eq!(
        audits,
        vec![
            ("CREATE".into(), 0),
            ("UPDATE".into(), 1),
            ("DELETE".into(), 2),
        ]
    );
}

fn audit(request_id: &str) -> TenantMutationAudit<'_> {
    TenantMutationAudit {
        actor_ref: "tenant-admin-service",
        request_id,
    }
}
