pub const POLICY_BUNDLE_TTL_MS: u64 = 86_400_000;

use anyhow::{Context, Result};
use prost14::Message as Prost14Message;
use sqlx::{PgPool, Row};
#[cfg(target_os = "linux")]
use std::time::Duration;
use uuid::Uuid;

#[cfg(target_os = "linux")]
use bpmp_authz_contracts::authorization::v1::{
    AuthorizationPolicyBundle, AuthorizationPolicyBundlePublication,
};

#[allow(dead_code)]
const POLICY_BUNDLE_RENEW_LEAD_MS: u64 = 21_600_000;
#[cfg(target_os = "linux")]
const POLICY_BUNDLE_RENEW_INTERVAL: Duration = Duration::from_secs(300);
#[allow(dead_code)]
const POLICY_BUNDLE_RENEW_BATCH: i64 = 500;

#[cfg(target_os = "linux")]
pub async fn run(
    pool: PgPool,
    config: crate::config::ServerConfig,
    signing: Option<crate::state::PolicyBundleSigning>,
    max_bundle_bytes: usize,
) -> Result<()> {
    use rdkafka::{
        message::{Header, OwnedHeaders},
        producer::{FutureProducer, FutureRecord},
        util::Timeout,
    };
    let producer: FutureProducer = crate::tenant_lifecycle::kafka_client(&config)
        .set("enable.idempotence", "true")
        .set("acks", "all")
        .set("max.in.flight.requests.per.connection", "1")
        .set("message.timeout.ms", "10000")
        .create()?;
    let mut next_renewal = tokio::time::Instant::now();
    loop {
        if tokio::time::Instant::now() >= next_renewal {
            next_renewal = tokio::time::Instant::now() + POLICY_BUNDLE_RENEW_INTERVAL;
            if let Some(signing) = signing.as_ref() {
                if let Err(error) = renew_expiring_bundles(&pool, signing, max_bundle_bytes).await {
                    tracing::warn!(%error, "authorization bundle renewal failed; will retry");
                }
            }
        }

        // Lock the oldest pending row through broker delivery so multiple
        // Authz replicas cannot publish sequence N+1 before N.
        let mut transaction = pool.begin().await?;
        let row = sqlx::query(
            "SELECT event_id,tenant_id,bundle_sequence,catalog_version_id,bundle_bytes FROM authorization_policy_bundle_outbox WHERE delivered_at IS NULL ORDER BY created_at,event_id LIMIT 1 FOR UPDATE",
        )
        .fetch_optional(&mut *transaction)
        .await?;
        let Some(row) = row else {
            transaction.commit().await?;
            tokio::time::sleep(Duration::from_millis(500)).await;
            continue;
        };
        let event_id: Uuid = row.get("event_id");
        let tenant_id: Uuid = row.get("tenant_id");
        let sequence: i64 = row.get("bundle_sequence");
        let catalog_version_id: Uuid = row.get("catalog_version_id");
        let bundle_bytes: Vec<u8> = row.get("bundle_bytes");
        let bundle = AuthorizationPolicyBundle::decode(bundle_bytes.as_slice())
            .context("decode stored signed authorization bundle")?;
        let publication = AuthorizationPolicyBundlePublication {
            event_id: event_id.to_string(),
            tenant_id: tenant_id.to_string(),
            bundle_sequence: u64::try_from(sequence)?,
            catalog_version: u64::try_from(bundle.catalog_version)?,
            published_at_epoch_ms: chrono::Utc::now().timestamp_millis().max(1) as u64,
            bundle_bytes,
        };
        if publication.tenant_id != bundle.tenant_id
            || publication.bundle_sequence != bundle.bundle_sequence
            || publication.catalog_version != bundle.catalog_version
            || catalog_version_id.is_nil()
        {
            anyhow::bail!("outbox metadata differs from signed policy bundle");
        }
        let bytes = publication.encode_to_vec();
        if bytes.len() > config.kafka_max_message_bytes {
            anyhow::bail!("authorization policy publication exceeds Kafka message limit");
        }
        match producer
            .send(
                FutureRecord::to(&config.authorization_policy_bundle_topic)
                    .key(&publication.tenant_id)
                    .payload(&bytes)
                    .headers(OwnedHeaders::new().insert(Header {
                        key: "x-bpmp-event-id",
                        value: Some(&publication.event_id),
                    })),
                Timeout::After(Duration::from_secs(10)),
            )
            .await
        {
            Ok(_) => {
                sqlx::query("UPDATE authorization_policy_bundle_outbox SET delivered_at=now(),delivery_attempts=delivery_attempts+1,last_delivery_error=NULL WHERE event_id=$1 AND delivered_at IS NULL")
                    .bind(event_id).execute(&mut *transaction).await?;
                transaction.commit().await?;
            }
            Err((error, _)) => {
                sqlx::query("UPDATE authorization_policy_bundle_outbox SET delivery_attempts=delivery_attempts+1,last_delivery_error=$2 WHERE event_id=$1 AND delivered_at IS NULL")
                    .bind(event_id).bind(error.to_string()).execute(&mut *transaction).await?;
                transaction.commit().await?;
                tracing::warn!(%event_id, %error, "authorization policy bundle delivery failed; outbox retained");
                tokio::time::sleep(Duration::from_secs(1)).await;
            }
        }
    }
}

#[allow(dead_code)]
async fn renew_expiring_bundles(
    pool: &PgPool,
    signing: &crate::state::PolicyBundleSigning,
    max_bundle_bytes: usize,
) -> Result<()> {
    let now = chrono::Utc::now().timestamp_millis().max(1) as u64;
    let renew_before = now.saturating_add(POLICY_BUNDLE_RENEW_LEAD_MS);
    let candidates = sqlx::query(
        r#"SELECT active.tenant_id
           FROM authorization_catalog_version active
           JOIN LATERAL (
               SELECT bundle_expires_at_epoch_ms
               FROM authorization_policy_bundle_outbox
               WHERE tenant_id = active.tenant_id
               ORDER BY bundle_sequence DESC LIMIT 1
           ) latest ON TRUE
           WHERE active.state = 'PUBLISHED'
             AND latest.bundle_expires_at_epoch_ms <= $1
             AND NOT EXISTS (
                 SELECT 1 FROM authorization_policy_bundle_outbox pending
                 WHERE pending.tenant_id = active.tenant_id AND pending.delivered_at IS NULL
             )
           ORDER BY latest.bundle_expires_at_epoch_ms, active.tenant_id
           LIMIT $2"#,
    )
    .bind(i64::try_from(renew_before)?)
    .bind(POLICY_BUNDLE_RENEW_BATCH)
    .fetch_all(pool)
    .await?;

    for candidate in candidates {
        let tenant_id: Uuid = candidate.get("tenant_id");
        if let Err(error) = renew_one_expiring_bundle(
            pool,
            signing,
            max_bundle_bytes,
            tenant_id,
            now,
            renew_before,
        )
        .await
        {
            tracing::error!(%tenant_id, %error, "failed to renew authorization bundle for tenant");
        }
    }
    Ok(())
}

#[allow(dead_code)]
async fn renew_one_expiring_bundle(
    pool: &PgPool,
    signing: &crate::state::PolicyBundleSigning,
    max_bundle_bytes: usize,
    tenant_id: Uuid,
    now: u64,
    renew_before: u64,
) -> Result<()> {
    use bpmp_authz_contracts::{AuthorizationArtifactLimits, AuthorizationBundleCodec};

    let mut transaction = pool.begin().await?;
    // Match the lock order used by publish_version; this serializes policy
    // publication and renewal while keeping bundle sequence monotonic.
    sqlx::query("SELECT id FROM tenant WHERE id = $1 FOR UPDATE")
        .bind(tenant_id)
        .fetch_optional(&mut *transaction)
        .await?;
    let row = sqlx::query(
        r#"SELECT version.id AS catalog_version_id, version.version_num,
                  outbox.bundle_sequence, outbox.bundle_bytes,
                  outbox.bundle_expires_at_epoch_ms
           FROM authorization_catalog_version version
           JOIN LATERAL (
               SELECT bundle_sequence, bundle_bytes, bundle_expires_at_epoch_ms
               FROM authorization_policy_bundle_outbox
               WHERE tenant_id = version.tenant_id
               ORDER BY bundle_sequence DESC LIMIT 1
           ) outbox ON TRUE
           WHERE version.tenant_id = $1 AND version.state = 'PUBLISHED'
             AND NOT EXISTS (
                 SELECT 1 FROM authorization_policy_bundle_outbox pending
                 WHERE pending.tenant_id = version.tenant_id AND pending.delivered_at IS NULL
             )
           FOR UPDATE OF version"#,
    )
    .bind(tenant_id)
    .fetch_optional(&mut *transaction)
    .await?;
    let Some(row) = row else {
        transaction.commit().await?;
        return Ok(());
    };
    let catalog_version_id: Uuid = row.get("catalog_version_id");
    let version_num: i64 = row.get("version_num");
    let current_sequence: i64 = row.get("bundle_sequence");
    let current_expiry: i64 = row.get("bundle_expires_at_epoch_ms");
    if current_expiry > i64::try_from(renew_before)? {
        transaction.commit().await?;
        return Ok(());
    }
    let bundle_bytes: Vec<u8> = row.get("bundle_bytes");
    let mut bundle = bpmp_authz_contracts::authorization::v1::AuthorizationPolicyBundle::decode(
        bundle_bytes.as_slice(),
    )
    .context("decode active authorization bundle for renewal")?;
    if bundle.tenant_id != tenant_id.to_string()
        || bundle.bundle_sequence != u64::try_from(current_sequence)?
        || bundle.catalog_version != u64::try_from(version_num)?
    {
        anyhow::bail!("active authorization bundle metadata differs from database");
    }
    let sequence = u64::try_from(current_sequence)?
        .checked_add(1)
        .context("authorization bundle sequence overflow")?;
    let expires_at = now.saturating_add(POLICY_BUNDLE_TTL_MS);
    bundle.bundle_sequence = sequence;
    bundle.valid_from_epoch_ms = now.saturating_sub(5_000);
    bundle.expires_at_epoch_ms = expires_at;
    let limits = AuthorizationArtifactLimits::new(max_bundle_bytes, 10_000)?;
    let renewed_bytes =
        AuthorizationBundleCodec::seal(bundle, &signing.key_id, signing.signer.as_ref(), limits)
            .context("sign renewed authorization bundle")?;
    sqlx::query(
        "INSERT INTO authorization_policy_bundle_outbox (tenant_id, catalog_version_id, bundle_sequence, bundle_bytes, bundle_expires_at_epoch_ms) VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(tenant_id)
    .bind(catalog_version_id)
    .bind(i64::try_from(sequence)?)
    .bind(renewed_bytes)
    .bind(i64::try_from(expires_at)?)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;
    tracing::info!(%tenant_id, bundle_sequence = sequence, catalog_version = version_num, "renewed authorization bundle validity");
    Ok(())
}
