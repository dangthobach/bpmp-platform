use std::sync::Arc;

use thiserror::Error;

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct OutboxRecord {
    pub cursor: u64,
    pub tenant_id: String,
    pub instance_id: String,
    pub event_id: String,
    pub payload: Vec<u8>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct PublishAcknowledgement {
    pub event_id: String,
}

#[allow(clippy::missing_errors_doc)]
pub trait OutboxStorePort: Send + Sync {
    /// Reads the durable publisher checkpoint used for crash recovery.
    fn publisher_checkpoint(&self) -> Result<u64, OutboxError>;

    /// Returns records strictly after `cursor`, ordered by ascending cursor.
    ///
    /// # Errors
    ///
    /// Returns [`OutboxError`] when durable outbox state cannot be read.
    fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError>;

    /// Advances the durable publisher checkpoint using compare-and-swap.
    ///
    /// # Errors
    ///
    /// Returns [`OutboxError::CheckpointConflict`] when another publisher has
    /// advanced the cursor, or another storage error on failure.
    fn checkpoint(&self, expected: u64, committed: u64) -> Result<(), OutboxError>;
}

impl<T: OutboxStorePort + ?Sized> OutboxStorePort for Arc<T> {
    fn publisher_checkpoint(&self) -> Result<u64, OutboxError> {
        (**self).publisher_checkpoint()
    }

    fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError> {
        (**self).read_after(cursor, limit)
    }

    fn checkpoint(&self, expected: u64, committed: u64) -> Result<(), OutboxError> {
        (**self).checkpoint(expected, committed)
    }
}

pub trait IntegrationEventPublisherPort: Send + Sync {
    /// Publishes one committed event and returns only after broker acknowledgement.
    ///
    /// # Errors
    ///
    /// Returns [`OutboxError`] when publication is rejected or unavailable.
    fn publish(&self, record: &OutboxRecord) -> Result<PublishAcknowledgement, OutboxError>;
}

pub trait RetryDelayPort: Send + Sync {
    fn wait(&self, delay_ms: u64);
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct OutboxPublisherConfig {
    batch_size: usize,
    max_publish_attempts: u32,
    initial_retry_delay_ms: u64,
    max_retry_delay_ms: u64,
    retry_multiplier_millis: u32,
}

impl OutboxPublisherConfig {
    /// Creates bounded publisher settings from deployment configuration.
    ///
    /// # Errors
    ///
    /// Rejects zero values, an invalid delay range, or a multiplier below 1.0.
    pub const fn new(
        batch_size: usize,
        max_publish_attempts: u32,
        initial_retry_delay_ms: u64,
        max_retry_delay_ms: u64,
        retry_multiplier_millis: u32,
    ) -> Result<Self, OutboxError> {
        if batch_size == 0
            || max_publish_attempts == 0
            || initial_retry_delay_ms == 0
            || max_retry_delay_ms < initial_retry_delay_ms
            || retry_multiplier_millis < 1_000
        {
            return Err(OutboxError::InvalidConfiguration);
        }
        Ok(Self {
            batch_size,
            max_publish_attempts,
            initial_retry_delay_ms,
            max_retry_delay_ms,
            retry_multiplier_millis,
        })
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct PublishBatchOutcome {
    pub published: usize,
    pub checkpoint: u64,
}

pub struct OutboxPublisher<S, P, D> {
    store: S,
    publisher: P,
    delay: D,
    config: OutboxPublisherConfig,
}

impl<S, P, D> OutboxPublisher<S, P, D>
where
    S: OutboxStorePort,
    P: IntegrationEventPublisherPort,
    D: RetryDelayPort,
{
    pub const fn new(store: S, publisher: P, delay: D, config: OutboxPublisherConfig) -> Self {
        Self {
            store,
            publisher,
            delay,
            config,
        }
    }

    /// Publishes at most one configured batch from a durable checkpoint.
    ///
    /// Broker acknowledgement always precedes checkpoint advancement. A crash
    /// between those operations republishes the same event on recovery.
    ///
    /// # Errors
    ///
    /// Fails on malformed ordering, oversized adapter output, exhausted
    /// publish retries, acknowledgement mismatch, or checkpoint failure.
    pub fn run_once(&self, initial_checkpoint: u64) -> Result<PublishBatchOutcome, OutboxError> {
        let records = self
            .store
            .read_after(initial_checkpoint, self.config.batch_size)?;
        validate_batch(&records, initial_checkpoint, self.config.batch_size)?;
        let mut checkpoint = initial_checkpoint;
        for record in &records {
            self.publish_with_retry(record)?;
            self.store.checkpoint(checkpoint, record.cursor)?;
            checkpoint = record.cursor;
        }
        Ok(PublishBatchOutcome {
            published: records.len(),
            checkpoint,
        })
    }

    fn publish_with_retry(&self, record: &OutboxRecord) -> Result<(), OutboxError> {
        let mut delay_ms = self.config.initial_retry_delay_ms;
        for attempt in 1..=self.config.max_publish_attempts {
            match self.publisher.publish(record) {
                Ok(ack) if ack.event_id == record.event_id => return Ok(()),
                Ok(_) => return Err(OutboxError::AcknowledgementMismatch),
                Err(error) if attempt == self.config.max_publish_attempts => return Err(error),
                Err(_) => {
                    self.delay.wait(delay_ms);
                    delay_ms = next_delay(delay_ms, self.config);
                }
            }
        }
        Err(OutboxError::PublishAttemptsExhausted)
    }
}

fn validate_batch(
    records: &[OutboxRecord],
    checkpoint: u64,
    configured_limit: usize,
) -> Result<(), OutboxError> {
    if records.len() > configured_limit {
        return Err(OutboxError::BatchLimitExceeded);
    }
    let mut previous = checkpoint;
    for record in records {
        if record.cursor <= previous || record.event_id.is_empty() || record.payload.is_empty() {
            return Err(OutboxError::InvalidRecordOrder);
        }
        previous = record.cursor;
    }
    Ok(())
}

fn next_delay(current: u64, config: OutboxPublisherConfig) -> u64 {
    current
        .saturating_mul(u64::from(config.retry_multiplier_millis))
        .saturating_div(1_000)
        .min(config.max_retry_delay_ms)
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum OutboxError {
    #[error("outbox publisher configuration is invalid")]
    InvalidConfiguration,
    #[error("outbox adapter returned more than the configured batch limit")]
    BatchLimitExceeded,
    #[error("outbox records are empty, malformed, duplicated, or out of order")]
    InvalidRecordOrder,
    #[error("broker acknowledgement does not match the published event")]
    AcknowledgementMismatch,
    #[error("publish attempts were exhausted")]
    PublishAttemptsExhausted,
    #[error("outbox checkpoint compare-and-swap conflict")]
    CheckpointConflict,
    #[error("outbox storage is unavailable: {0}")]
    StoreUnavailable(String),
    #[error("integration broker is unavailable: {0}")]
    BrokerUnavailable(String),
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};

    use super::*;

    struct Store {
        records: Vec<OutboxRecord>,
        checkpoint: Mutex<u64>,
        fail_checkpoint_once: AtomicBool,
    }

    impl OutboxStorePort for Store {
        fn publisher_checkpoint(&self) -> Result<u64, OutboxError> {
            Ok(*self.checkpoint.lock().unwrap())
        }

        fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError> {
            Ok(self
                .records
                .iter()
                .filter(|record| record.cursor > cursor)
                .take(limit)
                .cloned()
                .collect())
        }

        fn checkpoint(&self, expected: u64, committed: u64) -> Result<(), OutboxError> {
            if self.fail_checkpoint_once.swap(false, Ordering::AcqRel) {
                return Err(OutboxError::StoreUnavailable("injected crash".into()));
            }
            let mut checkpoint = self.checkpoint.lock().unwrap();
            if *checkpoint != expected {
                return Err(OutboxError::CheckpointConflict);
            }
            *checkpoint = committed;
            Ok(())
        }
    }

    #[derive(Default)]
    struct Publisher {
        published: Mutex<Vec<String>>,
        failures_remaining: AtomicU32,
    }

    impl IntegrationEventPublisherPort for Publisher {
        fn publish(&self, record: &OutboxRecord) -> Result<PublishAcknowledgement, OutboxError> {
            if self
                .failures_remaining
                .fetch_update(Ordering::AcqRel, Ordering::Acquire, |remaining| {
                    remaining.checked_sub(1)
                })
                .is_ok()
            {
                return Err(OutboxError::BrokerUnavailable("retry".into()));
            }
            self.published.lock().unwrap().push(record.event_id.clone());
            Ok(PublishAcknowledgement {
                event_id: record.event_id.clone(),
            })
        }
    }

    #[derive(Default)]
    struct Delay(Mutex<Vec<u64>>);

    impl RetryDelayPort for Delay {
        fn wait(&self, delay_ms: u64) {
            self.0.lock().unwrap().push(delay_ms);
        }
    }

    fn record(cursor: u64) -> OutboxRecord {
        OutboxRecord {
            cursor,
            tenant_id: "tenant-a".into(),
            instance_id: "instance-1".into(),
            event_id: format!("event-{cursor}"),
            payload: vec![1],
        }
    }

    fn config() -> OutboxPublisherConfig {
        OutboxPublisherConfig::new(10, 3, 10, 100, 2_000).unwrap()
    }

    #[test]
    fn publishes_in_order_and_checkpoints_after_ack() {
        let runtime = OutboxPublisher::new(
            Store {
                records: vec![record(1), record(2)],
                checkpoint: Mutex::new(0),
                fail_checkpoint_once: AtomicBool::new(false),
            },
            Publisher::default(),
            Delay::default(),
            config(),
        );
        assert_eq!(
            runtime.run_once(0).unwrap(),
            PublishBatchOutcome {
                published: 2,
                checkpoint: 2
            }
        );
        assert_eq!(
            &*runtime.publisher.published.lock().unwrap(),
            &["event-1".to_owned(), "event-2".to_owned()]
        );
    }

    #[test]
    fn retries_with_bounded_backoff() {
        let publisher = Publisher::default();
        publisher.failures_remaining.store(2, Ordering::Release);
        let runtime = OutboxPublisher::new(
            Store {
                records: vec![record(1)],
                checkpoint: Mutex::new(0),
                fail_checkpoint_once: AtomicBool::new(false),
            },
            publisher,
            Delay::default(),
            config(),
        );
        runtime.run_once(0).unwrap();
        assert_eq!(&*runtime.delay.0.lock().unwrap(), &[10, 20]);
    }

    #[test]
    fn crash_after_ack_republishes_without_losing_event() {
        let runtime = OutboxPublisher::new(
            Store {
                records: vec![record(1)],
                checkpoint: Mutex::new(0),
                fail_checkpoint_once: AtomicBool::new(true),
            },
            Publisher::default(),
            Delay::default(),
            config(),
        );
        assert!(matches!(
            runtime.run_once(0),
            Err(OutboxError::StoreUnavailable(_))
        ));
        assert_eq!(*runtime.store.checkpoint.lock().unwrap(), 0);
        runtime.run_once(0).unwrap();
        assert_eq!(
            &*runtime.publisher.published.lock().unwrap(),
            &["event-1".to_owned(), "event-1".to_owned()]
        );
        assert_eq!(*runtime.store.checkpoint.lock().unwrap(), 1);
    }
}
