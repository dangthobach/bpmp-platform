package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimStart(ctx context.Context, now time.Time, lease time.Duration, token uuid.UUID) (application.StartJob, bool, error) {
	var job application.StartJob
	if lease <= 0 || token == uuid.Nil {
		return job, false, application.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return job, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `SELECT tenant_id,case_id,command_id,actor_id,correlation_id,workflow_type,workflow_version,attempts
		FROM workflow_start_jobs WHERE status IN ('PENDING','RECONCILING') AND next_attempt_at <= $1
		AND (lease_until IS NULL OR lease_until <= $1)
		ORDER BY next_attempt_at,case_id FOR UPDATE SKIP LOCKED LIMIT 1`, now).Scan(
		&job.TenantID, &job.CaseID, &job.CommandID, &job.ActorID, &job.CorrelationID,
		&job.WorkflowType, &job.WorkflowVersion, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	job.Attempts++
	job.LeaseToken = token
	_, err = tx.Exec(ctx, `UPDATE workflow_start_jobs SET attempts=$3,lease_token=$4,lease_until=$5,updated_at=$6
		WHERE tenant_id=$1 AND case_id=$2`, job.TenantID, job.CaseID, job.Attempts, token, now.Add(lease), now)
	if err != nil {
		return job, false, err
	}
	return job, true, tx.Commit(ctx)
}

func (s *Store) CompleteStart(ctx context.Context, job application.StartJob, now time.Time, eventID uuid.UUID) error {
	if eventID == uuid.Nil || job.LeaseToken == uuid.Nil {
		return application.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE workflow_start_jobs SET status='STARTED',lease_token=NULL,lease_until=NULL,updated_at=$4,last_error=NULL
		WHERE tenant_id=$1 AND case_id=$2 AND lease_token=$3 AND lease_until>$4 AND status IN ('PENDING','RECONCILING')`, job.TenantID, job.CaseID, job.LeaseToken, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return application.ErrLeaseLost
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE customer_cases SET orchestration_status='ACTIVE',workflow_instance_id=$3,
		row_version=row_version+1,updated_at=$4,updated_by='case-service:start-worker'
		WHERE tenant_id=$1 AND id=$2 AND orchestration_status IN ('START_PENDING','RECONCILING') AND NOT is_deleted
		RETURNING row_version`, job.TenantID, job.CaseID, job.CaseID.String(), now).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrConflict
	}
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"workflow_instance_id": job.CaseID.String(), "orchestration_status": "ACTIVE"})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO case_outbox
		(tenant_id,event_id,aggregate_id,aggregate_version,event_type,schema_version,correlation_id,causation_id,occurred_at,producer,payload)
		VALUES($1,$2,$3,$4,'CaseWorkflowStarted',1,$5,$6,$7,'case-service',$8)`,
		job.TenantID, eventID, job.CaseID, version, job.CorrelationID, job.CommandID.String(), now, payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RetryStart(ctx context.Context, job application.StartJob, now, next time.Time, code string) error {
	if job.LeaseToken == uuid.Nil || code == "" || len(code) > 80 {
		return application.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	status := "RECONCILING"
	if code == "START_INPUT_UNAVAILABLE" {
		status = "PENDING"
	}
	if job.Attempts >= 8 {
		status = "FAILED_MANUAL"
	}
	tag, err := tx.Exec(ctx, `UPDATE workflow_start_jobs SET status=$4,next_attempt_at=$5,lease_token=NULL,
		lease_until=NULL,last_error=$6,updated_at=$7
		WHERE tenant_id=$1 AND case_id=$2 AND lease_token=$3 AND lease_until>$7 AND status IN ('PENDING','RECONCILING')`,
		job.TenantID, job.CaseID, job.LeaseToken, status, next, code, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return application.ErrLeaseLost
	}
	var version int64
	caseStatus := "RECONCILING"
	eventType := "CaseWorkflowReconciling"
	if status == "FAILED_MANUAL" {
		caseStatus = "START_FAILED"
		eventType = "CaseWorkflowStartFailed"
	} else if status == "PENDING" {
		return tx.Commit(ctx)
	}
	err = tx.QueryRow(ctx, `UPDATE customer_cases SET orchestration_status=$4,row_version=row_version+1,
		updated_at=$3,updated_by='case-service:start-worker'
		WHERE tenant_id=$1 AND id=$2 AND orchestration_status IN ('START_PENDING','RECONCILING')
		AND orchestration_status<>$4 AND NOT is_deleted
		RETURNING row_version`, job.TenantID, job.CaseID, now, caseStatus).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		payload, marshalErr := json.Marshal(map[string]string{"orchestration_status": caseStatus})
		if marshalErr != nil {
			return marshalErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO case_outbox
			(tenant_id,event_id,aggregate_id,aggregate_version,event_type,schema_version,correlation_id,causation_id,occurred_at,producer,payload)
			VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,'case-service',$9)`,
			job.TenantID, uuid.New(), job.CaseID, version, eventType, job.CorrelationID, job.CommandID.String(), now, payload)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
