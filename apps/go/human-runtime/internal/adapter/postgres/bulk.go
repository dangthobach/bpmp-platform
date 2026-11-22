package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/jackc/pgx/v5"
)

func (s *Store) BeginBulkOperation(ctx context.Context, operation application.BulkOperation, items []application.BulkItemReceipt) (string, []application.BulkItemReceipt, bool, error) {
	if operation.TenantID == "" || operation.ActorID == "" || operation.OperationID == "" || operation.IdempotencyKey == "" || len(operation.RequestDigest) != 32 || len(items) == 0 {
		return "", nil, false, application.ErrBulkRequest
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockNames := []string{"bulk-idempotency:" + operation.ActorID + ":" + operation.IdempotencyKey, "bulk-operation:" + operation.OperationID}
	for _, lockName := range lockNames {
		lockDigest := sha256.Sum256([]byte(operation.TenantID + "\x00" + lockName))
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(lockDigest[:8]))); err != nil {
			return "", nil, false, err
		}
	}
	var storedDigest []byte
	var existingOperationID string
	err = tx.QueryRow(ctx, `SELECT operation_id,request_digest FROM human_bulk_operations WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, operation.TenantID, operation.ActorID, operation.IdempotencyKey).Scan(&existingOperationID, &storedDigest)
	if err == nil {
		if subtle.ConstantTimeCompare(storedDigest, operation.RequestDigest) != 1 {
			return "", nil, false, application.ErrIdempotencyConflict
		}
		receipts, loadErr := loadBulkReceipts(ctx, tx, operation.TenantID, existingOperationID)
		if loadErr != nil {
			return "", nil, false, loadErr
		}
		if err = tx.Commit(ctx); err != nil {
			return "", nil, false, err
		}
		return existingOperationID, receipts, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO human_bulk_operations(tenant_id,actor_id,operation_id,idempotency_key,action,mode,correlation_id,request_digest,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
		operation.TenantID, operation.ActorID, operation.OperationID, operation.IdempotencyKey,
		operation.Action, operation.Mode, operation.CorrelationID, operation.RequestDigest, operation.OccurredAt)
	if err != nil {
		return "", nil, false, err
	}
	for _, item := range items {
		if _, err = tx.Exec(ctx, `INSERT INTO human_bulk_item_receipts(tenant_id,operation_id,work_item_id,command_id,status,updated_at) VALUES($1,$2,$3,$4,'PENDING',$5)`, operation.TenantID, operation.OperationID, item.WorkItemID, item.CommandID, operation.OccurredAt); err != nil {
			return "", nil, false, err
		}
	}
	if err = appendAudit(ctx, tx, operation.TenantID, "bulk:"+operation.OperationID, "", "", operation.ActorID, "BULK_ACCEPTED", operation.OccurredAt, operation.OperationID, operation.CorrelationID, 0, 0, map[string]any{"action": operation.Action, "mode": operation.Mode, "item_count": len(items)}); err != nil {
		return "", nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", nil, false, err
	}
	return operation.OperationID, items, false, nil
}

func loadBulkReceipts(ctx context.Context, tx pgx.Tx, tenantID, operationID string) ([]application.BulkItemReceipt, error) {
	rows, err := tx.Query(ctx, `SELECT work_item_id,command_id,status,error_code,retryable,resulting_version FROM human_bulk_item_receipts WHERE tenant_id=$1 AND operation_id=$2 ORDER BY work_item_id`, tenantID, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var receipts []application.BulkItemReceipt
	for rows.Next() {
		var receipt application.BulkItemReceipt
		if err = rows.Scan(&receipt.WorkItemID, &receipt.CommandID, &receipt.Status, &receipt.ErrorCode, &receipt.Retryable, &receipt.ResultingVersion); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

func (s *Store) SaveBulkItemReceipt(ctx context.Context, tenantID, operationID, workItemID string, receipt application.BulkItemReceipt, at time.Time) error {
	if receipt.Status != "PENDING" && receipt.Status != "SUCCEEDED" && receipt.Status != "FAILED" {
		return application.ErrBulkRequest
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actorID, operationAction, correlationID string
	if err = tx.QueryRow(ctx, `SELECT actor_id,action,correlation_id FROM human_bulk_operations WHERE tenant_id=$1 AND operation_id=$2`, tenantID, operationID).Scan(&actorID, &operationAction, &correlationID); err != nil {
		return err
	}
	var attempt int
	err = tx.QueryRow(ctx, `UPDATE human_bulk_item_receipts SET status=$1,error_code=$2,retryable=$3,resulting_version=$4,updated_at=$5,attempt_count=attempt_count+1 WHERE tenant_id=$6 AND operation_id=$7 AND work_item_id=$8 AND command_id=$9 RETURNING attempt_count`,
		receipt.Status, receipt.ErrorCode, receipt.Retryable, receipt.ResultingVersion, at, tenantID, operationID, workItemID, receipt.CommandID).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrBulkRequest
	}
	if err != nil {
		return err
	}
	if err = appendAudit(ctx, tx, tenantID, fmt.Sprintf("bulk-item:%s:%s:%d", operationID, workItemID, attempt), workItemID, "", actorID, "BULK_ITEM_"+receipt.Status, at, receipt.CommandID, correlationID, 0, receipt.ResultingVersion, map[string]any{"operation_id": operationID, "action": operationAction, "error_code": receipt.ErrorCode, "retryable": receipt.Retryable}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE human_bulk_operations SET updated_at=$1 WHERE tenant_id=$2 AND operation_id=$3`, at, tenantID, operationID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FindBulkCommandAudit(ctx context.Context, tenantID, commandID string) (string, int64, bool, error) {
	var action string
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT action,COALESCE(to_version,0) FROM human_audit_log WHERE tenant_id=$1 AND command_id=$2 AND action IN ('DELEGATED','COMPLETED') ORDER BY occurred_at DESC,audit_id DESC LIMIT 1`, tenantID, commandID).Scan(&action, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return action, version, true, nil
}

var _ application.BulkReceiptStore = (*Store)(nil)
