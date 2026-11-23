package kafkapublisher

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

type fakeProducer struct {
	record *kgo.Record
	err    error
}

func (p *fakeProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.record = records[0]
	return kgo.ProduceResults{{Record: records[0], Err: p.err}}
}

func TestPublishStablePublicEnvelope(t *testing.T) {
	producer := &fakeProducer{}
	publisher := &Publisher{producer: producer, topic: "bpmp.case.events.v1.test", maxBytes: 4096, timeout: time.Second}
	event := application.OutboxEvent{
		TenantID: uuid.New(), EventID: uuid.New(), AggregateID: uuid.New(), AggregateVersion: 2,
		EventType: "CaseWorkflowStarted", SchemaVersion: 1, Producer: "case-service",
		Payload: json.RawMessage(`{"orchestration_status":"ACTIVE"}`), LeaseToken: uuid.New(), Attempts: 3,
	}
	if err := publisher.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if string(producer.record.Key) != event.TenantID.String()+":"+event.AggregateID.String() {
		t.Fatalf("wrong partition key: %s", producer.record.Key)
	}
	var envelope map[string]any
	if err := json.Unmarshal(producer.record.Value, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["event_id"] != event.EventID.String() || envelope["lease_token"] != nil || envelope["attempts"] != nil {
		t.Fatalf("unstable or private envelope: %s", producer.record.Value)
	}
	if producer.record.Headers[0].Key != "bpmp-event-id" || string(producer.record.Headers[0].Value) != event.EventID.String() {
		t.Fatalf("missing event identity header: %+v", producer.record.Headers)
	}
	producer.err = errors.New("broker unavailable")
	if err := publisher.Publish(context.Background(), event); err == nil {
		t.Fatal("broker failure was acknowledged")
	}
	publisher.maxBytes = 1
	if err := publisher.Publish(context.Background(), event); err == nil {
		t.Fatal("oversized event was published")
	}
}
