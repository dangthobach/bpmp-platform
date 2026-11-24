package engineevents

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type eventRecorder struct {
	event application.EngineEvent
	calls int
}

func (r *eventRecorder) ApplyEngineEvent(_ context.Context, event application.EngineEvent) (bool, error) {
	r.event = event
	r.calls++
	return true, nil
}

func TestHandlerMapsCommittedWorkflowEventWithoutPayload(t *testing.T) {
	tenantID, instanceID := uuid.New(), uuid.New()
	envelope := &enginev1.EventEnvelope{
		Metadata: &enginev1.EventMetadata{
			EventId: "engine-event-1", TenantId: tenantID.String(), InstanceId: instanceID.String(),
			Sequence: 3, WorkflowType: "credit-review", WorkflowVersion: "7",
			OccurredAtEpochMs: 1_800_000_000_000,
		},
		Event: &enginev1.EventEnvelope_WorkflowStarted{WorkflowStarted: &enginev1.WorkflowStarted{StartNodeId: "start"}},
	}
	raw, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	repository := &eventRecorder{}
	service, err := application.NewEngineEventService(repository)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.Handle(context.Background(), &kgo.Record{Value: raw}); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository calls = %d, want 1", repository.calls)
	}
	if repository.event.TenantID != tenantID || repository.event.InstanceID != instanceID.String() ||
		repository.event.EventID != "engine-event-1" || repository.event.WorkflowType != "credit-review" ||
		repository.event.WorkflowVersion != "7" || repository.event.Sequence != 3 ||
		repository.event.EventType != "workflow_started" || repository.event.NodeID != "start" ||
		!repository.event.OccurredAt.Equal(time.UnixMilli(1_800_000_000_000).UTC()) ||
		repository.event.PayloadDigest != sha256.Sum256(raw) {
		t.Fatalf("unexpected projected Engine event: %+v", repository.event)
	}
}

func TestHandlerRejectsInvalidAndOversizedRecords(t *testing.T) {
	repository := &eventRecorder{}
	service, _ := application.NewEngineEventService(repository)
	handler, _ := New(service)
	for _, record := range []*kgo.Record{nil, {Value: []byte("not protobuf")}, {Value: make([]byte, MaxEventBytes+1)}} {
		if err := handler.Handle(context.Background(), record); err == nil {
			t.Fatal("invalid record was accepted")
		}
	}
	if repository.calls != 0 {
		t.Fatalf("invalid events reached repository %d times", repository.calls)
	}
}
