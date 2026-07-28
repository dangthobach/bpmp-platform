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
func (*fakeStore) CommitCaseCompletion(context.Context, CommittedCaseCompletion) error { return nil }
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

var testPolicyProvider = RuntimePolicyProviderFunc(func() (RuntimePolicy, error) {
	return RuntimePolicy{
		MaxAssignmentCandidates: 16,
		MaxDelegationDepth:      3,
		QueryDefaultPageSize:    50,
		QueryMaxPageSize:        200,
	}, nil
})

func (r *recordingEngine) CompleteUserTask(_ context.Context, command EngineCompleteCommand) error {
	r.command = command
	r.calls++
	return r.err
}

func TestCompleteRetriesSameDurableCommandAfterEngineFailure(t *testing.T) {
	store := &fakeStore{item: assignedItem()}
	engine := &recordingEngine{err: errors.New("engine unavailable")}
	service, _ := NewService(store, engine, testPolicyProvider)
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
	service, _ := NewService(store, engine, testPolicyProvider)
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
	service, _ := NewService(store, engine, testPolicyProvider)
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

func TestDynamicPolicyBoundsAssignmentClaimsAndDelegationDepth(t *testing.T) {
	policy := RuntimePolicyProviderFunc(func() (RuntimePolicy, error) {
		return RuntimePolicy{
			MaxAssignmentCandidates: 1,
			MaxDelegationDepth:      1,
			QueryDefaultPageSize:    25,
			QueryMaxPageSize:        100,
		}, nil
	})
	item := assignedItem()
	store := &fakeStore{item: item}
	service, err := NewService(store, &recordingEngine{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	request := DelegateRequest{
		TenantID: "tenant-a", WorkItemID: "work-1", CommandID: "delegate-1",
		ExpectedVersion: 1, Actor: ActorCredential{
			ActorID: "alice", OriginalSignedToken: []byte("signed"),
		},
		ActorGroups: map[string]struct{}{"group-a": {}, "group-b": {}},
		Assignment:  domain.Assignment{AssigneeID: "bob"},
		OccurredAt:  time.Unix(20, 0).UTC(),
	}
	if err = service.Delegate(context.Background(), request); !errors.Is(err, ErrPolicyLimit) {
		t.Fatalf("candidate claim limit was not enforced: %v", err)
	}
	request.ActorGroups = nil
	if err = service.Delegate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if store.item.DelegationDepth != 1 {
		t.Fatalf("delegation depth was not persisted in domain state: %d", store.item.DelegationDepth)
	}
	request.CommandID = "delegate-2"
	request.ExpectedVersion = store.item.Version
	request.Actor.ActorID = "bob"
	request.Assignment = domain.Assignment{AssigneeID: "carol"}
	if err = service.Delegate(context.Background(), request); !errors.Is(err, ErrPolicyLimit) {
		t.Fatalf("delegation depth limit was not enforced: %v", err)
	}
}

func assignedItem() domain.WorkItem {
	return domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1",
		NodeID: "review", Status: domain.WorkItemActive,
		Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}
}
