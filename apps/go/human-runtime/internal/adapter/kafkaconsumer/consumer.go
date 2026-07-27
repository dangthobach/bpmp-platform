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
	batchSize func() (int, error)
}

func New(client Client, handler Handler, batchSize int) (*Consumer, error) {
	if client == nil || handler == nil || batchSize <= 0 {
		return nil, errors.New("Kafka client, handler, and positive batch size are required")
	}
	return &Consumer{
		client: client, handler: handler,
		batchSize: func() (int, error) { return batchSize, nil },
	}, nil
}

func NewDynamic(
	client Client,
	handler Handler,
	batchSize func() (int, error),
) (*Consumer, error) {
	if client == nil || handler == nil || batchSize == nil {
		return nil, errors.New("Kafka client, handler, and batch policy are required")
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
			return errors.New("Kafka batch policy is invalid")
		}
		fetches := c.client.PollRecords(ctx, batchSize)
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
