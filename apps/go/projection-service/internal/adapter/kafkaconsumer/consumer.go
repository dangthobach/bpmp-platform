package kafkaconsumer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type RecordHandler interface {
	Handle(context.Context, *kgo.Record) error
}

type Client interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type Consumer struct {
	client  Client
	handler RecordHandler
	policy  func() (RuntimePolicy, error)
}

type RuntimePolicy struct {
	ConsumeBatchSize         int
	RebuildBatchSize         int
	RealtimePublishBatchSize int
	CheckpointFlush          time.Duration
	MaxLag                   time.Duration
}

func (p RuntimePolicy) validate() error {
	if p.ConsumeBatchSize <= 0 ||
		p.RebuildBatchSize <= 0 ||
		p.RealtimePublishBatchSize <= 0 ||
		p.CheckpointFlush <= 0 ||
		p.MaxLag <= 0 {
		return errors.New("projection Kafka runtime policy is invalid")
	}
	return nil
}

func New(
	client Client,
	handler RecordHandler,
	policy func() (RuntimePolicy, error),
) (*Consumer, error) {
	if client == nil || handler == nil || policy == nil {
		return nil, errors.New("projection Kafka consumer configuration is invalid")
	}
	return &Consumer{client: client, handler: handler, policy: policy}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	catchingUp := false
	for ctx.Err() == nil {
		policy, err := c.policy()
		if err != nil {
			return err
		}
		if err = policy.validate(); err != nil {
			return err
		}
		batchSize := min(policy.ConsumeBatchSize, policy.RealtimePublishBatchSize)
		if catchingUp {
			batchSize = min(policy.ConsumeBatchSize, policy.RebuildBatchSize)
		}
		deadline := time.Now().Add(policy.CheckpointFlush)
		pending := make([]*kgo.Record, 0, batchSize)
		nextCatchingUp := false
		for len(pending) < batchSize && ctx.Err() == nil {
			fetches := c.client.PollRecords(ctx, batchSize-len(pending))
			if errs := fetches.Errors(); len(errs) > 0 {
				if ctx.Err() != nil {
					return nil
				}
				return errs[0].Err
			}
			for _, record := range fetches.Records() {
				if !record.Timestamp.IsZero() &&
					time.Since(record.Timestamp) > policy.MaxLag {
					nextCatchingUp = true
					slog.Warn(
						"projection event exceeds configured lag",
						"topic", record.Topic,
						"partition", record.Partition,
						"offset", record.Offset,
						"max_lag", policy.MaxLag,
					)
				}
				if err = c.handler.Handle(ctx, record); err != nil {
					return err
				}
				pending = append(pending, record)
			}
			if len(pending) > 0 && !time.Now().Before(deadline) {
				break
			}
		}
		if len(pending) > 0 {
			if err = c.client.CommitRecords(ctx, pending...); err != nil {
				return err
			}
		}
		catchingUp = nextCatchingUp
	}
	return nil
}
