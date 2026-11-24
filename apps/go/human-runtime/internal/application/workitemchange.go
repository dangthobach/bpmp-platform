package application

import (
	"context"
	"errors"
	"time"
)

type WorkItemChange struct {
	TenantID   string
	EventID    string
	WorkItemID string
	Payload    []byte
	Attempts   int
}

type WorkItemChangeStore interface {
	ClaimWorkItemChanges(context.Context, time.Time, time.Time, string, int) ([]WorkItemChange, error)
	AckWorkItemChange(context.Context, WorkItemChange, string, time.Time) error
	RetryWorkItemChange(context.Context, WorkItemChange, string, time.Time) error
}

type WorkItemChangePublisher interface {
	PublishWorkItemChange(context.Context, WorkItemChange) error
}

type WorkItemChangeWorker struct {
	store      WorkItemChangeStore
	publisher  WorkItemChangePublisher
	workerID   string
	batchSize  int
	lease      time.Duration
	retryDelay time.Duration
}

func NewWorkItemChangeWorker(store WorkItemChangeStore, publisher WorkItemChangePublisher, workerID string, batchSize int, lease, retryDelay time.Duration) (*WorkItemChangeWorker, error) {
	if store == nil || publisher == nil || workerID == "" || batchSize <= 0 || batchSize > 1000 || lease <= 0 || retryDelay <= 0 {
		return nil, errors.New("valid work-item change worker configuration is required")
	}
	return &WorkItemChangeWorker{store: store, publisher: publisher, workerID: workerID, batchSize: batchSize, lease: lease, retryDelay: retryDelay}, nil
}

func (w *WorkItemChangeWorker) RunOnce(ctx context.Context, now time.Time) (int, error) {
	claims, err := w.store.ClaimWorkItemChanges(ctx, now, now.Add(w.lease), w.workerID, w.batchSize)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, claim := range claims {
		if err = w.publisher.PublishWorkItemChange(ctx, claim); err != nil {
			if retryErr := w.store.RetryWorkItemChange(ctx, claim, w.workerID, now.Add(w.retryDelay)); retryErr != nil {
				return published, retryErr
			}
			continue
		}
		if err = w.store.AckWorkItemChange(ctx, claim, w.workerID, now); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}
