package application

import (
	"context"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type TenantReadinessRepository interface {
	ClaimTenantReadinessBatch(
		context.Context,
		string,
		int,
		time.Duration,
		time.Time,
	) ([]domain.TenantReadinessPublication, error)
	CompleteTenantReadinessBatch(
		context.Context,
		string,
		[]domain.TenantReadinessPublication,
		time.Time,
	) error
	FailTenantReadinessBatch(
		context.Context,
		string,
		[]domain.TenantReadinessPublication,
		time.Time,
		string,
	) error
}

type TenantReadinessSink interface {
	PublishTenantReadiness(context.Context, domain.TenantReadinessPublication) error
}

type TenantReadinessPublisher struct {
	repository TenantReadinessRepository
	sink       TenantReadinessSink
	config     PublisherConfig
	now        func() time.Time
}

func NewTenantReadinessPublisher(
	repository TenantReadinessRepository,
	sink TenantReadinessSink,
	config PublisherConfig,
) (*TenantReadinessPublisher, error) {
	if repository == nil || sink == nil || config.WorkerID == "" ||
		config.BatchSize <= 0 || config.LeaseDuration <= 0 ||
		config.InitialRetryDelay <= 0 ||
		config.MaxRetryDelay < config.InitialRetryDelay ||
		config.RetryMultiplier < 1000 {
		return nil, errors.New("tenant readiness publisher dependencies are invalid")
	}
	return &TenantReadinessPublisher{
		repository: repository,
		sink:       sink,
		config:     config,
		now:        time.Now,
	}, nil
}

func (p *TenantReadinessPublisher) RunOnce(ctx context.Context) (int, error) {
	now := p.now().UTC()
	records, err := p.repository.ClaimTenantReadinessBatch(
		ctx,
		p.config.WorkerID,
		p.config.BatchSize,
		p.config.LeaseDuration,
		now,
	)
	if err != nil || len(records) == 0 {
		return 0, err
	}
	for _, record := range records {
		if err = p.sink.PublishTenantReadiness(ctx, record); err != nil {
			nextAttempt := now.Add(retryDelay(p.config, record.AttemptCount))
			failErr := p.repository.FailTenantReadinessBatch(
				ctx,
				p.config.WorkerID,
				records,
				nextAttempt,
				boundedError(err),
			)
			if failErr != nil {
				return 0, errors.Join(err, failErr)
			}
			return 0, err
		}
	}
	err = p.repository.CompleteTenantReadinessBatch(
		ctx,
		p.config.WorkerID,
		records,
		p.now().UTC(),
	)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func retryDelay(config PublisherConfig, attempt uint32) time.Duration {
	delay := config.InitialRetryDelay
	for current := uint32(1); current < attempt && delay < config.MaxRetryDelay; current++ {
		next := time.Duration(
			(uint64(delay) * uint64(config.RetryMultiplier)) / 1000,
		)
		if next <= delay || next >= config.MaxRetryDelay {
			return config.MaxRetryDelay
		}
		delay = next
	}
	return delay
}
