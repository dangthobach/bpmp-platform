package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

func (s *Store) ClaimPublicationBatch(
	ctx context.Context,
	workerID string,
	batchSize int,
	leaseDuration time.Duration,
	now time.Time,
) ([]domain.Publication, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var checkpoint int64
	var leaseOwner *string
	var leaseUntil *time.Time
	err = tx.QueryRow(ctx, `SELECT checkpoint,lease_owner,lease_until
		FROM configuration_outbox_publish_state WHERE singleton_id=1 FOR UPDATE`).
		Scan(&checkpoint, &leaseOwner, &leaseUntil)
	if err != nil {
		return nil, err
	}
	if publicationLeaseIsActive(leaseOwner, leaseUntil, now) {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT
		o.event_id::text,o.event_sequence,o.request_id,o.correlation_id,o.command_id,
		COALESCE(o.trace_parent,''),COALESCE(o.trace_state,''),o.tenant_id,o.profile_id::text,o.version_id::text,
		v.config_version,v.policy_version,v.ordinal,p.owner,p.scope_type,p.scope_reference,
		v.content_hash,o.event_type,o.occurred_at,o.attempt_count
		FROM configuration_outbox o
		JOIN configuration_profiles p ON p.id=o.profile_id AND p.tenant_id=o.tenant_id
		JOIN configuration_versions v ON v.id=o.version_id AND v.profile_id=p.id
		WHERE o.event_sequence>$1 AND o.published_at IS NULL
		ORDER BY o.event_sequence
		LIMIT $2`, checkpoint, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]domain.Publication, 0, batchSize)
	for rows.Next() {
		var record domain.Publication
		var hash []byte
		if err = rows.Scan(
			&record.EventID, &record.EventSequence, &record.RequestID, &record.CorrelationID,
			&record.CommandID, &record.TraceParent, &record.TraceState, &record.TenantID, &record.ProfileID,
			&record.VersionID, &record.ConfigVersion, &record.PolicyVersion, &record.Ordinal,
			&record.Owner, &record.Scope.Type, &record.Scope.Reference, &hash, &record.Kind,
			&record.OccurredAt, &record.AttemptCount,
		); err != nil {
			return nil, err
		}
		copy(record.ContentHash[:], hash)
		records = append(records, record)
	}
	if err = rows.Err(); err != nil || len(records) == 0 {
		return records, err
	}
	if records[0].EventSequence != uint64(checkpoint)+1 {
		return nil, errors.New("configuration outbox sequence is not contiguous")
	}
	if records[0].OccurredAt.After(now) {
		return nil, nil
	}
	leaseDeadline := now.Add(leaseDuration)
	first, last := records[0].EventSequence, records[len(records)-1].EventSequence
	tag, err := tx.Exec(ctx, `UPDATE configuration_outbox
		SET lease_owner=$1,lease_until=$2,attempt_count=attempt_count+1
		WHERE event_sequence BETWEEN $3 AND $4 AND published_at IS NULL
		  AND next_attempt_at<=$5`, workerID, leaseDeadline, first, last, now)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != int64(len(records)) {
		return nil, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE configuration_outbox_publish_state
		SET lease_owner=$1,lease_until=$2,updated_at=$3 WHERE singleton_id=1`,
		workerID, leaseDeadline, now); err != nil {
		return nil, err
	}
	for index := range records {
		records[index].AttemptCount++
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return records, nil
}

func publicationLeaseIsActive(owner *string, until *time.Time, now time.Time) bool {
	return owner != nil && until != nil && until.After(now)
}

func (s *Store) CompletePublicationBatch(
	ctx context.Context,
	workerID string,
	records []domain.Publication,
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
		FROM configuration_outbox_publish_state WHERE singleton_id=1 FOR UPDATE`).
		Scan(&checkpoint, &leaseOwner)
	if err != nil {
		return err
	}
	if leaseOwner == nil || *leaseOwner != workerID ||
		records[0].EventSequence != uint64(checkpoint)+1 {
		return errors.New("configuration outbox lease was lost")
	}
	last := records[len(records)-1].EventSequence
	tag, err := tx.Exec(ctx, `UPDATE configuration_outbox
		SET published_at=$1,lease_owner=NULL,lease_until=NULL
		WHERE event_sequence BETWEEN $2 AND $3 AND lease_owner=$4 AND published_at IS NULL`,
		now, records[0].EventSequence, last, workerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(records)) {
		return errors.New("configuration outbox batch changed while publishing")
	}
	if _, err = tx.Exec(ctx, `UPDATE configuration_outbox_publish_state
		SET checkpoint=$1,lease_owner=NULL,lease_until=NULL,updated_at=$2
		WHERE singleton_id=1`, last, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailPublicationBatch(
	ctx context.Context,
	workerID string,
	records []domain.Publication,
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
	if _, err = tx.Exec(ctx, `UPDATE configuration_outbox
		SET next_attempt_at=$1,lease_owner=NULL,lease_until=NULL,last_error=$2
		WHERE event_sequence BETWEEN $3 AND $4 AND lease_owner=$5 AND published_at IS NULL`,
		nextAttemptAt, lastError, records[0].EventSequence, last, workerID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE configuration_outbox_publish_state
		SET lease_owner=NULL,lease_until=NULL,updated_at=$1
		WHERE singleton_id=1 AND lease_owner=$2`, time.Now().UTC(), workerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
