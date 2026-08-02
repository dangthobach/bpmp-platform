package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

func (s *Store) ClaimTenantReadinessBatch(
	ctx context.Context,
	workerID string,
	batchSize int,
	leaseDuration time.Duration,
	now time.Time,
) ([]domain.TenantReadinessPublication, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var checkpoint int64
	var leaseOwner *string
	var leaseUntil *time.Time
	err = tx.QueryRow(ctx, `SELECT checkpoint,lease_owner,lease_until
		FROM tenant_configuration_readiness_publish_state
		WHERE singleton_id=1 FOR UPDATE`).
		Scan(&checkpoint, &leaseOwner, &leaseUntil)
	if err != nil {
		return nil, err
	}
	if publicationLeaseIsActive(leaseOwner, leaseUntil, now) {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT event_id::text,event_sequence,request_id,
		correlation_id,command_id,COALESCE(trace_parent,''),COALESCE(trace_state,''),tenant_id,
		tenant_version,ready,missing_owners,profile_set_hash,occurred_at,attempt_count
		FROM tenant_configuration_readiness_outbox
		WHERE event_sequence>$1 AND published_at IS NULL
		ORDER BY event_sequence LIMIT $2`, checkpoint, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]domain.TenantReadinessPublication, 0, batchSize)
	for rows.Next() {
		var record domain.TenantReadinessPublication
		var owners []string
		var hash []byte
		if err = rows.Scan(
			&record.EventID,
			&record.EventSequence,
			&record.RequestID,
			&record.CorrelationID,
			&record.CommandID,
			&record.TraceParent,
			&record.TraceState,
			&record.TenantID,
			&record.TenantVersion,
			&record.Ready,
			&owners,
			&hash,
			&record.OccurredAt,
			&record.AttemptCount,
		); err != nil {
			return nil, err
		}
		record.MissingOwners = make([]domain.Owner, len(owners))
		for index, owner := range owners {
			record.MissingOwners[index] = domain.Owner(owner)
			if domain.ValidateOwner(record.MissingOwners[index]) != nil {
				return nil, errors.New("readiness outbox contains an invalid owner")
			}
		}
		if len(hash) != len(record.ProfileSetHash) {
			return nil, errors.New("readiness outbox contains an invalid profile hash")
		}
		copy(record.ProfileSetHash[:], hash)
		records = append(records, record)
	}
	if err = rows.Err(); err != nil || len(records) == 0 {
		return records, err
	}
	if records[0].EventSequence != uint64(checkpoint)+1 {
		return nil, errors.New("tenant readiness outbox sequence is not contiguous")
	}
	if records[0].OccurredAt.After(now) {
		return nil, nil
	}
	deadline := now.Add(leaseDuration)
	first, last := records[0].EventSequence, records[len(records)-1].EventSequence
	tag, err := tx.Exec(ctx, `UPDATE tenant_configuration_readiness_outbox
		SET lease_owner=$1,lease_until=$2,attempt_count=attempt_count+1
		WHERE event_sequence BETWEEN $3 AND $4 AND published_at IS NULL
		  AND next_attempt_at<=$5`,
		workerID, deadline, first, last, now)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != int64(len(records)) {
		return nil, nil
	}
	_, err = tx.Exec(ctx, `UPDATE tenant_configuration_readiness_publish_state
		SET lease_owner=$1,lease_until=$2,updated_at=$3 WHERE singleton_id=1`,
		workerID, deadline, now)
	if err != nil {
		return nil, err
	}
	for index := range records {
		records[index].AttemptCount++
	}
	return records, tx.Commit(ctx)
}

func (s *Store) CompleteTenantReadinessBatch(
	ctx context.Context,
	workerID string,
	records []domain.TenantReadinessPublication,
	now time.Time,
) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var checkpoint int64
	var leaseOwner *string
	err = tx.QueryRow(ctx, `SELECT checkpoint,lease_owner
		FROM tenant_configuration_readiness_publish_state
		WHERE singleton_id=1 FOR UPDATE`).Scan(&checkpoint, &leaseOwner)
	if err != nil {
		return err
	}
	if leaseOwner == nil || *leaseOwner != workerID ||
		records[0].EventSequence != uint64(checkpoint)+1 {
		return errors.New("tenant readiness outbox lease was lost")
	}
	last := records[len(records)-1].EventSequence
	tag, err := tx.Exec(ctx, `UPDATE tenant_configuration_readiness_outbox
		SET published_at=$1,lease_owner=NULL,lease_until=NULL
		WHERE event_sequence BETWEEN $2 AND $3 AND lease_owner=$4 AND published_at IS NULL`,
		now, records[0].EventSequence, last, workerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(records)) {
		return errors.New("tenant readiness outbox batch changed while publishing")
	}
	_, err = tx.Exec(ctx, `UPDATE tenant_configuration_readiness_publish_state
		SET checkpoint=$1,lease_owner=NULL,lease_until=NULL,updated_at=$2
		WHERE singleton_id=1`, last, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailTenantReadinessBatch(
	ctx context.Context,
	workerID string,
	records []domain.TenantReadinessPublication,
	nextAttemptAt time.Time,
	lastError string,
) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	last := records[len(records)-1].EventSequence
	_, err = tx.Exec(ctx, `UPDATE tenant_configuration_readiness_outbox
		SET next_attempt_at=$1,lease_owner=NULL,lease_until=NULL,last_error=$2
		WHERE event_sequence BETWEEN $3 AND $4 AND lease_owner=$5 AND published_at IS NULL`,
		nextAttemptAt, lastError, records[0].EventSequence, last, workerID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE tenant_configuration_readiness_publish_state
		SET lease_owner=NULL,lease_until=NULL,updated_at=$1
		WHERE singleton_id=1 AND lease_owner=$2`, time.Now().UTC(), workerID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
