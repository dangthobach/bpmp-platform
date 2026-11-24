package engineevents

import (
	"context"
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/twmb/franz-go/pkg/kgo"
)

type consumerClient struct {
	fetches []kgo.Fetches
	cancel  context.CancelFunc
	commits int
}

func (c *consumerClient) PollRecords(_ context.Context, _ int) kgo.Fetches {
	if len(c.fetches) == 0 {
		c.cancel()
		return kgo.Fetches{}
	}
	result := c.fetches[0]
	c.fetches = c.fetches[1:]
	return result
}

func (c *consumerClient) CommitRecords(_ context.Context, _ ...*kgo.Record) error {
	c.commits++
	return nil
}

func consumerConfig() kafkaconfig.Consumer {
	return kafkaconfig.Consumer{
		Bootstrap: kafkaconfig.Bootstrap{Brokers: []string{"127.0.0.1:9092"}, ClientID: "bpmp-case-events-tests",
			SecurityProtocol: kafkaconfig.ProtocolPlaintext, DialTimeoutMS: 100, RequestTimeoutMS: 100},
		Topic: "bpmp.engine.committed-events.v1.test", ConsumerGroup: "bpmp.case.engine-events.v1.test",
		BatchSize: 10, MaxMessageBytes: MaxEventBytes, PollTimeoutMS: 50, SessionTimeoutMS: 500,
	}
}

func TestConsumerDoesNotCommitAfterHandlerFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClient{cancel: cancel, fetches: []kgo.Fetches{fetchWithRecords(&kgo.Record{Topic: "bpmp.engine.committed-events.v1.test", Value: []byte("invalid")})}}
	repository := &eventRecorder{}
	service, _ := application.NewEngineEventService(repository)
	handler, _ := New(service)
	consumer, err := NewConsumer(client, handler, consumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = consumer.Run(ctx); err == nil {
		t.Fatal("invalid event did not stop the consumer")
	}
	if client.commits != 0 {
		t.Fatalf("consumer committed %d offsets after handler failure", client.commits)
	}
}

func TestConsumerCommitsAfterDurableHandlerSuccessAndStopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClient{cancel: cancel, fetches: []kgo.Fetches{fetchWithRecords(&kgo.Record{Topic: "bpmp.engine.committed-events.v1.test", Value: []byte("valid protobuf")})}}
	consumer, err := NewConsumer(client, handlerFunc(func(context.Context, *kgo.Record) error { return nil }), consumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if client.commits != 1 {
		t.Fatalf("consumer commits = %d, want 1", client.commits)
	}
}

func TestConsumerRejectsUnboundedMessageConfiguration(t *testing.T) {
	client := &consumerClient{cancel: func() {}}
	consumerConfig := consumerConfig()
	consumerConfig.MaxMessageBytes = MaxEventBytes + 1
	if _, err := NewConsumer(client, handlerFunc(func(context.Context, *kgo.Record) error { return nil }), consumerConfig); err == nil {
		t.Fatal("consumer accepted a Kafka message bound larger than the protobuf handler bound")
	}
}

type handlerFunc func(context.Context, *kgo.Record) error

func (f handlerFunc) Handle(ctx context.Context, record *kgo.Record) error { return f(ctx, record) }

func fetchWithRecords(records ...*kgo.Record) kgo.Fetches {
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "bpmp.engine.committed-events.v1.test", Partitions: []kgo.FetchPartition{{Partition: 0, Records: records}}}}}}
}
