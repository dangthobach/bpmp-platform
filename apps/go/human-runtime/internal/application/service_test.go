package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

type fakeStore struct {
	item      domain.WorkItem
	requested bool
}

func (f *fakeStore) GetWorkItem(context.Context, string, string) (domain.WorkItem, error) {
	return f.item, nil
}
func (f *fakeStore) ProjectActivation(context.Context, domain.Activation) (domain.WorkItem, bool, error) {
	return f.item, false, nil
}
func (f *fakeStore) RequestCompletion(_ context.Context, item domain.WorkItem, _, _, _ string) error {
	f.item = item
	f.requested = true
	return nil
}
func (*fakeStore) CommitCompletion(context.Context, CommittedCompletion) error {
	return nil
}
func (*fakeStore) CommitCancellation(context.Context, CommittedCancellation) error { return nil }
func (f *fakeStore) Delegate(_ context.Context, item domain.WorkItem, _, _, _ string) error {
	f.item = item
	return nil
}
func (*fakeStore) ProjectCase(context.Context, CommittedCase) (bool, error)            { return false, nil }
func (*fakeStore) CommitCaseTransition(context.Context, CommittedCaseTransition) error { return nil }
func (*fakeStore) TransitionCaseStage(context.Context, string, string, string, domain.PlanItemStatus, string, time.Time) error {
	return nil
}
func (*fakeStore) AchieveCaseMilestone(context.Context, string, string, string, string, time.Time) error {
	return nil
}

type recordingEngine struct {
	command EngineCompleteCommand
	calls   int
	err     error
}

func (r *recordingEngine) CompleteUserTask(_ context.Context, command EngineCompleteCommand) error {
	r.command = command
	r.calls++
	return r.err
}

func TestCompleteRetriesSameDurableCommandAfterEngineFailure(t *testing.T) {
	store := &fakeStore{item: assignedItem()}
	engine := &recordingEngine{err: errors.New("engine unavailable")}
	service, _ := NewService(store, engine)
	request := CompleteRequest{
		TenantID: "tenant-a", WorkItemID: "work-1", CommandID: "command-1",
		Decision: "approved", ExpectedVersion: 1,
		Actor:      ActorCredential{ActorID: "alice", OriginalSignedToken: []byte("signed")},
		OccurredAt: time.Unix(10, 0).UTC(),
	}
	if err := service.Complete(context.Background(), request); err == nil {
		t.Fatal("expected first engine call to fail")
	}
	if store.item.Status != domain.WorkItemCompletionRequested || store.item.CompletionCommandID != "command-1" {
		t.Fatalf("completion intent was not durable: %#v", store.item)
	}
	engine.err = nil
	if err := service.Complete(context.Background(), request); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if engine.calls != 2 {
		t.Fatalf("expected engine retry, got %d calls", engine.calls)
	}
	request.Decision = "rejected"
	if err := service.Complete(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestWorkloadCannotReplaceMissingActorProof(t *testing.T) {
	store := &fakeStore{item: assignedItem()}
	engine := &recordingEngine{}
	service, _ := NewService(store, engine)
	err := service.Complete(context.Background(), CompleteRequest{
		TenantID: "tenant-a", WorkItemID: "work-1", ExpectedVersion: 1,
		Actor: ActorCredential{ActorID: "alice"}, Decision: "approved", OccurredAt: time.Now(),
	})
	if !errors.Is(err, ErrActorProof) || store.requested || engine.calls != 0 {
		t.Fatalf("missing actor proof changed state: err=%v requested=%v calls=%d", err, store.requested, engine.calls)
	}
}

func TestCompleteForwardsOriginalActorTokenUnchanged(t *testing.T) {
	store := &fakeStore{item: assignedItem()}
	engine := &recordingEngine{}
	service, _ := NewService(store, engine)
	token := []byte("signed.actor.jwt")
	err := service.Complete(context.Background(), CompleteRequest{
		TenantID: "tenant-a", WorkItemID: "work-1", CommandID: "command-1",
		CorrelationID: "correlation-1", Decision: "approved", ExpectedVersion: 1,
		Actor: ActorCredential{ActorID: "alice", OriginalSignedToken: token}, OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(engine.command.OriginalToken) != string(token) || engine.command.ActorID != "alice" {
		t.Fatalf("actor credential was not preserved: %#v", engine.command)
	}
	if store.item.Status != domain.WorkItemCompletionRequested {
		t.Fatalf("work item finalized before committed event: %s", store.item.Status)
	}
}

func assignedItem() domain.WorkItem {
	return domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1",
		NodeID: "review", Status: domain.WorkItemActive,
		Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}
}
