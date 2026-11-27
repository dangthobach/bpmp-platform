package odsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// OutboxEnvelope is the standard versioned ODS integration event envelope.
type OutboxEnvelope struct {
	EventID          string          `json:"event_id"`
	EventType        string          `json:"event_type"`
	SchemaVersion    int             `json:"schema_version"`
	TenantID         string          `json:"tenant_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	OccurredAt       time.Time       `json:"occurred_at"`
	Producer         string          `json:"producer"`
	CorrelationID    string          `json:"correlation_id"`
	TraceParent      string          `json:"traceparent,omitempty"`
	Payload          json.RawMessage `json:"payload"`
}

// Publisher abstracts durable broker publication (Kafka/Redpanda).
type Publisher interface {
	Publish(ctx context.Context, topic, partitionKey string, env OutboxEnvelope) error
	Close()
}

// KafkaPublisher publishes outbox envelopes to Kafka with RequiredAcks=All and idempotent delivery.
type KafkaPublisher struct {
	client  *kgo.Client
	timeout time.Duration
}

func NewKafkaPublisher(brokers []string, clientID string, produceTimeout time.Duration) (*KafkaPublisher, error) {
	if len(brokers) == 0 {
		return nil, errors.New("kafka brokers are required")
	}
	if produceTimeout <= 0 {
		produceTimeout = 5 * time.Second
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(clientID),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordRetries(3),
		kgo.ProduceRequestTimeout(produceTimeout),
		kgo.MaxBufferedRecords(1024),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka publisher: %w", err)
	}
	return &KafkaPublisher{client: client, timeout: produceTimeout}, nil
}

func (p *KafkaPublisher) Publish(ctx context.Context, topic, partitionKey string, env OutboxEnvelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	headers := []kgo.RecordHeader{
		{Key: "event_id", Value: []byte(env.EventID)},
		{Key: "event_type", Value: []byte(env.EventType)},
		{Key: "tenant_id", Value: []byte(env.TenantID)},
		{Key: "correlation_id", Value: []byte(env.CorrelationID)},
	}
	if env.TraceParent != "" {
		headers = append(headers, kgo.RecordHeader{Key: "traceparent", Value: []byte(env.TraceParent)})
	}
	rec := &kgo.Record{
		Topic:   topic,
		Key:     []byte(partitionKey),
		Value:   payload,
		Headers: headers,
	}
	pubCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.client.ProduceSync(pubCtx, rec).FirstErr()
}

func (p *KafkaPublisher) Close() {
	if p != nil && p.client != nil {
		p.client.Close()
	}
}

// OutboxStore is implemented by each domain service's PostgreSQL adapter.
type OutboxStore interface {
	FetchUnpublished(ctx context.Context, limit int) ([]OutboxEnvelope, error)
	MarkPublished(ctx context.Context, eventID string, publishedAt time.Time) error
}

// RelayConfig controls bounded outbox polling and backoff.
type RelayConfig struct {
	Topic        string
	BatchSize    int
	PollInterval time.Duration
	MaxBackoff   time.Duration
}

// RunOutboxRelay continuously relays committed outbox rows to Kafka and marks published_at ONLY after broker ACK.
func RunOutboxRelay(ctx context.Context, logger *slog.Logger, store OutboxStore, pub Publisher, cfg RelayConfig) {
	if cfg.BatchSize <= 0 || cfg.BatchSize > 256 {
		cfg.BatchSize = 50
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 10 * time.Second
	}

	backoff := cfg.PollInterval
	for {
		if ctx.Err() != nil {
			return
		}
		published, hadErr := RelayOnce(ctx, logger, store, pub, cfg.Topic, cfg.BatchSize)
		if hadErr {
			sleepCtx(ctx, backoff)
			backoff = min(backoff*2, cfg.MaxBackoff)
			continue
		}
		backoff = cfg.PollInterval
		if published == 0 {
			sleepCtx(ctx, cfg.PollInterval)
		}
	}
}

// RelayOnce executes a single bounded outbox relay pass. Returns (publishedCount, hadError).
func RelayOnce(ctx context.Context, logger *slog.Logger, store OutboxStore, pub Publisher, topic string, batchSize int) (int, bool) {
	events, err := store.FetchUnpublished(ctx, batchSize)
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("outbox fetch failed", "topic", topic, "error", err)
		}
		return 0, true
	}
	published := 0
	for _, env := range events {
		partKey := env.TenantID + ":" + env.AggregateID
		if err := pub.Publish(ctx, topic, partKey, env); err != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("outbox publish failed; retaining unpublished", "topic", topic, "event_id", env.EventID, "error", err)
			}
			return published, true
		}
		if err := store.MarkPublished(ctx, env.EventID, time.Now().UTC()); err != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("outbox mark published failed", "event_id", env.EventID, "error", err)
			}
			return published, true
		}
		published++
	}
	return published, false
}

// ConsumerConfig configures a resilient at-least-once Kafka consumer with manual offset commit.
type ConsumerConfig struct {
	Brokers       []string
	GroupID       string
	Topics        []string
	ClientID      string
	MaxPollBatch  int
	CommitTimeout time.Duration
}

// RunConsumer consumes committed domain events and commits offsets ONLY after handler succeeds.
func RunConsumer(ctx context.Context, logger *slog.Logger, cfg ConsumerConfig, handle func(context.Context, string, []byte) error) error {
	if len(cfg.Brokers) == 0 || cfg.GroupID == "" || len(cfg.Topics) == 0 {
		return errors.New("consumer requires brokers, group_id, and topics")
	}
	if cfg.MaxPollBatch <= 0 {
		cfg.MaxPollBatch = 64
	}
	if cfg.CommitTimeout <= 0 {
		cfg.CommitTimeout = 5 * time.Second
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
		kgo.ConsumerGroup(cfg.GroupID),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.DisableAutoCommit(),
		kgo.FetchMaxBytes(2<<20),
	)
	if err != nil {
		return fmt.Errorf("create kafka consumer: %w", err)
	}
	defer client.Close()

	for {
		if ctx.Err() != nil {
			return nil
		}
		fetches := client.PollRecords(ctx, cfg.MaxPollBatch)
		if ctx.Err() != nil {
			return nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fe := range errs {
				if logger != nil {
					logger.Warn("kafka fetch error (retrying)", "topic", fe.Topic, "partition", fe.Partition, "error", fe.Err)
				}
			}
			sleepCtx(ctx, time.Second)
			continue
		}

		allHandled := true
		fetches.EachRecord(func(rec *kgo.Record) {
			if !allHandled {
				return
			}
			if err := handle(ctx, rec.Topic, rec.Value); err != nil {
				allHandled = false
				if logger != nil {
					logger.Warn("consumer handler failed; skipping offset commit", "topic", rec.Topic, "offset", rec.Offset, "error", err)
				}
			}
		})

		if allHandled && !fetches.Empty() {
			commitCtx, cancel := context.WithTimeout(ctx, cfg.CommitTimeout)
			if err := client.CommitUncommittedOffsets(commitCtx); err != nil && logger != nil {
				logger.Warn("kafka offset commit failed", "error", err)
			}
			cancel()
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
