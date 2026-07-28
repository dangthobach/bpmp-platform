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
	policy        func() (int, time.Duration, time.Duration, error)
}

func NewEscalationWorker(store EscalationStore, publisher EscalationPublisher, workerID string, batchSize int, leaseDuration, retryDelay time.Duration) (*EscalationWorker, error) {
	if store == nil || publisher == nil || workerID == "" || batchSize <= 0 || leaseDuration <= 0 || retryDelay <= 0 {
		return nil, errors.New("valid escalation worker configuration is required")
	}
	return &EscalationWorker{store: store, publisher: publisher, workerID: workerID, batchSize: batchSize, leaseDuration: leaseDuration, retryDelay: retryDelay}, nil
}

func NewDynamicEscalationWorker(
	store EscalationStore,
	publisher EscalationPublisher,
	workerID string,
	policy func() (int, time.Duration, time.Duration, error),
) (*EscalationWorker, error) {
	if store == nil || publisher == nil || workerID == "" || policy == nil {
		return nil, errors.New("valid dynamic escalation worker configuration is required")
	}
	return &EscalationWorker{
		store: store, publisher: publisher, workerID: workerID, policy: policy,
	}, nil
}

func (w *EscalationWorker) RunOnce(ctx context.Context, now time.Time) (int, error) {
	batchSize, leaseDuration, retryDelay := w.batchSize, w.leaseDuration, w.retryDelay
	if w.policy != nil {
		var err error
		batchSize, leaseDuration, retryDelay, err = w.policy()
		if err != nil {
			return 0, err
		}
	}
	if batchSize <= 0 || leaseDuration <= 0 || retryDelay <= 0 {
		return 0, errors.New("escalation runtime policy is invalid")
	}
	claims, err := w.store.ClaimDueEscalations(ctx, now, now.Add(leaseDuration), w.workerID, batchSize)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, claim := range claims {
		if err = w.publisher.PublishEscalation(ctx, claim); err != nil {
			if retryErr := w.store.RetryEscalation(ctx, claim, w.workerID, now.Add(retryDelay)); retryErr != nil {
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
