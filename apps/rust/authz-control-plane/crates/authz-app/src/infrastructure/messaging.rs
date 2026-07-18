//! Outbox publisher background worker.
//!
//! Polls `app_outbox` for pending rows, publishes them to an audit / Kafka sink,
//! and marks them processed. Includes a pluggable [`OutboxSink`] so tests can
//! capture published events without a broker. The default sink writes to the
//! `tracing` log — replace with `rdkafka` in production wiring.

use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use serde_json::Value as JsonValue;
use sqlx::PgPool;
use tokio::time::sleep;
use uuid::Uuid;

#[derive(Debug, Clone)]
pub struct PublishedEvent {
    pub id: Uuid,
    pub tenant_id: Uuid,
    pub aggregate_type: String,
    pub event_type: String,
    pub payload: JsonValue,
}

#[async_trait]
pub trait OutboxSink: Send + Sync {
    async fn publish(&self, evt: &PublishedEvent) -> anyhow::Result<()>;
}

pub struct LoggingSink;

#[async_trait]
impl OutboxSink for LoggingSink {
    async fn publish(&self, evt: &PublishedEvent) -> anyhow::Result<()> {
        tracing::info!(
            event_id = %evt.id,
            event_type = %evt.event_type,
            tenant = %evt.tenant_id,
            "outbox event published"
        );
        Ok(())
    }
}

pub struct OutboxWorker {
    pool: PgPool,
    sink: Arc<dyn OutboxSink>,
    poll_interval: Duration,
    batch_size: i64,
}

impl OutboxWorker {
    pub fn new(
        pool: PgPool,
        sink: Arc<dyn OutboxSink>,
        poll_interval: Duration,
        batch_size: i64,
    ) -> Self {
        Self {
            pool,
            sink,
            poll_interval,
            batch_size,
        }
    }

    pub async fn run(self) {
        loop {
            match self.tick().await {
                Ok(n) if n > 0 => {
                    metrics::counter!("authz_app_outbox_published_total").increment(n as u64);
                }
                Ok(_) => {}
                Err(e) => {
                    tracing::error!(error = %e, "outbox tick failed");
                    metrics::counter!("authz_app_outbox_errors_total").increment(1);
                }
            }
            sleep(self.poll_interval).await;
        }
    }

    async fn tick(&self) -> anyhow::Result<i64> {
        // Hold a single transaction across SELECT + publish + UPDATE so that
        // `FOR UPDATE SKIP LOCKED` is actually effective: row locks survive
        // until COMMIT, preventing other workers from picking the same rows.
        // Trade-off: lock duration ≈ batch publish latency. Bounded by
        // `batch_size`, which the operator tunes.
        let mut tx = self.pool.begin().await?;

        let rows: Vec<(Uuid, Uuid, String, String, JsonValue)> = sqlx::query_as(
            "SELECT id, tenant_id, aggregate_type, event_type, payload \
             FROM app_outbox WHERE processed_at IS NULL \
             ORDER BY id LIMIT $1 \
             FOR UPDATE SKIP LOCKED",
        )
        .bind(self.batch_size)
        .fetch_all(&mut *tx)
        .await?;

        if rows.is_empty() {
            tx.commit().await?;
            return Ok(0);
        }

        for (id, tenant, atype, etype, payload) in &rows {
            let evt = PublishedEvent {
                id: *id,
                tenant_id: *tenant,
                aggregate_type: atype.clone(),
                event_type: etype.clone(),
                payload: payload.clone(),
            };
            // At-least-once: publish before marking. A crash between publish
            // and COMMIT leaves the row unprocessed → republished on retry.
            // Consumers MUST be idempotent (use `id` as dedup key).
            self.sink.publish(&evt).await?;
            sqlx::query("UPDATE app_outbox SET processed_at = now() WHERE id = $1")
                .bind(id)
                .execute(&mut *tx)
                .await?;
        }
        tx.commit().await?;
        Ok(rows.len() as i64)
    }
}
