package kafkapublisher

import (
	"context"
	"errors"
	"fmt"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"github.com/twmb/franz-go/pkg/kgo"
)

type WorkItemChangePublisher struct {
	producer SyncProducer
	topic    string
}

func NewWorkItemChangePublisher(producer SyncProducer, topic string) (*WorkItemChangePublisher, error) {
	if producer == nil || topic == "" {
		return nil, errors.New("kafka producer and human change topic are required")
	}
	return &WorkItemChangePublisher{producer: producer, topic: topic}, nil
}

func (p *WorkItemChangePublisher) PublishWorkItemChange(ctx context.Context, event application.WorkItemChange) error {
	if event.TenantID == "" || event.EventID == "" || event.WorkItemID == "" || len(event.Payload) == 0 {
		return errors.New("work-item change event is incomplete")
	}
	record := &kgo.Record{
		Topic: p.topic,
		Key:   []byte(event.TenantID + ":" + event.WorkItemID),
		Value: append([]byte(nil), event.Payload...),
		Headers: []kgo.RecordHeader{
			{Key: "bpmp-event-type", Value: []byte("bpmp.human.v1.WorkItemChangedEvent")},
			{Key: "bpmp-tenant-id", Value: []byte(event.TenantID)},
			{Key: "bpmp-event-id", Value: []byte(event.EventID)},
		},
	}
	ctx = requestmeta.WithValues(ctx, requestmeta.Values{
		RequestID: event.EventID, CorrelationID: event.EventID, TenantID: event.TenantID,
	})
	ctx, span := requestmeta.StartKafkaProducerSpan(ctx, record)
	defer span.End()
	requestmeta.InjectKafka(ctx, record)
	if err := p.producer.ProduceSync(ctx, record).FirstErr(); err != nil {
		requestmeta.RecordSpanError(span, err)
		return fmt.Errorf("publish work-item change %s: %w", event.EventID, err)
	}
	return nil
}

var _ application.WorkItemChangePublisher = (*WorkItemChangePublisher)(nil)
