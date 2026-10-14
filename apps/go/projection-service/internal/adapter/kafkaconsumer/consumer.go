package kafkaconsumer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
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
	CommitRetryAttempts      uint32
	CommitBackoff            time.Duration
	CommitTimeout            time.Duration
}

func (p RuntimePolicy) validate() error {
	if p.ConsumeBatchSize <= 0 ||
		p.RebuildBatchSize <= 0 ||
		p.RealtimePublishBatchSize <= 0 ||
		p.CheckpointFlush <= 0 ||
		p.MaxLag <= 0 || p.CommitRetryAttempts == 0 || p.CommitRetryAttempts > 20 ||
		p.CommitBackoff <= 0 || p.CommitBackoff > 5*time.Minute || p.CommitTimeout <= 0 || p.CommitTimeout > 5*time.Minute {
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
			pollCtx, cancelPoll := context.WithDeadline(ctx, deadline)
			fetches := c.client.PollRecords(pollCtx, batchSize-len(pending))
			pollExpired := errors.Is(pollCtx.Err(), context.DeadlineExceeded)
			cancelPoll()
			faults := fetches.Errors()
			if pollExpired {
				filtered := faults[:0]
				for _, fault := range faults {
					if !errors.Is(fault.Err, context.DeadlineExceeded) {
						filtered = append(filtered, fault)
					}
				}
				faults = filtered
			}
			if errs := faults; len(errs) > 0 {
				if ctx.Err() != nil {
					return nil
				}
				if err := kafkaconfig.CheckFetchErrors(ctx, errs); err != nil {
					return err
				}
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
				if err = c.handleRecord(ctx, record); err != nil {
					return err
				}
				pending = append(pending, record)
			}
			if !time.Now().Before(deadline) {
				break
			}
		}
		if len(pending) > 0 {
			if err = kafkaconfig.RetryOffsetCommit(ctx, c.client, policy.CommitRetryAttempts, policy.CommitBackoff, policy.CommitTimeout, pending...); err != nil {
				return err
			}
		}
		catchingUp = nextCatchingUp
	}
	return nil
}

func (c *Consumer) handleRecord(ctx context.Context, record *kgo.Record) error {
	ctx, span := requestmeta.StartKafkaConsumerSpan(ctx, record)
	defer span.End()
	err := c.handler.Handle(ctx, record)
	requestmeta.RecordSpanError(span, err)
	return err
}
