package kafkapublisher

import (
	"context"
	"errors"
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/twmb/franz-go/pkg/kgo"
)

type recordingProducer struct {
	record *kgo.Record
	err    error
}

func (p *recordingProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.record = records[0]
	return kgo.ProduceResults{{Record: records[0], Err: p.err}}
}

func TestEscalationPublisherUsesStableTenantWorkItemKeyAndWaitsForAck(t *testing.T) {
	producer := &recordingProducer{}
	publisher, _ := NewEscalationPublisher(producer, "human-escalations")
	escalation := application.Escalation{TenantID: "tenant-a", EscalationID: "escalation-1", WorkItemID: "work-1", Payload: []byte(`{"policy_ref":"manager"}`)}
	if err := publisher.PublishEscalation(context.Background(), escalation); err != nil {
		t.Fatal(err)
	}
	if producer.record.Topic != "human-escalations" || string(producer.record.Key) != "tenant-a:work-1" {
		t.Fatalf("unexpected Kafka record: %#v", producer.record)
	}
	producer.err = errors.New("broker unavailable")
	if err := publisher.PublishEscalation(context.Background(), escalation); err == nil {
		t.Fatal("broker acknowledgement error was ignored")
	}
}
