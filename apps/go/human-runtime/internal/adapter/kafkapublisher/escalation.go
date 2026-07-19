package kafkapublisher

import (
	"context"
	"errors"
	"fmt"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/twmb/franz-go/pkg/kgo"
)

type SyncProducer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

type EscalationPublisher struct {
	producer SyncProducer
	topic    string
}

func NewEscalationPublisher(producer SyncProducer, topic string) (*EscalationPublisher, error) {
	if producer == nil || topic == "" {
		return nil, errors.New("Kafka producer and escalation topic are required")
	}
	return &EscalationPublisher{producer: producer, topic: topic}, nil
}

func (p *EscalationPublisher) PublishEscalation(ctx context.Context, escalation application.Escalation) error {
	if escalation.TenantID == "" || escalation.EscalationID == "" || escalation.WorkItemID == "" || len(escalation.Payload) == 0 {
		return errors.New("escalation envelope is incomplete")
	}
	record := &kgo.Record{
		Topic: p.topic,
		Key:   []byte(escalation.TenantID + ":" + escalation.WorkItemID),
		Value: append([]byte(nil), escalation.Payload...),
		Headers: []kgo.RecordHeader{
			{Key: "bpmp-event-type", Value: []byte("human.sla.escalation.v1")},
			{Key: "bpmp-tenant-id", Value: []byte(escalation.TenantID)},
			{Key: "bpmp-escalation-id", Value: []byte(escalation.EscalationID)},
		},
	}
	results := p.producer.ProduceSync(ctx, record)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("publish escalation %s: %w", escalation.EscalationID, err)
	}
	return nil
}

var _ application.EscalationPublisher = (*EscalationPublisher)(nil)
