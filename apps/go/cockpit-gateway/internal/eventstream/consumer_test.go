package eventstream

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	notificationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/notification/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

func TestCommittedUserTaskEventFansOutTypedTenantSignals(t *testing.T) {
	hub, err := subscription.New(subscription.Config{
		MaxSubscriptions: 4, BufferSize: 4, ReplaySize: 4, MaxReplayStreams: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		SignalWorkflowChanged, SignalWorkItemChanged, SignalAuditChanged,
	}
	values := make([]*subscription.Subscription, 0, len(names))
	for _, name := range names {
		value, subscribeErr := hub.Subscribe(subscription.Filter{
			TenantID: "tenant-a", Name: name,
		})
		if subscribeErr != nil {
			t.Fatal(subscribeErr)
		}
		defer value.Close()
		values = append(values, value)
	}
	envelope := &enginev1.EventEnvelope{
		Metadata: &enginev1.EventMetadata{
			EventId: "event-1", TenantId: "tenant-a", InstanceId: "instance-1",
			Sequence: 2, WorkflowType: "approval", WorkflowVersion: "1",
			OccurredAtEpochMs: uint64(time.Now().UnixMilli()),
		},
		Event: &enginev1.EventEnvelope_UserTaskActivated{
			UserTaskActivated: &enginev1.UserTaskActivated{NodeId: "approve"},
		},
	}
	raw, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{hub: hub}
	if err = consumer.handle(&kgo.Record{
		Topic:     "bpmp.engine.committed-events.v1.test",
		Partition: 2, Offset: 9, Value: raw,
	}); err != nil {
		t.Fatal(err)
	}
	for index, value := range values {
		select {
		case signal := <-value.Signals:
			if signal.Name != names[index] || signal.TenantID != "tenant-a" ||
				signal.Cursor != "bpmp.engine.committed-events.v1.test:2:9" {
				t.Fatalf("unexpected signal: %+v", signal)
			}
		default:
			t.Fatalf("signal %s was not delivered", names[index])
		}
	}
}

func TestMalformedCommittedEventFailsClosed(t *testing.T) {
	hub, err := subscription.New(subscription.Config{
		MaxSubscriptions: 1, BufferSize: 1, ReplaySize: 1, MaxReplayStreams: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{hub: hub}
	if err = consumer.handle(&kgo.Record{Value: []byte("not-protobuf")}); err == nil {
		t.Fatal("malformed event must fail")
	}
}

type stubKafkaClient struct {
	records   []*kgo.Record
	polled    bool
	committed []*kgo.Record
	cancel    context.CancelFunc
}

func (s *stubKafkaClient) PollRecords(_ context.Context, _ int) kgo.Fetches {
	if s.polled {
		s.cancel()
		return nil
	}
	s.polled = true
	return kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: s.records[0].Topic,
			Partitions: []kgo.FetchPartition{{
				Partition: s.records[0].Partition,
				Records:   s.records,
			}},
		}},
	}}
}

func (s *stubKafkaClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	s.committed = append(s.committed, records...)
	return nil
}

func TestInAppNotificationHintPublishesRecipientScopedSignalAndCommitsOffset(t *testing.T) {
	hub, err := subscription.New(subscription.Config{
		MaxSubscriptions: 4, BufferSize: 4, ReplaySize: 4, MaxReplayStreams: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	subA, err := hub.Subscribe(subscription.Filter{TenantID: "tenant-a", Name: SignalNotificationInApp, RecipientID: "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	defer subA.Close()
	subB, err := hub.Subscribe(subscription.Filter{TenantID: "tenant-a", Name: SignalNotificationInApp, RecipientID: "user-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer subB.Close()

	hint := &notificationv1.InAppNotificationHint{
		SchemaVersion:       1,
		EventId:             "hint-1",
		TenantId:            "tenant-a",
		RecipientSubjectRef: "user-a",
		DeliveryId:          "del-1",
		MessageId:           "msg-42",
		CategoryKey:         "workflow.approval",
		Sequence:            7,
		OccurredAtEpochMs:   time.Now().UnixMilli(),
	}
	raw, err := proto.Marshal(hint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	record := &kgo.Record{
		Topic:     "bpmp.notification.inapp-hints.v1.test",
		Partition: 1,
		Offset:    12,
		Value:     raw,
	}
	client := &stubKafkaClient{records: []*kgo.Record{record}, cancel: cancel}
	consumer, err := NewConsumer(
		client,
		hub,
		10,
		"bpmp.human.work-item-events.v1.test",
		WithInAppHintTopic("bpmp.notification.inapp-hints.v1.test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.committed) != 1 || client.committed[0] != record {
		t.Fatalf("expected manual offset commit after publish, got %d commits", len(client.committed))
	}
	select {
	case signal := <-subA.Signals:
		if signal.Name != SignalNotificationInApp || signal.TenantID != "tenant-a" ||
			signal.RecipientID != "user-a" || signal.Cursor != "bpmp.notification.inapp-hints.v1.test:1:12" {
			t.Fatalf("unexpected signal: %+v", signal)
		}
		var body inAppHintPayload
		if err = json.Unmarshal(signal.Payload, &body); err != nil {
			t.Fatal(err)
		}
		if body.Sequence != 7 || body.MessageID != "msg-42" || body.CategoryKey != "workflow.approval" {
			t.Fatalf("unexpected payload: %+v", body)
		}
	default:
		t.Fatal("user-a did not receive in-app notification hint")
	}
	select {
	case leaked := <-subB.Signals:
		t.Fatalf("user-b in same tenant must not receive user-a hint: %+v", leaked)
	default:
	}
}

func TestMalformedInAppHintFailsClosedWithoutCommit(t *testing.T) {
	hub, err := subscription.New(subscription.Config{
		MaxSubscriptions: 2, BufferSize: 2, ReplaySize: 2, MaxReplayStreams: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	record := &kgo.Record{
		Topic: "bpmp.notification.inapp-hints.v1.test",
		Value: []byte("not-protobuf"),
	}
	client := &stubKafkaClient{records: []*kgo.Record{record}, cancel: cancel}
	consumer, err := NewConsumer(
		client,
		hub,
		10,
		"bpmp.human.work-item-events.v1.test",
		WithInAppHintTopic("bpmp.notification.inapp-hints.v1.test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = consumer.Run(ctx); err == nil {
		t.Fatal("expected malformed hint to fail closed")
	}
	if len(client.committed) != 0 {
		t.Fatalf("expected zero commits on failure, got %d", len(client.committed))
	}
}
