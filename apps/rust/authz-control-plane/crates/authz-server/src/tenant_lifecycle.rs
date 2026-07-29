use std::time::Duration;

use anyhow::{Context, Result};
use authz_core::ids::TenantId;
use authz_db::{apply_tenant_configuration_readiness, TenantConfigurationReadiness};
use prost::Message;
use rdkafka::consumer::{CommitMode, Consumer, StreamConsumer};
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::util::Timeout;
use rdkafka::{ClientConfig, Message as _};
use sqlx::PgPool;
use tokio::time::sleep;
use uuid::Uuid;

use crate::config::ServerConfig;
use crate::contracts::bpmp::configuration::v1::ConfigurationOwner;
use crate::contracts::bpmp::tenancy::v1::{
    TenantConfigurationReadinessEvent, TenantLifecycleEvent, TenantLifecycleKind,
};

#[derive(Debug)]
struct LifecycleRecord {
    event_id: Uuid,
    event_sequence: i64,
    tenant_id: Uuid,
    tenant_code: String,
    tenant_version: i64,
    lifecycle_kind: String,
    actor_ref: String,
    correlation_id: String,
    occurred_at_epoch_ms: i64,
}

pub async fn run_publisher(pool: PgPool, config: ServerConfig) -> Result<()> {
    let producer: FutureProducer = kafka_client(&config).create()?;
    loop {
        let records = claim_lifecycle_batch(&pool, &config).await?;
        if records.is_empty() {
            sleep(Duration::from_millis(config.tenant_lifecycle_poll_ms)).await;
            continue;
        }
        for record in &records {
            let payload = lifecycle_payload(record)?;
            producer
                .send(
                    FutureRecord::to(&config.tenant_lifecycle_topic)
                        .key(&record.tenant_id.to_string())
                        .payload(&payload),
                    Timeout::After(Duration::from_millis(
                        config.tenant_lifecycle_lease_ms as u64,
                    )),
                )
                .await
                .map_err(|(error, _)| error)
                .context("publish tenant lifecycle event")?;
        }
        complete_lifecycle_batch(&pool, &config, &records).await?;
    }
}

pub async fn run_readiness_consumer(pool: PgPool, config: ServerConfig) -> Result<()> {
    let consumer: StreamConsumer = kafka_client(&config)
        .set("group.id", &config.tenant_readiness_consumer_group)
        .set("enable.auto.commit", "false")
        .set("enable.auto.offset.store", "false")
        .set("auto.offset.reset", "earliest")
        .create()?;
    consumer.subscribe(&[&config.tenant_readiness_topic])?;
    loop {
        let message = consumer.recv().await?;
        let payload = message
            .payload()
            .context("tenant readiness event has no payload")?;
        if payload.len() > config.kafka_max_message_bytes {
            anyhow::bail!("tenant readiness event exceeds configured byte limit");
        }
        let event = TenantConfigurationReadinessEvent::decode(payload)
            .context("decode tenant readiness event")?;
        validate_readiness(&event)?;
        let tenant_id = Uuid::parse_str(&event.tenant_id)
            .context("tenant readiness tenant id is not a UUID")?;
        let event_id =
            Uuid::parse_str(&event.event_id).context("tenant readiness event id is not a UUID")?;
        let hash: [u8; 32] = event
            .profile_set_hash
            .as_slice()
            .try_into()
            .context("tenant readiness profile hash must contain 32 bytes")?;
        apply_tenant_configuration_readiness(
            &pool,
            TenantConfigurationReadiness {
                tenant_id: TenantId::from_uuid(tenant_id),
                tenant_version: i64::try_from(event.tenant_version)
                    .context("tenant version exceeds i64")?,
                ready: event.ready,
                profile_set_hash: &hash,
                event_id,
                event_sequence: i64::try_from(event.event_sequence)
                    .context("readiness event sequence exceeds i64")?,
            },
        )
        .await?;
        consumer.commit_message(&message, CommitMode::Sync)?;
    }
}

fn kafka_client(config: &ServerConfig) -> ClientConfig {
    let mut client = ClientConfig::new();
    client
        .set("bootstrap.servers", config.kafka_brokers.join(","))
        .set("client.id", &config.kafka_client_id)
        .set("security.protocol", &config.kafka_security_protocol)
        .set(
            "message.max.bytes",
            config.kafka_max_message_bytes.to_string(),
        )
        .set(
            "fetch.message.max.bytes",
            config.kafka_max_message_bytes.to_string(),
        );
    if config.kafka_security_protocol == "SSL" {
        client
            .set(
                "ssl.ca.location",
                config.kafka_ca_file.as_deref().unwrap_or_default(),
            )
            .set(
                "ssl.certificate.location",
                config.kafka_certificate_file.as_deref().unwrap_or_default(),
            )
            .set(
                "ssl.key.location",
                config.kafka_private_key_file.as_deref().unwrap_or_default(),
            );
    }
    client
}

async fn claim_lifecycle_batch(
    pool: &PgPool,
    config: &ServerConfig,
) -> Result<Vec<LifecycleRecord>> {
    let mut tx = pool.begin().await?;
    let (checkpoint, lease_owner, lease_until): (
        i64,
        Option<String>,
        Option<chrono::DateTime<chrono::Utc>>,
    ) = sqlx::query_as(
        "SELECT checkpoint,lease_owner,lease_until \
         FROM tenant_lifecycle_publish_state WHERE singleton_id=1 FOR UPDATE",
    )
    .fetch_one(&mut *tx)
    .await?;
    if lease_owner.is_some_and(|_| lease_until.is_some_and(|until| until > chrono::Utc::now())) {
        tx.commit().await?;
        return Ok(Vec::new());
    }
    let rows: Vec<(
        Uuid,
        i64,
        Uuid,
        String,
        i64,
        String,
        String,
        String,
        chrono::DateTime<chrono::Utc>,
    )> = sqlx::query_as(
        "SELECT event_id,event_sequence,tenant_id,tenant_code,tenant_version,\
         lifecycle_kind,actor_ref,correlation_id,occurred_at \
         FROM tenant_lifecycle_outbox \
         WHERE event_sequence>$1 AND published_at IS NULL \
         ORDER BY event_sequence LIMIT $2",
    )
    .bind(checkpoint)
    .bind(config.tenant_lifecycle_batch_size)
    .fetch_all(&mut *tx)
    .await?;
    if rows.is_empty() {
        tx.commit().await?;
        return Ok(Vec::new());
    }
    if rows[0].1 != checkpoint + 1 {
        anyhow::bail!("tenant lifecycle outbox sequence is not contiguous");
    }
    let records = rows
        .into_iter()
        .map(
            |(
                event_id,
                event_sequence,
                tenant_id,
                tenant_code,
                tenant_version,
                lifecycle_kind,
                actor_ref,
                correlation_id,
                occurred_at,
            )| LifecycleRecord {
                event_id,
                event_sequence,
                tenant_id,
                tenant_code,
                tenant_version,
                lifecycle_kind,
                actor_ref,
                correlation_id,
                occurred_at_epoch_ms: occurred_at.timestamp_millis(),
            },
        )
        .collect::<Vec<_>>();
    let first = records[0].event_sequence;
    let last = records[records.len() - 1].event_sequence;
    let affected = sqlx::query(
        "UPDATE tenant_lifecycle_outbox SET lease_owner=$1,\
         lease_until=clock_timestamp()+($2::bigint * interval '1 millisecond'),\
         attempt_count=attempt_count+1 \
         WHERE event_sequence BETWEEN $3 AND $4 AND published_at IS NULL",
    )
    .bind(&config.tenant_lifecycle_worker_id)
    .bind(config.tenant_lifecycle_lease_ms)
    .bind(first)
    .bind(last)
    .execute(&mut *tx)
    .await?
    .rows_affected();
    if affected != records.len() as u64 {
        tx.rollback().await?;
        return Ok(Vec::new());
    }
    sqlx::query(
        "UPDATE tenant_lifecycle_publish_state SET lease_owner=$1,\
         lease_until=clock_timestamp()+($2::bigint * interval '1 millisecond'),\
         updated_at=clock_timestamp() WHERE singleton_id=1",
    )
    .bind(&config.tenant_lifecycle_worker_id)
    .bind(config.tenant_lifecycle_lease_ms)
    .execute(&mut *tx)
    .await?;
    tx.commit().await?;
    Ok(records)
}

async fn complete_lifecycle_batch(
    pool: &PgPool,
    config: &ServerConfig,
    records: &[LifecycleRecord],
) -> Result<()> {
    let mut tx = pool.begin().await?;
    let checkpoint: i64 = sqlx::query_scalar(
        "SELECT checkpoint FROM tenant_lifecycle_publish_state \
         WHERE singleton_id=1 AND lease_owner=$1 FOR UPDATE",
    )
    .bind(&config.tenant_lifecycle_worker_id)
    .fetch_one(&mut *tx)
    .await?;
    if records[0].event_sequence != checkpoint + 1 {
        anyhow::bail!("tenant lifecycle publisher lease or checkpoint was lost");
    }
    let first = records[0].event_sequence;
    let last = records[records.len() - 1].event_sequence;
    let affected = sqlx::query(
        "UPDATE tenant_lifecycle_outbox SET published_at=clock_timestamp(),\
         lease_owner=NULL,lease_until=NULL \
         WHERE event_sequence BETWEEN $1 AND $2 AND lease_owner=$3 \
         AND published_at IS NULL",
    )
    .bind(first)
    .bind(last)
    .bind(&config.tenant_lifecycle_worker_id)
    .execute(&mut *tx)
    .await?
    .rows_affected();
    if affected != records.len() as u64 {
        anyhow::bail!("tenant lifecycle outbox batch changed before completion");
    }
    sqlx::query(
        "UPDATE tenant_lifecycle_publish_state SET checkpoint=$1,\
         lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() \
         WHERE singleton_id=1",
    )
    .bind(last)
    .execute(&mut *tx)
    .await?;
    tx.commit().await?;
    Ok(())
}

fn lifecycle_payload(record: &LifecycleRecord) -> Result<Vec<u8>> {
    let kind = match record.lifecycle_kind.as_str() {
        "CREATED" => TenantLifecycleKind::Created,
        "UPDATED" => TenantLifecycleKind::Updated,
        "ACTIVATION_REQUESTED" => TenantLifecycleKind::ActivationRequested,
        "ACTIVATED" => TenantLifecycleKind::Activated,
        "SUSPENDED" => TenantLifecycleKind::Suspended,
        "DELETED" => TenantLifecycleKind::Deleted,
        _ => anyhow::bail!("tenant lifecycle outbox contains an invalid kind"),
    };
    let event = TenantLifecycleEvent {
        schema_version: 1,
        event_id: record.event_id.to_string(),
        event_sequence: u64::try_from(record.event_sequence)?,
        tenant_id: record.tenant_id.to_string(),
        tenant_code: record.tenant_code.clone(),
        tenant_version: u64::try_from(record.tenant_version)?,
        kind: kind as i32,
        actor_ref: record.actor_ref.clone(),
        correlation_id: record.correlation_id.clone(),
        occurred_at_epoch_ms: u64::try_from(record.occurred_at_epoch_ms)?,
    };
    let mut payload = Vec::with_capacity(event.encoded_len());
    event.encode(&mut payload)?;
    Ok(payload)
}

fn validate_readiness(event: &TenantConfigurationReadinessEvent) -> Result<()> {
    if event.schema_version != 1
        || event.event_id.trim().is_empty()
        || event.event_sequence == 0
        || event.tenant_id.trim().is_empty()
        || event.profile_set_hash.len() != 32
        || event.occurred_at_epoch_ms == 0
        || event.missing_owners.iter().any(|owner| {
            ConfigurationOwner::try_from(*owner)
                .map_or(true, |value| value == ConfigurationOwner::Unspecified)
        })
        || (event.ready && !event.missing_owners.is_empty())
    {
        anyhow::bail!("tenant readiness event metadata is invalid");
    }
    Ok(())
}
