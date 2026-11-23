package kafkapublisher

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestPublisherBrokerRoundTrip(t *testing.T) {
	broker := os.Getenv("CASE_SERVICE_KAFKA_BROKER")
	if broker == "" {
		t.Skip("CASE_SERVICE_KAFKA_BROKER is not configured")
	}
	const topic = "bpmp.case.events.v1.test"
	config := kafkaconfig.Producer{
		Bootstrap: kafkaconfig.Bootstrap{Brokers: []string{broker}, ClientID: "bpmp-case-outbox-it",
			SecurityProtocol: kafkaconfig.ProtocolPlaintext, DialTimeoutMS: 2000, RequestTimeoutMS: 2000},
		Topic: topic, MessageTimeoutMS: 5000, MaxInflight: 1, MaxMessageBytes: 4096,
		RequiredAcks: "ALL", EnableIdempotence: true,
	}
	client, err := kafkaconfig.NewProducerClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	response, err := client.Request(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	created := response.(*kmsg.CreateTopicsResponse)
	if len(created.Topics) != 1 {
		t.Fatalf("unexpected topic creation response: %+v", created.Topics)
	}
	if topicErr := kerr.ErrorForCode(created.Topics[0].ErrorCode); topicErr != nil && topicErr != kerr.TopicAlreadyExists {
		t.Fatal(topicErr)
	}
	publisher, err := New(client, config)
	if err != nil {
		t.Fatal(err)
	}
	event := application.OutboxEvent{TenantID: uuid.New(), EventID: uuid.New(), AggregateID: uuid.New(),
		AggregateVersion: 1, EventType: "CaseCreated", SchemaVersion: 1, Producer: "case-service",
		Payload: json.RawMessage(`{"status":"START_PENDING"}`), LeaseToken: uuid.New(), Attempts: 2}
	if err = publisher.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	for ctx.Err() == nil {
		fetches := consumer.PollFetches(ctx)
		if errs := fetches.Errors(); len(errs) > 0 {
			t.Fatal(errs)
		}
		for _, record := range fetches.Records() {
			var got application.OutboxEvent
			if err = json.Unmarshal(record.Value, &got); err != nil {
				t.Fatal(err)
			}
			if got.EventID == event.EventID {
				if got.AggregateID != event.AggregateID || got.Attempts != 0 || got.LeaseToken != uuid.Nil {
					t.Fatalf("unexpected broker envelope: %+v", got)
				}
				return
			}
		}
	}
	t.Fatal("Case event did not arrive before deadline")
}
