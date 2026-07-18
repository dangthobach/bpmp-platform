//! Outbox writer scoped to the active transaction. Rows are appended atomically
//! with the aggregate change; a separate worker publishes and marks them.

use async_trait::async_trait;
use sqlx::{Postgres, Transaction};

use crate::application::errors::AppError;
use crate::application::ports::outbox::{OutboxRecord, OutboxRepository};

pub(crate) async fn enqueue(
    tx: &mut Transaction<'static, Postgres>,
    rec: OutboxRecord,
) -> Result<(), AppError> {
    sqlx::query(
        "INSERT INTO app_outbox \
            (id, tenant_id, aggregate_type, aggregate_id, event_type, payload, diff) \
         VALUES ($1,$2,$3,$4,$5,$6,$7)",
    )
    .bind(rec.id)
    .bind(rec.tenant_id)
    .bind(rec.aggregate_type)
    .bind(rec.aggregate_id)
    .bind(rec.event_type)
    .bind(rec.payload)
    .bind(rec.diff)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

pub struct PgOutboxRepoView;

#[async_trait]
impl OutboxRepository for PgOutboxRepoView {
    async fn enqueue(&mut self, _record: OutboxRecord) -> Result<(), AppError> {
        Err(AppError::Internal("call via UnitOfWork".into()))
    }
}
