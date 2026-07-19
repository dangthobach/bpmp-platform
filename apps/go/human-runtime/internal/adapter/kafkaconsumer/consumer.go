package kafkaconsumer

import (
	"context"
	"errors"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Handler interface {
	Handle(context.Context, []byte) error
}
type Client interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}
type Consumer struct {
	client    Client
	handler   Handler
	batchSize int
}

func New(client Client, handler Handler, batchSize int) (*Consumer, error) {
	if client == nil || handler == nil || batchSize <= 0 {
		return nil, errors.New("Kafka client, handler, and positive batch size are required")
	}
	return &Consumer{client: client, handler: handler, batchSize: batchSize}, nil
}
func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		fetches := c.client.PollRecords(ctx, c.batchSize)
		if errs := fetches.Errors(); len(errs) > 0 {
			return errs[0].Err
		}
		records := fetches.Records()
		for _, record := range records {
			if err := c.HandleRecord(ctx, record); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
func (c *Consumer) HandleRecord(ctx context.Context, record *kgo.Record) error {
	if err := c.handler.Handle(ctx, record.Value); err != nil {
		return err
	}
	return c.client.CommitRecords(ctx, record)
}
