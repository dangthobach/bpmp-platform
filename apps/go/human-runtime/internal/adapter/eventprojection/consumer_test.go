package eventprojection

import (
	"context"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"google.golang.org/protobuf/proto"
)

type projectionStore struct {
	activation         domain.Activation
	completionDecision string
	completion         application.CommittedCompletion
	caseCompletion     application.CommittedCaseCompletion
}

func (p *projectionStore) GetWorkItem(context.Context, string, string) (domain.WorkItem, error) {
	return domain.WorkItem{}, nil
}
func (p *projectionStore) ProjectActivation(_ context.Context, activation domain.Activation) (domain.WorkItem, bool, error) {
	p.activation = activation
	return domain.WorkItem{}, false, nil
}
func (*projectionStore) RequestCompletion(context.Context, domain.WorkItem, string, string, string) error {
	return nil
}
func (p *projectionStore) CommitCompletion(_ context.Context, event application.CommittedCompletion) error {
	p.completionDecision = event.Decision
	p.completion = event
	return nil
}
func (*projectionStore) CommitCancellation(context.Context, application.CommittedCancellation) error {
	return nil
}
func (*projectionStore) Delegate(context.Context, domain.WorkItem, string, string, string) error {
	return nil
}
func (*projectionStore) ProjectCase(context.Context, application.CommittedCase) (bool, error) {
	return false, nil
}
func (*projectionStore) CommitCaseTransition(context.Context, application.CommittedCaseTransition) error {
	return nil
}
func (p *projectionStore) CommitCaseCompletion(_ context.Context, event application.CommittedCaseCompletion) error {
	p.caseCompletion = event
	return nil
}
func (*projectionStore) TransitionCaseStage(context.Context, string, string, string, domain.PlanItemStatus, string, time.Time) error {
	return nil
}
func (*projectionStore) AchieveCaseMilestone(context.Context, string, string, string, string, time.Time) error {
	return nil
}

type noEngine struct{}

func (noEngine) CompleteUserTask(context.Context, application.EngineCompleteCommand) error {
	return nil
}

func TestActivationUsesCommittedMetadata(t *testing.T) {
	store := &projectionStore{}
	service, _ := application.NewService(store, noEngine{})
	consumer, _ := New(service)
	envelope := &enginev1.EventEnvelope{Metadata: &enginev1.EventMetadata{EventId: "event-1", TenantId: "tenant-a", InstanceId: "instance-1", WorkflowType: "approval", WorkflowVersion: "1", Sequence: 7, OccurredAtEpochMs: 123}, Event: &enginev1.EventEnvelope_UserTaskActivated{UserTaskActivated: &enginev1.UserTaskActivated{NodeId: "review", TaskType: "review", AssignmentPolicyRef: "reviewers"}}}
	payload, _ := proto.Marshal(envelope)
	if err := consumer.Handle(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if store.activation.TenantID != "tenant-a" || store.activation.EventID != "event-1" || store.activation.Sequence != 7 {
		t.Fatalf("metadata lost: %#v", store.activation)
	}
}

func TestCaseCompletionUsesAuthoritativeCommittedEvent(t *testing.T) {
	store := &projectionStore{}
	service, _ := application.NewService(store, noEngine{})
	consumer, _ := New(service)
	envelope := &enginev1.EventEnvelope{
		Metadata: &enginev1.EventMetadata{EventId: "case-event-3", TenantId: "tenant-a", InstanceId: "case-1", Sequence: 3, OccurredAtEpochMs: 456},
		Event:    &enginev1.EventEnvelope_CaseCompleted{CaseCompleted: &enginev1.CaseCompleted{CaseId: "case-1"}},
	}
	payload, _ := proto.Marshal(envelope)
	if err := consumer.Handle(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if store.caseCompletion.CaseID != "case-1" || store.caseCompletion.Sequence != 3 {
		t.Fatalf("case completion metadata lost: %#v", store.caseCompletion)
	}
}
