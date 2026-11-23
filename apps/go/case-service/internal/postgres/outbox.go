package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimEvent(ctx context.Context, now time.Time, lease time.Duration, token uuid.UUID) (application.OutboxEvent, bool, error) {
	var event application.OutboxEvent
	if lease <= 0 || token == uuid.Nil {
		return event, false, application.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return event, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `SELECT o.tenant_id,o.event_id,o.aggregate_id,o.aggregate_version,o.event_type,o.schema_version,
		o.correlation_id,o.causation_id,o.occurred_at,o.producer,o.payload,o.attempts
		FROM case_outbox o WHERE o.published_at IS NULL AND o.next_attempt_at <= $1
		AND (o.lease_until IS NULL OR o.lease_until <= $1)
		AND NOT EXISTS (SELECT 1 FROM case_outbox prior WHERE prior.tenant_id=o.tenant_id
			AND prior.aggregate_id=o.aggregate_id AND prior.aggregate_version<o.aggregate_version
			AND prior.published_at IS NULL)
		ORDER BY o.next_attempt_at,o.occurred_at,o.event_id FOR UPDATE OF o SKIP LOCKED LIMIT 1`, now).Scan(
		&event.TenantID, &event.EventID, &event.AggregateID, &event.AggregateVersion,
		&event.EventType, &event.SchemaVersion, &event.CorrelationID, &event.CausationID,
		&event.OccurredAt, &event.Producer, &event.Payload, &event.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return event, false, nil
	}
	if err != nil {
		return event, false, err
	}
	event.Attempts++
	event.LeaseToken = token
	_, err = tx.Exec(ctx, `UPDATE case_outbox SET attempts=$3,lease_token=$4,lease_until=$5
		WHERE tenant_id=$1 AND event_id=$2`, event.TenantID, event.EventID, event.Attempts, token, now.Add(lease))
	if err != nil {
		return event, false, err
	}
	return event, true, tx.Commit(ctx)
}

func (s *Store) MarkPublished(ctx context.Context, event application.OutboxEvent, now time.Time) error {
	if event.LeaseToken == uuid.Nil {
		return application.ErrInvalid
	}
	tag, err := s.pool.Exec(ctx, `UPDATE case_outbox SET published_at=$4,lease_token=NULL,lease_until=NULL
		WHERE tenant_id=$1 AND event_id=$2 AND lease_token=$3 AND lease_until>$4 AND published_at IS NULL`,
		event.TenantID, event.EventID, event.LeaseToken, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return application.ErrLeaseLost
	}
	return nil
}

func (s *Store) RetryEvent(ctx context.Context, event application.OutboxEvent, now, next time.Time) error {
	if event.LeaseToken == uuid.Nil || !next.After(now) {
		return application.ErrInvalid
	}
	tag, err := s.pool.Exec(ctx, `UPDATE case_outbox SET next_attempt_at=$4,lease_token=NULL,lease_until=NULL
		WHERE tenant_id=$1 AND event_id=$2 AND lease_token=$3 AND lease_until>$5 AND published_at IS NULL`,
		event.TenantID, event.EventID, event.LeaseToken, next, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return application.ErrLeaseLost
	}
	return nil
}
