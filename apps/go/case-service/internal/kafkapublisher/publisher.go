package kafkapublisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

type producer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

type Publisher struct {
	producer producer
	topic    string
	maxBytes int
	timeout  time.Duration
}

// New accepts a producer configured with ALL acknowledgements and idempotence.
func New(client *kgo.Client, config kafkaconfig.Producer) (*Publisher, error) {
	if client == nil || config.Validate() != nil || config.MessageTimeout() > 20*time.Second {
		return nil, kafkaconfig.ErrInvalidConfig
	}
	return &Publisher{producer: client, topic: config.Topic, maxBytes: config.MaxMessageBytes, timeout: config.MessageTimeout()}, nil
}

func (p *Publisher) Publish(ctx context.Context, event application.OutboxEvent) error {
	if event.TenantID == uuid.Nil || event.EventID == uuid.Nil || event.AggregateID == uuid.Nil || event.AggregateVersion < 1 ||
		event.SchemaVersion < 1 || event.EventType == "" || event.Producer == "" || !json.Valid(event.Payload) {
		return application.ErrInvalid
	}
	// Marshal the public envelope, excluding lease state, as one stable record.
	value, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(value) > p.maxBytes {
		return errors.New("Case event exceeds Kafka message bound")
	}
	record := &kgo.Record{
		Topic: p.topic,
		Key:   []byte(event.TenantID.String() + ":" + event.AggregateID.String()),
		Value: value,
		Headers: []kgo.RecordHeader{
			{Key: "bpmp-event-id", Value: []byte(event.EventID.String())},
			{Key: "bpmp-tenant-id", Value: []byte(event.TenantID.String())},
			{Key: "bpmp-schema-version", Value: []byte(fmt.Sprint(event.SchemaVersion))},
		},
	}
	publishCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err = p.producer.ProduceSync(publishCtx, record).FirstErr(); err != nil {
		return fmt.Errorf("publish Case event %s: %w", event.EventID, err)
	}
	return nil
}
