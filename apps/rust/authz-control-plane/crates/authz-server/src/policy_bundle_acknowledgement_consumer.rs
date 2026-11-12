use anyhow::{Context, Result};
use bpmp_authz_contracts::authorization::v1::{
    AuthorizationPolicyBundle, AuthorizationPolicyBundleAcknowledgement,
};
use prost14::Message;
use rdkafka::{
    consumer::{CommitMode, Consumer, StreamConsumer},
    Message as _,
};
use sqlx::PgPool;
use uuid::Uuid;

use crate::{config::ServerConfig, tenant_lifecycle::kafka_client};

pub async fn run(pool: PgPool, config: ServerConfig) -> Result<()> {
    let consumer: StreamConsumer = kafka_client(&config)
        .set(
            "group.id",
            &config.authorization_policy_bundle_acknowledgement_consumer_group,
        )
        .set("enable.auto.commit", "false")
        .set("enable.auto.offset.store", "false")
        .set("auto.offset.reset", "earliest")
        .create()?;
    consumer.subscribe(&[&config.authorization_policy_bundle_acknowledgement_topic])?;
    loop {
        let message = consumer.recv().await?;
        let payload = message
            .payload()
            .context("authorization bundle acknowledgement has no payload")?;
        if payload.len() > config.kafka_max_message_bytes {
            anyhow::bail!("authorization bundle acknowledgement exceeds configured byte limit");
        }
        let ack = AuthorizationPolicyBundleAcknowledgement::decode(payload)
            .context("decode authorization bundle acknowledgement")?;
        if ack.encode_to_vec() != payload
            || ack.schema_version != 1
            || ack.acknowledgement_id.trim().is_empty()
            || ack.consumer_id.trim().is_empty()
            || ack.consumer_type.trim().is_empty()
            || ack.bundle_sequence == 0
            || ack.catalog_version == 0
            || ack.acknowledged_at_epoch_ms == 0
            || ack.bundle_content_hash.len() != 32
            || message.key() != Some(ack.tenant_id.as_bytes())
            || ack.acknowledgement_id
                != format!(
                    "{}/{}/{}",
                    ack.consumer_id, ack.tenant_id, ack.bundle_sequence
                )
        {
            anyhow::bail!("authorization bundle acknowledgement metadata is invalid");
        }
        let tenant_id = Uuid::parse_str(&ack.tenant_id)
            .context("authorization acknowledgement tenant id is invalid")?;
        let (catalog_version_id, bundle_bytes): (Uuid, Vec<u8>) = sqlx::query_as(
            "SELECT catalog_version_id, bundle_bytes FROM authorization_policy_bundle_outbox WHERE tenant_id = $1 AND bundle_sequence = $2",
        ).bind(tenant_id).bind(i64::try_from(ack.bundle_sequence)?).fetch_optional(&pool).await?.context("acknowledged policy bundle is not published by Authz")?;
        let bundle = AuthorizationPolicyBundle::decode(bundle_bytes.as_slice())
            .context("decode Authz-owned policy bundle")?;
        if bundle.tenant_id != ack.tenant_id
            || bundle.bundle_sequence != ack.bundle_sequence
            || bundle.catalog_version != ack.catalog_version
            || bundle.content_hash != ack.bundle_content_hash
        {
            anyhow::bail!("acknowledgement does not match the published signed bundle");
        }
        let mut tx = pool.begin().await?;
        let affected = sqlx::query(
            r#"INSERT INTO authorization_policy_bundle_acknowledgement
                 (tenant_id, bundle_sequence, catalog_version_id, consumer_id, consumer_type, bundle_content_hash, acknowledged_at_epoch_ms)
               VALUES ($1,$2,$3,$4,$5,$6,$7)
               ON CONFLICT (tenant_id,bundle_sequence,consumer_id) DO UPDATE
                 SET acknowledged_at_epoch_ms = GREATEST(authorization_policy_bundle_acknowledgement.acknowledged_at_epoch_ms, EXCLUDED.acknowledged_at_epoch_ms)
               WHERE authorization_policy_bundle_acknowledgement.catalog_version_id = EXCLUDED.catalog_version_id
                 AND authorization_policy_bundle_acknowledgement.consumer_type = EXCLUDED.consumer_type
                 AND authorization_policy_bundle_acknowledgement.bundle_content_hash = EXCLUDED.bundle_content_hash"#,
        ).bind(tenant_id).bind(i64::try_from(ack.bundle_sequence)?).bind(catalog_version_id).bind(&ack.consumer_id).bind(&ack.consumer_type).bind(&ack.bundle_content_hash).bind(i64::try_from(ack.acknowledged_at_epoch_ms)?).execute(&mut *tx).await?.rows_affected();
        if affected != 1 {
            anyhow::bail!(
                "consumer acknowledgement conflicts with previously recorded bundle state"
            );
        }
        tx.commit().await?;
        consumer.commit_message(&message, CommitMode::Sync)?;
    }
}
