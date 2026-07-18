use authz_core::{ids::TenantId, AuthzError};
use authz_db::repositories::tenant_write::{update_tenant_status, TenantStatus};
use sqlx::PgPool;

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn tenant_update_combines_row_lock_and_expected_version(pool: PgPool) {
    let tenant_id = TenantId::new();
    sqlx::query("INSERT INTO tenant (id, code, name) VALUES ($1, 'lock-test', 'Lock Test')")
        .bind(tenant_id.into_uuid())
        .execute(&pool)
        .await
        .unwrap();

    let next_version = update_tenant_status(&pool, tenant_id, TenantStatus::Suspended, 0)
        .await
        .unwrap();
    assert_eq!(next_version, 1);

    let error = update_tenant_status(&pool, tenant_id, TenantStatus::Active, 0)
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
