//! Transactional Outbox port.
//!
//! Inserts MUST execute on the same transaction that mutates aggregates.
//! A background worker publishes pending rows to Kafka and marks them processed.

use async_trait::async_trait;
use serde_json::Value as JsonValue;
use uuid::Uuid;

use crate::application::errors::AppError;
use crate::domain::organization::DomainEvent;

#[derive(Debug, Clone)]
pub struct OutboxRecord {
    pub id: Uuid,
    pub tenant_id: Uuid,
    pub aggregate_type: &'static str,
    pub aggregate_id: Uuid,
    pub event_type: &'static str,
    pub payload: JsonValue,
    /// Old/new value pair for audit. `None` for non-update events.
    pub diff: Option<JsonValue>,
}

#[async_trait]
pub trait OutboxRepository: Send + Sync {
    async fn enqueue(&mut self, record: OutboxRecord) -> Result<(), AppError>;

    /// Convenience: build the record from a domain event + audit diff.
    async fn enqueue_event(
        &mut self,
        evt: &DomainEvent,
        aggregate_id: Uuid,
        diff: Option<JsonValue>,
    ) -> Result<(), AppError> {
        let record = OutboxRecord {
            id: Uuid::new_v4(),
            tenant_id: evt.tenant_id(),
            aggregate_type: "organization",
            aggregate_id,
            event_type: evt.event_type(),
            payload: serde_json::to_value(evt).map_err(|e| AppError::Internal(e.to_string()))?,
            diff,
        };
        self.enqueue(record).await
    }
}
