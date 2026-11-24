package engineevents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type RecordHandler interface {
	Handle(context.Context, *kgo.Record) error
}

type Consumer struct {
	client  KafkaClient
	handler RecordHandler
	config  kafkaconfig.Consumer
}

func NewConsumer(client KafkaClient, handler RecordHandler, config kafkaconfig.Consumer) (*Consumer, error) {
	if client == nil || handler == nil || config.Validate() != nil || config.MaxMessageBytes > MaxEventBytes {
		return nil, errors.New("Case Engine event consumer configuration is invalid")
	}
	return &Consumer{client: client, handler: handler, config: config}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, c.config.PollTimeout())
		fetches := c.client.PollRecords(pollCtx, c.config.BatchSize)
		cancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			if err := kafkaconfig.CheckFetchErrors(ctx, errs); err != nil {
				return fmt.Errorf("poll committed Engine events: %w", err)
			}
		}
		records := fetches.Records()
		if len(records) == 0 {
			continue
		}
		for _, record := range records {
			recordCtx, span := requestmeta.StartKafkaConsumerSpan(ctx, record)
			err := c.handler.Handle(recordCtx, record)
			requestmeta.RecordSpanError(span, err)
			span.End()
			if err != nil {
				return fmt.Errorf("apply committed Engine event at %s[%d] offset %d: %w", record.Topic, record.Partition, record.Offset, err)
			}
		}
		// Local projection/inbox transaction commits before the Kafka offset. A
		// crash in between replays the record and the durable inbox deduplicates it.
		if err := kafkaconfig.RetryOffsetCommit(ctx, c.client, 3, 200*time.Millisecond, 5*time.Second, records...); err != nil {
			return fmt.Errorf("commit Case Engine event offsets: %w", err)
		}
	}
	return nil
}
