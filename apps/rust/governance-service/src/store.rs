use std::time::Duration;

use anyhow::{Context, Result};
use bpmp_contracts::governance::v1::{
    AbortAndReconcileSpec, ApprovalRequestStatus, ApprovalRequestView, ApprovalRole,
    PrepareAbortAndReconcileResponse, SignedApproval,
};
use serde_json::json;
use sqlx::postgres::PgPoolOptions;
use sqlx::{PgPool, Postgres, Row, Transaction};

use crate::config::PostgresConfig;

#[derive(Clone)]
pub struct GovernanceStore {
    pool: PgPool,
}

impl GovernanceStore {
    pub async fn connect(config: &PostgresConfig) -> Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(config.max_connections)
            .acquire_timeout(Duration::from_millis(config.acquire_timeout_ms))
            .connect(&config.dsn)
            .await
            .context("connect governance PostgreSQL")?;
        Ok(Self { pool })
    }

    pub const fn pool(&self) -> &PgPool {
        &self.pool
    }

    pub async fn create(
        &self,
        request_id: &str,
        idempotency_key: &str,
        spec: &AbortAndReconcileSpec,
        prepared: &PrepareAbortAndReconcileResponse,
        now: u64,
    ) -> Result<ApprovalRequestView> {
        if let Some(existing) = self
            .find_by_idempotency(&spec.tenant_id, idempotency_key)
            .await?
        {
            return Ok(existing);
        }
        let mut tx = self.pool.begin().await?;
        sqlx::query(
            "INSERT INTO governance_approval_requests(
                tenant_id,request_id,idempotency_key,instance_id,workflow_type,workflow_version,
                policy_id,legal_deadline_epoch_ms,key_scope,key_epoch,reason_code,request_digest,
                pending_ledger_digest,expected_version,config_version,policy_version,status,
                created_at_epoch_ms,updated_at_epoch_ms
             ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'PENDING',$17,$17)",
        )
        .bind(&spec.tenant_id)
        .bind(request_id)
        .bind(idempotency_key)
        .bind(&spec.instance_id)
        .bind(&spec.workflow_type)
        .bind(&spec.workflow_version)
        .bind(&spec.policy_id)
        .bind(i64_value(spec.legal_deadline_epoch_ms)?)
        .bind(&spec.key_scope)
        .bind(i64_value(spec.key_epoch)?)
        .bind(&spec.reason_code)
        .bind(&prepared.request_digest)
        .bind(&prepared.pending_ledger_digest)
        .bind(i64_value(prepared.expected_version)?)
        .bind(&prepared.config_version)
        .bind(&prepared.policy_version)
        .bind(i64_value(now)?)
        .execute(&mut *tx)
        .await?;
        append_audit(
            &mut tx,
            &spec.tenant_id,
            request_id,
            "CREATED",
            "system",
            1,
            now,
        )
        .await?;
        tx.commit().await?;
        self.get(&spec.tenant_id, request_id).await
    }

    pub async fn record_approval(
        &self,
        tenant_id: &str,
        request_id: &str,
        role: ApprovalRole,
        approval: &SignedApproval,
        required_approvers: u32,
        now: u64,
    ) -> Result<ApprovalRequestView> {
        let role_name = match role {
            ApprovalRole::Requester => "REQUESTER",
            ApprovalRole::Approver => "APPROVER",
            ApprovalRole::Unspecified => anyhow::bail!("approval role is unspecified"),
        };
        let mut tx = self.pool.begin().await?;
        let row = sqlx::query(
            "SELECT request_digest,status,aggregate_version FROM governance_approval_requests
             WHERE tenant_id=$1 AND request_id=$2 FOR UPDATE",
        )
        .bind(tenant_id)
        .bind(request_id)
        .fetch_optional(&mut *tx)
        .await?
        .context("approval request does not exist")?;
        let digest: Vec<u8> = row.try_get("request_digest")?;
        let status: String = row.try_get("status")?;
        let version: i64 = row.try_get("aggregate_version")?;
        if !matches!(status.as_str(), "PENDING" | "APPROVED")
            || approval.tenant_id != tenant_id
            || approval.request_digest != digest
        {
            anyhow::bail!("approval does not match an open request");
        }
        let inserted = sqlx::query(
            "INSERT INTO governance_signed_approvals(
                tenant_id,request_id,role,actor_id,request_digest,capability,auth_assurance,
                approved_at_epoch_ms,expires_at_epoch_ms,key_id,signature
             ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
             ON CONFLICT DO NOTHING",
        )
        .bind(tenant_id)
        .bind(request_id)
        .bind(role_name)
        .bind(&approval.actor_id)
        .bind(&approval.request_digest)
        .bind(&approval.capability)
        .bind(&approval.auth_assurance)
        .bind(i64_value(approval.approved_at_epoch_ms)?)
        .bind(i64_value(approval.expires_at_epoch_ms)?)
        .bind(&approval.key_id)
        .bind(&approval.signature)
        .execute(&mut *tx)
        .await?
        .rows_affected();
        if inserted == 0 {
            tx.rollback().await?;
            return self.get(tenant_id, request_id).await;
        }
        let requester_count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM governance_signed_approvals
             WHERE tenant_id=$1 AND request_id=$2 AND role='REQUESTER'",
        )
        .bind(tenant_id)
        .bind(request_id)
        .fetch_one(&mut *tx)
        .await?;
        let approver_count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM governance_signed_approvals
             WHERE tenant_id=$1 AND request_id=$2 AND role='APPROVER'",
        )
        .bind(tenant_id)
        .bind(request_id)
        .fetch_one(&mut *tx)
        .await?;
        let next_status = if requester_count == 1 && approver_count >= i64::from(required_approvers)
        {
            "APPROVED"
        } else {
            "PENDING"
        };
        let next_version = version
            .checked_add(1)
            .context("aggregate version overflow")?;
        sqlx::query(
            "UPDATE governance_approval_requests
             SET status=$3,aggregate_version=$4,updated_at=now(),updated_at_epoch_ms=$5
             WHERE tenant_id=$1 AND request_id=$2",
        )
        .bind(tenant_id)
        .bind(request_id)
        .bind(next_status)
        .bind(next_version)
        .bind(i64_value(now)?)
        .execute(&mut *tx)
        .await?;
        append_audit(
            &mut tx,
            tenant_id,
            request_id,
            "APPROVAL_RECORDED",
            &approval.actor_id,
            next_version,
            now,
        )
        .await?;
        tx.commit().await?;
        self.get(tenant_id, request_id).await
    }

    pub async fn mark_committed(
        &self,
        tenant_id: &str,
        request_id: &str,
        expected_aggregate_version: u64,
        command_id: &str,
        committed_sequence: u64,
        now: u64,
    ) -> Result<ApprovalRequestView> {
        let next_version = expected_aggregate_version
            .checked_add(1)
            .context("aggregate version overflow")?;
        let mut tx = self.pool.begin().await?;
        let changed = sqlx::query(
            "UPDATE governance_approval_requests
             SET status='KEY_SHRED_PENDING',aggregate_version=$4,committed_command_id=$5,
                 committed_sequence=$6,shred_available_at=now(),updated_at=now(),
                 updated_at_epoch_ms=$7
             WHERE tenant_id=$1 AND request_id=$2 AND aggregate_version=$3
               AND status='APPROVED'",
        )
        .bind(tenant_id)
        .bind(request_id)
        .bind(i64_value(expected_aggregate_version)?)
        .bind(i64_value(next_version)?)
        .bind(command_id)
        .bind(i64_value(committed_sequence)?)
        .bind(i64_value(now)?)
        .execute(&mut *tx)
        .await?
        .rows_affected();
        if changed != 1 {
            anyhow::bail!("approval request changed before commit projection");
        }
        append_audit(
            &mut tx,
            tenant_id,
            request_id,
            "ENGINE_COMMITTED",
            "system",
            i64_value(next_version)?,
            now,
        )
        .await?;
        tx.commit().await?;
        self.get(tenant_id, request_id).await
    }

    pub async fn get(&self, tenant_id: &str, request_id: &str) -> Result<ApprovalRequestView> {
        let row = sqlx::query(
            "SELECT * FROM governance_approval_requests WHERE tenant_id=$1 AND request_id=$2",
        )
        .bind(tenant_id)
        .bind(request_id)
        .fetch_optional(&self.pool)
        .await?
        .context("approval request does not exist")?;
        self.view(row).await
    }

    pub async fn claim_key_shreds(
        &self,
        worker_id: &str,
        batch_size: u32,
        lease_ms: u64,
    ) -> Result<Vec<ApprovalRequestView>> {
        let mut tx = self.pool.begin().await?;
        let rows = sqlx::query(
            "SELECT tenant_id,request_id FROM governance_approval_requests
             WHERE status='KEY_SHRED_PENDING' AND shred_available_at <= now()
               AND (shred_lease_until IS NULL OR shred_lease_until < now())
             ORDER BY shred_available_at,tenant_id,request_id
             LIMIT $1 FOR UPDATE SKIP LOCKED",
        )
        .bind(i64::from(batch_size))
        .fetch_all(&mut *tx)
        .await?;
        let mut identities = Vec::with_capacity(rows.len());
        for row in rows {
            let tenant_id: String = row.try_get("tenant_id")?;
            let request_id: String = row.try_get("request_id")?;
            sqlx::query(
                "UPDATE governance_approval_requests
                 SET shred_lease_until=now()+($3::bigint*interval '1 millisecond'),
                     shred_worker_id=$4,shred_attempts=shred_attempts+1
                 WHERE tenant_id=$1 AND request_id=$2",
            )
            .bind(&tenant_id)
            .bind(&request_id)
            .bind(i64_value(lease_ms)?)
            .bind(worker_id)
            .execute(&mut *tx)
            .await?;
            identities.push((tenant_id, request_id));
        }
        tx.commit().await?;
        let mut values = Vec::with_capacity(identities.len());
        for (tenant_id, request_id) in identities {
            values.push(self.get(&tenant_id, &request_id).await?);
        }
        Ok(values)
    }

    pub async fn complete_key_shred(
        &self,
        tenant_id: &str,
        request_id: &str,
        worker_id: &str,
        now: u64,
    ) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let row = sqlx::query(
            "UPDATE governance_approval_requests
             SET status='COMPLETED',aggregate_version=aggregate_version+1,
                 shred_lease_until=NULL,shred_worker_id='',last_error='',
                 updated_at=now(),updated_at_epoch_ms=$4
             WHERE tenant_id=$1 AND request_id=$2 AND status='KEY_SHRED_PENDING'
               AND shred_worker_id=$3
             RETURNING aggregate_version",
        )
        .bind(tenant_id)
        .bind(request_id)
        .bind(worker_id)
        .bind(i64_value(now)?)
        .fetch_optional(&mut *tx)
        .await?
        .context("key shred lease is no longer owned")?;
        let version: i64 = row.try_get("aggregate_version")?;
        append_audit(
            &mut tx,
            tenant_id,
            request_id,
            "KEY_SHRED_COMPLETED",
            worker_id,
            version,
            now,
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }

    pub async fn retry_key_shred(
        &self,
        tenant_id: &str,
        request_id: &str,
        worker_id: &str,
        retry_delay_ms: u64,
        error: &str,
        now: u64,
    ) -> Result<()> {
        sqlx::query(
            "UPDATE governance_approval_requests
             SET shred_lease_until=NULL,shred_worker_id='',last_error=$4,
                 shred_available_at=now()+($5::bigint*interval '1 millisecond'),
                 updated_at=now(),updated_at_epoch_ms=$6
             WHERE tenant_id=$1 AND request_id=$2 AND status='KEY_SHRED_PENDING'
               AND shred_worker_id=$3",
        )
        .bind(tenant_id)
        .bind(request_id)
        .bind(worker_id)
        .bind(error)
        .bind(i64_value(retry_delay_ms)?)
        .bind(i64_value(now)?)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    async fn find_by_idempotency(
        &self,
        tenant_id: &str,
        key: &str,
    ) -> Result<Option<ApprovalRequestView>> {
        let row = sqlx::query(
            "SELECT request_id FROM governance_approval_requests
             WHERE tenant_id=$1 AND idempotency_key=$2",
        )
        .bind(tenant_id)
        .bind(key)
        .fetch_optional(&self.pool)
        .await?;
        match row {
            Some(row) => {
                let request_id: String = row.try_get("request_id")?;
                self.get(tenant_id, &request_id).await.map(Some)
            }
            None => Ok(None),
        }
    }

    async fn view(&self, row: sqlx::postgres::PgRow) -> Result<ApprovalRequestView> {
        let tenant_id: String = row.try_get("tenant_id")?;
        let request_id: String = row.try_get("request_id")?;
        let approvals = sqlx::query(
            "SELECT * FROM governance_signed_approvals
             WHERE tenant_id=$1 AND request_id=$2 ORDER BY role,actor_id",
        )
        .bind(&tenant_id)
        .bind(&request_id)
        .fetch_all(&self.pool)
        .await?;
        let mut requesters = Vec::new();
        let mut approvers = Vec::new();
        for approval in approvals {
            let value = SignedApproval {
                request_digest: approval.try_get("request_digest")?,
                tenant_id: tenant_id.clone(),
                actor_id: approval.try_get("actor_id")?,
                capability: approval.try_get("capability")?,
                auth_assurance: approval.try_get("auth_assurance")?,
                approved_at_epoch_ms: u64_value(approval.try_get("approved_at_epoch_ms")?)?,
                expires_at_epoch_ms: u64_value(approval.try_get("expires_at_epoch_ms")?)?,
                key_id: approval.try_get("key_id")?,
                signature: approval.try_get("signature")?,
            };
            let role: String = approval.try_get("role")?;
            if role == "REQUESTER" {
                requesters.push(value);
            } else {
                approvers.push(value);
            }
        }
        let status: String = row.try_get("status")?;
        Ok(ApprovalRequestView {
            request_id,
            spec: Some(AbortAndReconcileSpec {
                tenant_id,
                instance_id: row.try_get("instance_id")?,
                workflow_type: row.try_get("workflow_type")?,
                workflow_version: row.try_get("workflow_version")?,
                policy_id: row.try_get("policy_id")?,
                legal_deadline_epoch_ms: u64_value(row.try_get("legal_deadline_epoch_ms")?)?,
                key_scope: row.try_get("key_scope")?,
                key_epoch: u64_value(row.try_get("key_epoch")?)?,
                reason_code: row.try_get("reason_code")?,
            }),
            request_digest: row.try_get("request_digest")?,
            pending_ledger_digest: row.try_get("pending_ledger_digest")?,
            expected_version: u64_value(row.try_get("expected_version")?)?,
            config_version: row.try_get("config_version")?,
            policy_version: row.try_get("policy_version")?,
            status: status_value(&status)? as i32,
            requester_approvals: requesters,
            approver_approvals: approvers,
            version: u64_value(row.try_get("aggregate_version")?)?,
            created_at_epoch_ms: u64_value(row.try_get("created_at_epoch_ms")?)?,
            updated_at_epoch_ms: u64_value(row.try_get("updated_at_epoch_ms")?)?,
            committed_command_id: row.try_get("committed_command_id")?,
            committed_sequence: u64_value(row.try_get("committed_sequence")?)?,
            last_error: row.try_get("last_error")?,
        })
    }
}

async fn append_audit(
    tx: &mut Transaction<'_, Postgres>,
    tenant_id: &str,
    request_id: &str,
    action: &str,
    actor_id: &str,
    version: i64,
    now: u64,
) -> Result<()> {
    sqlx::query(
        "INSERT INTO governance_service_audit(
            tenant_id,request_id,action,actor_id,aggregate_version,occurred_at_epoch_ms,details
         ) VALUES($1,$2,$3,$4,$5,$6,$7)",
    )
    .bind(tenant_id)
    .bind(request_id)
    .bind(action)
    .bind(actor_id)
    .bind(version)
    .bind(i64_value(now)?)
    .bind(json!({}))
    .execute(&mut **tx)
    .await?;
    Ok(())
}

fn status_value(value: &str) -> Result<ApprovalRequestStatus> {
    Ok(match value {
        "PENDING" => ApprovalRequestStatus::Pending,
        "APPROVED" => ApprovalRequestStatus::Approved,
        "COMMITTED" => ApprovalRequestStatus::Committed,
        "KEY_SHRED_PENDING" => ApprovalRequestStatus::KeyShredPending,
        "COMPLETED" => ApprovalRequestStatus::Completed,
        "REJECTED" => ApprovalRequestStatus::Rejected,
        _ => anyhow::bail!("stored approval status is invalid"),
    })
}

fn i64_value(value: u64) -> Result<i64> {
    i64::try_from(value).context("unsigned value exceeds PostgreSQL bigint")
}

fn u64_value(value: i64) -> Result<u64> {
    u64::try_from(value).context("stored PostgreSQL bigint is negative")
}
