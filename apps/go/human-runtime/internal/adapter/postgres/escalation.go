package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
)

func (s *Store) ClaimDueEscalations(ctx context.Context, now, leaseUntil time.Time, workerID string, limit int) ([]application.Escalation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO escalation_outbox
        (tenant_id,escalation_id,work_item_id,escalation_policy_ref,payload,available_at,created_at)
        SELECT tenant_id,work_item_id||':'||escalation_policy_ref,work_item_id,escalation_policy_ref,
        jsonb_build_object('tenant_id',tenant_id,'work_item_id',work_item_id,'instance_id',instance_id,
        'node_id',node_id,'policy_ref',escalation_policy_ref,'sla_deadline',sla_deadline),$1,$1
        FROM work_items WHERE status='ACTIVE' AND NOT is_deleted AND sla_deadline<=$1
        AND escalation_policy_ref IS NOT NULL ON CONFLICT DO NOTHING`, now)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH due AS (
        SELECT tenant_id,escalation_id FROM escalation_outbox WHERE published_at IS NULL AND NOT is_deleted
        AND available_at<=$1 AND (lease_expires_at IS NULL OR lease_expires_at<=$1)
        ORDER BY available_at,tenant_id,escalation_id FOR UPDATE SKIP LOCKED LIMIT $2)
        UPDATE escalation_outbox e SET lease_owner=$3,lease_expires_at=$4,version=e.version+1
        FROM due WHERE e.tenant_id=due.tenant_id AND e.escalation_id=due.escalation_id
        RETURNING e.tenant_id,e.escalation_id,e.work_item_id,e.escalation_policy_ref,e.payload,e.attempts`, now, limit, workerID, leaseUntil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := make([]application.Escalation, 0, limit)
	for rows.Next() {
		var claim application.Escalation
		if err = rows.Scan(&claim.TenantID, &claim.EscalationID, &claim.WorkItemID, &claim.PolicyRef, &claim.Payload, &claim.Attempts); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Store) AckEscalation(ctx context.Context, claim application.Escalation, workerID string, at time.Time) error {
	result, err := s.pool.Exec(ctx, `UPDATE escalation_outbox SET published_at=$4,lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE tenant_id=$1 AND escalation_id=$2 AND lease_owner=$3 AND published_at IS NULL`, claim.TenantID, claim.EscalationID, workerID, at)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrVersionConflict
	}
	return nil
}
func (s *Store) RetryEscalation(ctx context.Context, claim application.Escalation, workerID string, next time.Time) error {
	result, err := s.pool.Exec(ctx, `UPDATE escalation_outbox SET attempts=attempts+1,available_at=$4,lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE tenant_id=$1 AND escalation_id=$2 AND lease_owner=$3 AND published_at IS NULL`, claim.TenantID, claim.EscalationID, workerID, next)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrVersionConflict
	}
	return nil
}

var _ application.EscalationStore = (*Store)(nil)
