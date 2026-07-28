package kafkaconsumer

import (
	"context"
	"errors"

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
	client    Client
	handler   RecordHandler
	batchSize func() (int, error)
}

func New(client Client, handler RecordHandler, batchSize func() (int, error)) (*Consumer, error) {
	if client == nil || handler == nil || batchSize == nil {
		return nil, errors.New("projection Kafka consumer configuration is invalid")
	}
	return &Consumer{client: client, handler: handler, batchSize: batchSize}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		batchSize, err := c.batchSize()
		if err != nil {
			return err
		}
		if batchSize <= 0 {
			return errors.New("projection Kafka batch size is invalid")
		}
		fetches := c.client.PollRecords(ctx, batchSize)
		if errs := fetches.Errors(); len(errs) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			return errs[0].Err
		}
		for _, record := range fetches.Records() {
			if err := c.handler.Handle(ctx, record); err != nil {
				return err
			}
			if err := c.client.CommitRecords(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}
