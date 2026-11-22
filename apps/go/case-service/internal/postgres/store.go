package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("Case PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Create(ctx context.Context, command application.CreateCommand) (result application.CreateResult, err error) {
	request := command.Request
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serializes only this tenant, actor and key. A hash collision merely adds contention.
	lockKey := request.TenantID.String() + ":" + request.ActorID + ":" + request.IdempotencyKey
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return result, err
	}
	var priorID uuid.UUID
	var priorDigest []byte
	err = tx.QueryRow(ctx, `SELECT case_id, request_digest FROM case_create_receipts
		WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, request.TenantID, request.ActorID, request.IdempotencyKey).Scan(&priorID, &priorDigest)
	if err == nil {
		if subtle.ConstantTimeCompare(priorDigest, command.Digest[:]) != 1 {
			return result, application.ErrConflict
		}
		result.Case, err = get(ctx, tx, request.TenantID, priorID)
		if err != nil {
			return result, err
		}
		result.Duplicate = true
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_cases
		(tenant_id,id,customer_ref,workflow_type,workflow_version,business_status,orchestration_status,row_version,created_at,created_by,updated_at,updated_by)
		VALUES($1,$2,$3,$4,$5,'SUBMITTED','START_PENDING',1,$6,$7,$6,$7)`,
		request.TenantID, command.CaseID, request.CustomerRef, request.WorkflowType, request.WorkflowVersion, command.Now, request.ActorID)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO case_create_receipts
		(tenant_id,actor_id,idempotency_key,request_digest,case_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		request.TenantID, request.ActorID, request.IdempotencyKey, command.Digest[:], command.CaseID, command.Now)
	if err != nil {
		return result, err
	}
	payload, err := json.Marshal(struct {
		CustomerRef     string `json:"customer_ref"`
		WorkflowType    string `json:"workflow_type"`
		WorkflowVersion string `json:"workflow_version"`
		Status          string `json:"orchestration_status"`
	}{request.CustomerRef, request.WorkflowType, request.WorkflowVersion, "START_PENDING"})
	if err != nil {
		return result, fmt.Errorf("encode CaseCreated payload: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO case_outbox
		(tenant_id,event_id,aggregate_id,aggregate_version,event_type,schema_version,correlation_id,causation_id,occurred_at,producer,payload)
		VALUES($1,$2,$3,1,'CaseCreated',1,$4,$5,$6,'case-service',$7)`,
		request.TenantID, command.EventID, command.CaseID, request.CorrelationID, request.IdempotencyKey, command.Now, payload)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workflow_start_jobs
		(tenant_id,case_id,command_id,actor_id,correlation_id,workflow_type,workflow_version,next_attempt_at,created_at,updated_at)
		VALUES($1,$2,$2,$3,$4,$5,$6,$7,$7,$7)`, request.TenantID, command.CaseID, request.ActorID,
		request.CorrelationID, request.WorkflowType, request.WorkflowVersion, command.Now)
	if err != nil {
		return result, err
	}
	result.Case = application.Case{TenantID: request.TenantID, ID: command.CaseID, CustomerRef: request.CustomerRef,
		WorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion, BusinessStatus: "SUBMITTED", OrchestrationStatus: "START_PENDING",
		Version: 1, CreatedAt: command.Now, UpdatedAt: command.Now}
	return result, tx.Commit(ctx)
}

func (s *Store) Get(ctx context.Context, tenantID, caseID uuid.UUID) (application.Case, error) {
	return get(ctx, s.pool, tenantID, caseID)
}

type queryRow interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func get(ctx context.Context, q queryRow, tenantID, caseID uuid.UUID) (application.Case, error) {
	var value application.Case
	err := q.QueryRow(ctx, `SELECT tenant_id,id,customer_ref,workflow_type,workflow_version,business_status,orchestration_status,
		COALESCE(workflow_instance_id,''),row_version,created_at,updated_at FROM customer_cases
		WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted`, tenantID, caseID).Scan(
		&value.TenantID, &value.ID, &value.CustomerRef, &value.WorkflowType, &value.WorkflowVersion, &value.BusinessStatus,
		&value.OrchestrationStatus, &value.WorkflowInstanceID, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, application.ErrNotFound
	}
	return value, err
}
