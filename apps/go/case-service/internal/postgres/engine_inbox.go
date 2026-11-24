package postgres

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrEngineEventConflict = errors.New("Engine event identity or sequence conflicts with stored Case projection")

func (s *Store) ApplyEngineEvent(ctx context.Context, event application.EngineEvent) (bool, error) {
	caseID, err := uuid.Parse(event.InstanceID)
	if err != nil {
		// The committed-event topic is shared by non-Case workflows whose instance
		// identifiers are not Case UUIDs. They are outside this projection.
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var workflowType, workflowVersion string
	err = tx.QueryRow(ctx, `SELECT workflow_type,workflow_version FROM customer_cases
		WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, event.TenantID, caseID).
		Scan(&workflowType, &workflowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if workflowType != event.WorkflowType || workflowVersion != event.WorkflowVersion {
		return false, ErrEngineEventConflict
	}

	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO case_inbox(tenant_id,source,event_id,payload_digest,applied_at)
		VALUES($1,'bpmp-engine',$2,$3,now()) ON CONFLICT DO NOTHING RETURNING event_id`,
		event.TenantID, event.EventID, event.PayloadDigest[:]).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var priorDigest []byte
		if err = tx.QueryRow(ctx, `SELECT payload_digest FROM case_inbox
			WHERE tenant_id=$1 AND source='bpmp-engine' AND event_id=$2`, event.TenantID, event.EventID).Scan(&priorDigest); err != nil {
			return false, err
		}
		if len(priorDigest) != len(event.PayloadDigest) || subtle.ConstantTimeCompare(priorDigest, event.PayloadDigest[:]) != 1 {
			return false, ErrEngineEventConflict
		}
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO case_engine_events
		(tenant_id,case_id,event_id,engine_sequence,event_type,node_id,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, event.TenantID, caseID, event.EventID,
		event.Sequence, event.EventType, event.NodeID, event.OccurredAt)
	if err != nil {
		return false, errors.Join(ErrEngineEventConflict, err)
	}
	status := ""
	switch event.EventType {
	case "workflow_started":
		status = "ACTIVE"
	case "workflow_completed", "workflow_terminated_for_compliance":
		status = "CLOSED"
	}
	_, err = tx.Exec(ctx, `UPDATE customer_cases SET engine_sequence=$3,
		orchestration_status=CASE WHEN orchestration_status='CLOSED' THEN 'CLOSED'
			WHEN $4='' THEN orchestration_status ELSE $4 END,
		updated_at=GREATEST(updated_at,$5),updated_by='case-service:engine-projection'
		WHERE tenant_id=$1 AND id=$2 AND engine_sequence<$3 AND NOT is_deleted`,
		event.TenantID, caseID, event.Sequence, status, event.OccurredAt)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

var _ application.EngineEventRepository = (*Store)(nil)
