use crate::contracts::bpmp::authorization::v1::AuthorizationQueryRevokeEvent;
use anyhow::Result;
use ed25519_dalek::{Signer, SigningKey};
use prost::Message;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use std::path::Path;
#[cfg(target_os="linux")]
use std::time::Duration;
use uuid::Uuid;

const DOMAIN: &[u8] = b"bpmp.authz.query-revoke.v1\0";
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub topic: String,
    pub worker_id: String,
    pub batch_size: i64,
    pub lease_ms: i64,
    pub poll_ms: u64,
    pub delivery_timeout_ms: u64,
    pub retry_backoff_ms: u64,
    pub max_retry_attempts: u32,
    pub signing_key_id: String,
    pub signing_key_path: String,
}
impl Config {
    pub fn load(path: &Path) -> Result<Self> {
        let value: Self = serde_json::from_slice(&std::fs::read(path)?)?;
        value.validate()?;
        Ok(value)
    }
    pub fn validate(&self) -> Result<()> {
        let parts = self.topic.split('.').collect::<Vec<_>>();
        if parts.len() != 5
            || parts[0] != "bpmp"
            || !parts[3].starts_with('v')
            || parts.iter().any(|p| {
                p.is_empty()
                    || !p
                        .bytes()
                        .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
            })
            || self.topic.len() > 249
            || self.worker_id.is_empty()
            || !(1..=1000).contains(&self.batch_size)
            || self.lease_ms <= 0
            || self.poll_ms == 0
            || self.poll_ms > 300000
            || self.delivery_timeout_ms == 0
            || self.delivery_timeout_ms > 300000
            || self.lease_ms as u64 <= self.delivery_timeout_ms * 2
            || self.retry_backoff_ms == 0
            || self.retry_backoff_ms > 300000
            || !(1..=20).contains(&self.max_retry_attempts)
            || self.signing_key_id.is_empty()
            || self.signing_key_path.is_empty()
        {
            anyhow::bail!("invalid bounded query revoke publisher configuration");
        }
        Ok(())
    }
}
#[derive(Clone, Debug)]
pub struct Record {
    pub event_id: Uuid,
    pub tenant_id: Uuid,
    pub actor_id: Uuid,
    pub epoch: i64,
    pub sequence: i64,
    pub revoked: bool,
    pub occurred_at_ms: i64,
}
pub struct Batch {
    pub generation: i64,
    pub records: Vec<Record>,
}
pub async fn claim(pool: &PgPool, config: &Config) -> Result<Option<Batch>> {
    let mut tx = pool.begin().await?;
    let row=sqlx::query("SELECT checkpoint,lease_generation,(lease_until>now()) AS leased FROM query_revoke_publish_state WHERE singleton FOR UPDATE").fetch_one(&mut *tx).await?;
    if row.get::<Option<bool>, _>("leased").unwrap_or(false) {
        tx.commit().await?;
        return Ok(None);
    }
    let checkpoint: i64 = row.get("checkpoint");
    let rows=sqlx::query("SELECT event_id,tenant_id,actor_id,revoke_epoch,sequence,revoked,(extract(epoch FROM occurred_at)*1000)::bigint AS occurred FROM query_revoke_feed WHERE sequence>$1 ORDER BY sequence LIMIT $2")
        .bind(checkpoint).bind(config.batch_size).fetch_all(&mut *tx).await?;
    if rows.is_empty() {
        tx.commit().await?;
        return Ok(None);
    }
    let generation:i64=sqlx::query_scalar("UPDATE query_revoke_publish_state SET lease_generation=lease_generation+1,lease_owner=$1,lease_until=now()+$2::bigint*interval '1 millisecond',updated_at=now() WHERE singleton RETURNING lease_generation")
        .bind(&config.worker_id).bind(config.lease_ms).fetch_one(&mut *tx).await?;
    let records = rows
        .iter()
        .map(|r| Record {
            event_id: r.get("event_id"),
            tenant_id: r.get("tenant_id"),
            actor_id: r.get("actor_id"),
            epoch: r.get("revoke_epoch"),
            sequence: r.get("sequence"),
            revoked: r.get("revoked"),
            occurred_at_ms: r.get("occurred"),
        })
        .collect();
    tx.commit().await?;
    Ok(Some(Batch {
        generation,
        records,
    }))
}
pub async fn renew(pool: &PgPool, config: &Config, generation: i64) -> Result<()> {
    let changed=sqlx::query("UPDATE query_revoke_publish_state SET lease_until=now()+$3::bigint*interval '1 millisecond',updated_at=now() WHERE singleton AND lease_owner=$1 AND lease_generation=$2 AND lease_until>now()")
        .bind(&config.worker_id).bind(generation).bind(config.lease_ms).execute(pool).await?.rows_affected();
    if changed != 1 {
        anyhow::bail!("query revoke publisher lease lost")
    };
    Ok(())
}
pub async fn checkpoint(
    pool: &PgPool,
    config: &Config,
    generation: i64,
    sequence: i64,
) -> Result<()> {
    let changed=sqlx::query("UPDATE query_revoke_publish_state SET checkpoint=GREATEST(checkpoint,$3),last_error=NULL,updated_at=now() WHERE singleton AND lease_owner=$1 AND lease_generation=$2 AND lease_until>now()")
        .bind(&config.worker_id).bind(generation).bind(sequence).execute(pool).await?.rows_affected();
    if changed != 1 {
        anyhow::bail!("query revoke checkpoint fence rejected")
    };
    Ok(())
}
pub fn payload(record: &Record, key: &SigningKey, key_id: &str) -> Result<Vec<u8>> {
    let mut event = AuthorizationQueryRevokeEvent {
        schema_version: 1,
        event_id: record.event_id.to_string(),
        tenant_id: record.tenant_id.to_string(),
        actor_id: record.actor_id.to_string(),
        revoke_epoch: u64::try_from(record.epoch)?,
        event_sequence: u64::try_from(record.sequence)?,
        revoked: record.revoked,
        occurred_at_epoch_ms: u64::try_from(record.occurred_at_ms)?,
        correlation_id: record.event_id.to_string(),
        signing_key_id: key_id.into(),
        content_hash: Vec::new(),
        signature: Vec::new(),
    };
    let hash = Sha256::digest(event.encode_to_vec());
    let mut message = DOMAIN.to_vec();
    message.extend_from_slice(&hash);
    event.content_hash = hash.to_vec();
    event.signature = key.sign(&message).to_bytes().to_vec();
    Ok(event.encode_to_vec())
}

#[cfg(target_os = "linux")]
pub async fn run(pool: PgPool, server: crate::config::ServerConfig) -> Result<()> {
    use rdkafka::{
        producer::{FutureProducer, FutureRecord},
        util::Timeout,
    };
    let initial = Config::load(Path::new(&server.query_revoke_kafka_config))?;
    let bytes = std::fs::read(&initial.signing_key_path)?;
    let seed: [u8; 32] = bytes
        .try_into()
        .map_err(|_| anyhow::anyhow!("query revoke signing seed must contain 32 bytes"))?;
    let key = SigningKey::from_bytes(&seed);
    let producer: FutureProducer = crate::tenant_lifecycle::kafka_client(&server)
        .set("enable.idempotence", "true")
        .set("acks", "all")
        .set("max.in.flight.requests.per.connection", "1")
        .set(
            "message.timeout.ms",
            initial.delivery_timeout_ms.to_string(),
        )
        .set(
            "queue.buffering.max.messages",
            initial.batch_size.to_string(),
        )
        .create()?;
    loop {
        // The deployment configuration is read once per batch, never during its lease.
        let config = Config::load(Path::new(&server.query_revoke_kafka_config))?;
        if config.topic != initial.topic
            || config.signing_key_id != initial.signing_key_id
            || config.signing_key_path != initial.signing_key_path
            || config.delivery_timeout_ms != initial.delivery_timeout_ms
            || config.batch_size > initial.batch_size
        {
            anyhow::bail!("revoke transport/key/queue expansion requires a rolling restart");
        }
        let Some(batch) = claim(&pool, &config).await? else {
            tokio::time::sleep(Duration::from_millis(config.poll_ms)).await;
            continue;
        };
        let mut failed = false;
        for record in &batch.records {
            let bytes = payload(record, &key, &config.signing_key_id)?;
            if bytes.len() > server.kafka_max_message_bytes {
                anyhow::bail!("signed revoke event exceeds transport bound")
            }
            let mut delivered = false;
            for attempt in 0..config.max_retry_attempts {
                if renew(&pool, &config, batch.generation).await.is_err() {
                    break;
                }
                let result = producer
                    .send(
                        FutureRecord::to(&config.topic)
                            .key(&record.tenant_id.to_string())
                            .payload(&bytes),
                        Timeout::After(Duration::from_millis(config.delivery_timeout_ms)),
                    )
                    .await;
                if result.is_ok() {
                    delivered = true;
                    break;
                }
                tracing::warn!(
                    sequence = record.sequence,
                    attempt,
                    "query revoke broker delivery failed; checkpoint retained"
                );
                tokio::time::sleep(Duration::from_millis(config.retry_backoff_ms)).await;
            }
            if !delivered {
                failed = true;
                break;
            }
            if checkpoint(&pool, &config, batch.generation, record.sequence)
                .await
                .is_err()
            {
                failed = true;
                break;
            }
        }
        sqlx::query("UPDATE query_revoke_publish_state SET lease_owner=NULL,lease_until=NULL,failure_count=failure_count+$3,last_error=$4,updated_at=now() WHERE singleton AND lease_owner=$1 AND lease_generation=$2")
            .bind(&config.worker_id).bind(batch.generation).bind(i64::from(failed)).bind(if failed{Some("delivery_or_lease_failure")}else{None}).execute(&pool).await?;
        if failed {
            tokio::time::sleep(Duration::from_millis(config.retry_backoff_ms)).await;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn payload_is_deterministic_and_signed() {
        let key = SigningKey::from_bytes(&[7; 32]);
        let record = Record {
            event_id: Uuid::nil(),
            tenant_id: Uuid::nil(),
            actor_id: Uuid::nil(),
            epoch: 1,
            sequence: 9,
            revoked: true,
            occurred_at_ms: 1234,
        };
        let a = payload(&record, &key, "k1").unwrap();
        assert_eq!(a, payload(&record, &key, "k1").unwrap());
        let event = AuthorizationQueryRevokeEvent::decode(a.as_slice()).unwrap();
        assert_eq!(event.signature.len(), 64);
    }
}
