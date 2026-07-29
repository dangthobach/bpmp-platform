package eventstream

import (
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
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
