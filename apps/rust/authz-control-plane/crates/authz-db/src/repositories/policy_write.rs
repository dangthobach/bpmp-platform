use authz_core::{ids::TenantId, AuthzError};
use sqlx::PgPool;
use uuid::Uuid;

pub async fn insert_policy(
    pool: &PgPool,
    policy_id: Uuid,
    tenant_id: TenantId,
    name: &str,
    description: Option<&str>,
) -> Result<(), AuthzError> {
    sqlx::query(
        r#"
        INSERT INTO policy (id, tenant_id, name, description, is_active)
        VALUES ($1, $2, $3, $4, true)
        "#,
    )
    .bind(policy_id)
    .bind(tenant_id.into_uuid())
    .bind(name)
    .bind(description)
    .execute(pool)
    .await?;
    Ok(())
}

pub async fn insert_policy_version(
    pool: &PgPool,
    version_id: Uuid,
    policy_id: Uuid,
    policy_content: &str,
    status: &str,
    notes: &str,
) -> Result<(), AuthzError> {
    let snapshot: serde_json::Value = serde_json::json!({
        "content": policy_content
    });

    sqlx::query(
        r#"
        INSERT INTO policy_version (id, policy_id, version_num, snapshot, status, notes)
        VALUES (
            $1, $2, 
            COALESCE((SELECT MAX(version_num) + 1 FROM policy_version WHERE policy_id = $2), 1),
            $3, $4, $5
        )
        "#,
    )
    .bind(version_id)
    .bind(policy_id)
    .bind(snapshot)
    .bind(status)
    .bind(notes)
    .execute(pool)
    .await?;
    Ok(())
}

pub async fn promote_policy_version(pool: &PgPool, version_id: Uuid) -> Result<(), AuthzError> {
    let mut tx = pool.begin().await?;

    let row: Option<(Uuid, String)> = sqlx::query_as(
        r#"
        SELECT policy_id, status
        FROM policy_version
        WHERE id = $1 AND is_deleted = false
        FOR UPDATE
        "#,
    )
    .bind(version_id)
    .fetch_optional(&mut *tx)
    .await?;
    let Some((policy_id, status)) = row else {
        return Err(AuthzError::PolicyVersionNotFound { version_id });
    };
    if status != "DRAFT" && status != "SHADOW" {
        return Err(AuthzError::InvalidPolicyState {
            version_id,
            expected_state: "DRAFT or SHADOW",
        });
    }

    sqlx::query("SELECT id FROM policy WHERE id = $1 AND is_deleted = false FOR UPDATE")
        .bind(policy_id)
        .fetch_one(&mut *tx)
        .await?;

    sqlx::query(
        "UPDATE policy_version SET status = 'ARCHIVED' \
         WHERE policy_id = $1 AND status = 'ACTIVE' AND is_deleted = false",
    )
    .bind(policy_id)
    .execute(&mut *tx)
    .await?;

    sqlx::query(
        "UPDATE policy_version SET status = 'ACTIVE', published_at = now() \
         WHERE id = $1 AND is_deleted = false",
    )
    .bind(version_id)
    .execute(&mut *tx)
    .await?;

    tx.commit().await?;
    Ok(())
}
