package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type TenantLifecycle struct {
	EventID       string
	TenantID      string
	TenantVersion int64
	EventSequence int64
	Deleted       bool
	RequestID     string
	CorrelationID string
	CommandID     string
	TraceParent   string
	TraceState    string
}

func (s *Store) ApplyTenantLifecycle(
	ctx context.Context,
	event TenantLifecycle,
	now time.Time,
) (bool, error) {
	if strings.TrimSpace(event.TenantID) == "" ||
		event.TenantVersion < 0 ||
		event.EventSequence <= 0 ||
		now.IsZero() {
		return false, errors.New("tenant lifecycle event is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var currentSequence int64
	err = tx.QueryRow(ctx, `SELECT lifecycle_event_sequence
		FROM tenant_configuration_readiness WHERE tenant_id=$1 FOR UPDATE`,
		event.TenantID).Scan(&currentSequence)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err == nil && currentSequence >= event.EventSequence {
		return false, tx.Commit(ctx)
	}
	ready, missing, hash, err := calculateTenantReadiness(ctx, tx, event.TenantID)
	if err != nil {
		return false, err
	}
	if event.Deleted {
		ready = false
		missing, err = requiredOwners(ctx, tx)
		if err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO tenant_configuration_readiness
		(tenant_id,tenant_version,lifecycle_event_sequence,ready,missing_owners,
		 profile_set_hash,version,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,1,$7)
		ON CONFLICT (tenant_id) DO UPDATE SET
		 tenant_version=EXCLUDED.tenant_version,
		 lifecycle_event_sequence=EXCLUDED.lifecycle_event_sequence,
		 ready=EXCLUDED.ready,
		 missing_owners=EXCLUDED.missing_owners,
		 profile_set_hash=EXCLUDED.profile_set_hash,
		 version=tenant_configuration_readiness.version+1,
		 updated_at=EXCLUDED.updated_at`,
		event.TenantID, event.TenantVersion, event.EventSequence, ready, missing, hash[:], now)
	if err != nil {
		return false, err
	}
	requestID := event.RequestID
	if requestID == "" {
		requestID = event.EventID
	}
	if requestID == "" {
		requestID = fmt.Sprintf("tenant-lifecycle:%d", event.EventSequence)
	}
	if err = insertTenantReadinessOutbox(
		ctx, tx, requestID, event.CorrelationID, event.CommandID, event.TraceParent, event.TraceState,
		event.TenantID, event.TenantVersion, ready, missing, hash, now,
	); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func refreshTenantReadiness(
	ctx context.Context,
	tx pgx.Tx,
	actor domain.Actor,
	now time.Time,
) error {
	tenantID := actor.TenantID
	var tenantVersion int64
	var currentReady bool
	var currentMissing []string
	var currentHash []byte
	err := tx.QueryRow(ctx, `SELECT tenant_version,ready,missing_owners,profile_set_hash
		FROM tenant_configuration_readiness WHERE tenant_id=$1 FOR UPDATE`,
		tenantID).Scan(&tenantVersion, &currentReady, &currentMissing, &currentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	ready, missing, hash, err := calculateTenantReadiness(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	if currentReady == ready &&
		strings.Join(currentMissing, "\x00") == strings.Join(missing, "\x00") &&
		bytes.Equal(currentHash, hash[:]) {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE tenant_configuration_readiness SET
		ready=$1,missing_owners=$2,profile_set_hash=$3,version=version+1,updated_at=$4
		WHERE tenant_id=$5`,
		ready, missing, hash[:], now, tenantID)
	if err != nil {
		return err
	}
	return insertTenantReadinessOutbox(
		ctx, tx, actor.RequestID, actor.CorrelationID, actor.CommandID, actor.TraceParent, actor.TraceState,
		tenantID, tenantVersion, ready, missing, hash, now,
	)
}

func calculateTenantReadiness(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
) (bool, []string, [32]byte, error) {
	rows, err := tx.Query(ctx, `SELECT requirement.owner,
		 COALESCE(array_agg(scope.version_id::text ORDER BY
		  scope.scope_type,scope.scope_reference,scope.version_id)
		  FILTER (WHERE scope.version_id IS NOT NULL),'{}'::text[]) AS version_ids
		FROM configuration_activation_requirements requirement
		LEFT JOIN configuration_active_scopes scope
		  ON scope.tenant_id=$1 AND scope.owner=requirement.owner
		WHERE requirement.is_required
		GROUP BY requirement.owner
		ORDER BY requirement.owner`, tenantID)
	if err != nil {
		return false, nil, [32]byte{}, err
	}
	defer rows.Close()
	missing := make([]string, 0)
	hasher := sha256.New()
	for rows.Next() {
		var owner string
		var versionIDs []string
		if err = rows.Scan(&owner, &versionIDs); err != nil {
			return false, nil, [32]byte{}, err
		}
		_, _ = hasher.Write([]byte(owner))
		_, _ = hasher.Write([]byte{0})
		if len(versionIDs) == 0 {
			missing = append(missing, owner)
		}
		for _, versionID := range versionIDs {
			_, _ = hasher.Write([]byte(versionID))
			_, _ = hasher.Write([]byte{0})
		}
	}
	if err = rows.Err(); err != nil {
		return false, nil, [32]byte{}, err
	}
	var hash [32]byte
	copy(hash[:], hasher.Sum(nil))
	return len(missing) == 0, missing, hash, nil
}

func requiredOwners(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT owner FROM configuration_activation_requirements
		WHERE is_required ORDER BY owner`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := make([]string, 0)
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	return owners, rows.Err()
}

func insertTenantReadinessOutbox(
	ctx context.Context,
	tx pgx.Tx,
	requestID string,
	correlationID string,
	commandID string,
	traceParent string,
	traceState string,
	tenantID string,
	tenantVersion int64,
	ready bool,
	missing []string,
	hash [32]byte,
	now time.Time,
) error {
	if requestID == "" {
		requestID = commandID
	}
	if correlationID == "" {
		correlationID = requestID
	}
	if commandID == "" {
		commandID = requestID
	}
	_, err := tx.Exec(ctx, `INSERT INTO tenant_configuration_readiness_outbox
		(event_id,request_id,correlation_id,command_id,trace_parent,trace_state,tenant_id,
		 tenant_version,ready,missing_owners,profile_set_hash,occurred_at,next_attempt_at)
		VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,$11,$12,$12)`,
		uuid.NewString(), requestID, correlationID, commandID, traceParent, traceState,
		tenantID, tenantVersion, ready, missing, hash[:], now)
	return err
}
