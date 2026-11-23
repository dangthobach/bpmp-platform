package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// OutboxEvent is the stable envelope delivered at least once to the event bus.
type OutboxEvent struct {
	TenantID         uuid.UUID       `json:"tenant_id"`
	EventID          uuid.UUID       `json:"event_id"`
	AggregateID      uuid.UUID       `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	EventType        string          `json:"event_type"`
	SchemaVersion    int             `json:"schema_version"`
	CorrelationID    string          `json:"correlation_id"`
	CausationID      string          `json:"causation_id"`
	OccurredAt       time.Time       `json:"occurred_at"`
	Producer         string          `json:"producer"`
	Payload          json.RawMessage `json:"payload"`
	LeaseToken       uuid.UUID       `json:"-"`
	Attempts         int             `json:"-"`
}

type OutboxRepository interface {
	ClaimEvent(context.Context, time.Time, time.Duration, uuid.UUID) (OutboxEvent, bool, error)
	MarkPublished(context.Context, OutboxEvent, time.Time) error
	RetryEvent(context.Context, OutboxEvent, time.Time, time.Time) error
}

// EventPublisher must return only after the broker has durably acknowledged the event.
// The event ID stays constant across retries so downstream consumers can deduplicate.
type EventPublisher interface {
	Publish(context.Context, OutboxEvent) error
}

type OutboxRunner struct {
	repo      OutboxRepository
	publisher EventPublisher
	now       func() time.Time
	newID     func() uuid.UUID
}

func NewOutboxRunner(repo OutboxRepository, publisher EventPublisher, now func() time.Time, newID func() uuid.UUID) (*OutboxRunner, error) {
	if repo == nil || publisher == nil || now == nil || newID == nil {
		return nil, ErrInvalid
	}
	return &OutboxRunner{repo: repo, publisher: publisher, now: now, newID: newID}, nil
}

func (r *OutboxRunner) RunOnce(ctx context.Context) (bool, error) {
	now, token := r.now().UTC(), r.newID()
	if token == uuid.Nil {
		return false, ErrInvalid
	}
	event, found, err := r.repo.ClaimEvent(ctx, now, 30*time.Second, token)
	if err != nil || !found {
		return found, err
	}
	if event.EventID == uuid.Nil || event.LeaseToken != token || event.Attempts < 1 {
		return true, ErrInvalid
	}
	if err := r.publisher.Publish(ctx, event); err != nil {
		if ctx.Err() != nil {
			return true, errors.Join(err, ctx.Err())
		}
		retryAt := r.now().UTC()
		backoff := time.Second << min(event.Attempts-1, 6)
		if retryErr := r.repo.RetryEvent(ctx, event, retryAt, retryAt.Add(backoff)); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, nil
	}
	return true, r.repo.MarkPublished(ctx, event, r.now().UTC())
}
