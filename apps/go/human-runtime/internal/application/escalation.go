package application

import (
	"context"
	"errors"
	"time"
)

type Escalation struct {
	TenantID     string
	EscalationID string
	WorkItemID   string
	PolicyRef    string
	Payload      []byte
	Attempts     int
}

type EscalationStore interface {
	ClaimDueEscalations(context.Context, time.Time, time.Time, string, int) ([]Escalation, error)
	AckEscalation(context.Context, Escalation, string, time.Time) error
	RetryEscalation(context.Context, Escalation, string, time.Time) error
}

type EscalationPublisher interface {
	PublishEscalation(context.Context, Escalation) error
}

type EscalationWorker struct {
	store         EscalationStore
	publisher     EscalationPublisher
	workerID      string
	batchSize     int
	leaseDuration time.Duration
	retryDelay    time.Duration
}

func NewEscalationWorker(store EscalationStore, publisher EscalationPublisher, workerID string, batchSize int, leaseDuration, retryDelay time.Duration) (*EscalationWorker, error) {
	if store == nil || publisher == nil || workerID == "" || batchSize <= 0 || leaseDuration <= 0 || retryDelay <= 0 {
		return nil, errors.New("valid escalation worker configuration is required")
	}
	return &EscalationWorker{store: store, publisher: publisher, workerID: workerID, batchSize: batchSize, leaseDuration: leaseDuration, retryDelay: retryDelay}, nil
}

func (w *EscalationWorker) RunOnce(ctx context.Context, now time.Time) (int, error) {
	claims, err := w.store.ClaimDueEscalations(ctx, now, now.Add(w.leaseDuration), w.workerID, w.batchSize)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, claim := range claims {
		if err = w.publisher.PublishEscalation(ctx, claim); err != nil {
			if retryErr := w.store.RetryEscalation(ctx, claim, w.workerID, now.Add(w.retryDelay)); retryErr != nil {
				return published, retryErr
			}
			continue
		}
		if err = w.store.AckEscalation(ctx, claim, w.workerID, now); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}
