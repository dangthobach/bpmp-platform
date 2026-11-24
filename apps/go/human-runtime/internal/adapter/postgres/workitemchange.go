package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimWorkItemChanges(ctx context.Context, now, leaseUntil time.Time, workerID string, limit int) ([]application.WorkItemChange, error) {
	if workerID == "" || limit <= 0 || limit > 1000 || !leaseUntil.After(now) {
		return nil, errors.New("work-item change lease claim is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `WITH due AS (
		SELECT tenant_id,event_id FROM human_work_item_change_outbox
		WHERE published_at IS NULL AND NOT is_deleted AND available_at<=$1
		AND (lease_expires_at IS NULL OR lease_expires_at<=$1)
		ORDER BY available_at,tenant_id,event_id FOR UPDATE SKIP LOCKED LIMIT $2)
		UPDATE human_work_item_change_outbox e SET lease_owner=$3,lease_expires_at=$4,row_version=e.row_version+1
		FROM due WHERE e.tenant_id=due.tenant_id AND e.event_id=due.event_id
		RETURNING e.tenant_id,e.event_id,e.work_item_id,e.payload,e.attempts`, now, limit, workerID, leaseUntil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := make([]application.WorkItemChange, 0, limit)
	for rows.Next() {
		var claim application.WorkItemChange
		if err = rows.Scan(&claim.TenantID, &claim.EventID, &claim.WorkItemID, &claim.Payload, &claim.Attempts); err != nil {
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

func (s *Store) AckWorkItemChange(ctx context.Context, claim application.WorkItemChange, workerID string, at time.Time) error {
	result, err := s.pool.Exec(ctx, `UPDATE human_work_item_change_outbox SET published_at=$4,lease_owner=NULL,lease_expires_at=NULL,row_version=row_version+1
		WHERE tenant_id=$1 AND event_id=$2 AND lease_owner=$3 AND published_at IS NULL`, claim.TenantID, claim.EventID, workerID, at)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrVersionConflict
	}
	return nil
}

func (s *Store) RetryWorkItemChange(ctx context.Context, claim application.WorkItemChange, workerID string, next time.Time) error {
	result, err := s.pool.Exec(ctx, `UPDATE human_work_item_change_outbox SET attempts=attempts+1,available_at=$4,lease_owner=NULL,lease_expires_at=NULL,row_version=row_version+1
		WHERE tenant_id=$1 AND event_id=$2 AND lease_owner=$3 AND published_at IS NULL`, claim.TenantID, claim.EventID, workerID, next)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrVersionConflict
	}
	return nil
}

var _ application.WorkItemChangeStore = (*Store)(nil)
