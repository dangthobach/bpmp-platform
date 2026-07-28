package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type PublicationRepository interface {
	ClaimPublicationBatch(context.Context, string, int, time.Duration, time.Time) ([]domain.Publication, error)
	CompletePublicationBatch(context.Context, string, []domain.Publication, time.Time) error
	FailPublicationBatch(context.Context, string, []domain.Publication, time.Time, string) error
}

type PublicationSink interface {
	Publish(context.Context, domain.Publication) error
}

type PublisherConfig struct {
	WorkerID          string
	BatchSize         int
	LeaseDuration     time.Duration
	InitialRetryDelay time.Duration
	MaxRetryDelay     time.Duration
	RetryMultiplier   uint32
}

type Publisher struct {
	repository PublicationRepository
	sink       PublicationSink
	config     PublisherConfig
	now        func() time.Time
}

func NewPublisher(repository PublicationRepository, sink PublicationSink, config PublisherConfig) (*Publisher, error) {
	if repository == nil || sink == nil || config.WorkerID == "" ||
		config.BatchSize <= 0 || config.LeaseDuration <= 0 ||
		config.InitialRetryDelay <= 0 || config.MaxRetryDelay < config.InitialRetryDelay ||
		config.RetryMultiplier < 1000 {
		return nil, errors.New("configuration publisher dependencies are invalid")
	}
	return &Publisher{repository: repository, sink: sink, config: config, now: time.Now}, nil
}

func (p *Publisher) RunOnce(ctx context.Context) (int, error) {
	now := p.now().UTC()
	records, err := p.repository.ClaimPublicationBatch(
		ctx, p.config.WorkerID, p.config.BatchSize, p.config.LeaseDuration, now,
	)
	if err != nil || len(records) == 0 {
		return 0, err
	}
	for _, record := range records {
		if err = p.sink.Publish(ctx, record); err != nil {
			nextAttempt := now.Add(p.retryDelay(record.AttemptCount))
			failErr := p.repository.FailPublicationBatch(
				ctx, p.config.WorkerID, records, nextAttempt, boundedError(err),
			)
			if failErr != nil {
				return 0, errors.Join(err, failErr)
			}
			return 0, err
		}
	}
	if err = p.repository.CompletePublicationBatch(ctx, p.config.WorkerID, records, p.now().UTC()); err != nil {
		return 0, err
	}
	return len(records), nil
}

func (p *Publisher) retryDelay(attempt uint32) time.Duration {
	delay := p.config.InitialRetryDelay
	for current := uint32(1); current < attempt && delay < p.config.MaxRetryDelay; current++ {
		next := time.Duration((uint64(delay) * uint64(p.config.RetryMultiplier)) / 1000)
		if next <= delay || next >= p.config.MaxRetryDelay {
			return p.config.MaxRetryDelay
		}
		delay = next
	}
	return delay
}

func boundedError(err error) string {
	message := fmt.Sprintf("%T: %v", err, err)
	const maxBytes = 1024
	if len(message) > maxBytes {
		return message[:maxBytes]
	}
	return message
}
