package eventprojection

import (
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
)

func TestMapEventCoversLifecycleTerminalStates(t *testing.T) {
	envelope := &enginev1.EventEnvelope{
		Metadata: &enginev1.EventMetadata{
			EventId: "event-1", TenantId: "tenant-a", InstanceId: "instance-1",
			Sequence: 4, OccurredAtEpochMs: 100, WorkflowType: "approval",
			WorkflowVersion: "1", ConfigVersion: "config-v1", PolicyVersion: "policy-v1",
		},
		Event: &enginev1.EventEnvelope_WorkflowTerminatedForCompliance{
			WorkflowTerminatedForCompliance: &enginev1.WorkflowTerminatedForCompliance{},
		},
	}
	event, err := mapEvent(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != application.EventTerminatedForCompliance || event.Sequence != 4 {
		t.Fatalf("unexpected terminal mapping: %+v", event)
	}
}

func TestMapEventRejectsMissingWorkflowScope(t *testing.T) {
	_, err := mapEvent(&enginev1.EventEnvelope{
		Metadata: &enginev1.EventMetadata{EventId: "event-1", TenantId: "tenant-a", Sequence: 1},
		Event:    &enginev1.EventEnvelope_WorkflowCompleted{WorkflowCompleted: &enginev1.WorkflowCompleted{}},
	})
	if err == nil {
		t.Fatal("incomplete workflow scope must be rejected")
	}
}
