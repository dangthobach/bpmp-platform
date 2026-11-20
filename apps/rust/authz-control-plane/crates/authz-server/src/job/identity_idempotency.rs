use sqlx::PgPool;
use std::time::Duration;

/// Prunes terminal identity mutation receipts in bounded batches. The receipt
/// TTL is stored per command; this worker only applies that retention policy.
pub async fn start_identity_idempotency_cleanup(pool: PgPool) {
    let mut interval = tokio::time::interval(Duration::from_secs(3600));
    loop {
        interval.tick().await;
        for _ in 0..10 {
            match sqlx::query(
                r#"DELETE FROM identity_profile_idempotency
                   WHERE ctid IN (
                       SELECT ctid FROM identity_profile_idempotency
                       WHERE expires_at <= now()
                       ORDER BY expires_at
                       LIMIT 500
                   )"#,
            )
            .execute(&pool)
            .await
            {
                Ok(result) if result.rows_affected() > 0 => continue,
                Ok(_) => break,
                Err(error) => {
                    tracing::warn!(%error, "identity idempotency cleanup batch failed");
                    break;
                }
            }
        }
    }
}
