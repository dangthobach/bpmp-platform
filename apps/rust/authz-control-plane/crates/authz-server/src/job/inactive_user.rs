use authz_db::repositories::tenant_write::deactivate_inactive_users;
use sqlx::PgPool;
use std::time::Duration;
use tokio::time;
use tracing::{error, info, warn};

/// Starts the background job that scans for and deactivates inactive users.
///
/// This loop runs indefinitely, waking up based on `interval_secs`. When it wakes up,
/// it deactivates users in batches until no more users match the criteria.
pub async fn start_inactive_user_job(
    pool: PgPool,
    interval_secs: u64,
    inactive_days: i32,
    batch_size: i64,
) {
    info!(
        "Starting Inactive User Deactivation Job (Interval: {}s, Threshold: {} days, Batch: {})",
        interval_secs, inactive_days, batch_size
    );

    let mut interval = time::interval(Duration::from_secs(interval_secs));

    // The first tick fires immediately, so we loop right away.
    loop {
        interval.tick().await;

        info!("Running inactive user scan...");

        let mut total_deactivated: u64 = 0;
        let mut retry_count = 0;

        loop {
            match deactivate_inactive_users(&pool, inactive_days, batch_size).await {
                Ok(0) => {
                    // No more rows matched, break the inner loop.
                    if total_deactivated > 0 {
                        info!(
                            "Completed inactive user scan. Total deactivated: {}",
                            total_deactivated
                        );
                    } else {
                        info!("Inactive user scan complete. No users required deactivation.");
                    }
                    break;
                }
                Ok(rows_affected) => {
                    total_deactivated += rows_affected;
                    retry_count = 0; // reset retry counter on success

                    // Small yield to allow other tasks to process
                    // and not completely monopolize CPU if the table is huge.
                    tokio::task::yield_now().await;
                }
                Err(e) => {
                    error!("Error during inactive user deactivation batch: {}", e);
                    retry_count += 1;
                    if retry_count >= 3 {
                        warn!("Too many errors in inactive user scan. Aborting this run.");
                        break;
                    }
                    // Wait a bit before retrying the batch
                    time::sleep(Duration::from_secs(5)).await;
                }
            }
        }
    }
}
