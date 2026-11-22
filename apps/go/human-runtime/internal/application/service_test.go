package application

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

type fakeStore struct {
	item             domain.WorkItem
	latestReadItem   *domain.WorkItem
	getWorkItemCalls int
	requested        bool
}

func (f *fakeStore) GetWorkItem(context.Context, string, string) (domain.WorkItem, error) {
	f.getWorkItemCalls++
	if f.getWorkItemCalls > 1 && f.latestReadItem != nil {
		return *f.latestReadItem, nil
	}
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
func (f *fakeStore) Delegate(_ context.Context, item domain.WorkItem, _, _, _, _ string) error {
	f.item = item
	return nil
}

type fakeBulkStore struct {
	*fakeStore
	operationID string
	digest      []byte
	receipts    []BulkItemReceipt
	audits      map[string]struct {
		action  string
		version int64
	}
	delegateCalls int
}

func (f *fakeBulkStore) BeginBulkOperation(_ context.Context, operation BulkOperation, initial []BulkItemReceipt) (string, []BulkItemReceipt, bool, error) {
	if f.operationID != "" {
		if !bytes.Equal(f.digest, operation.RequestDigest) {
			return "", nil, false, ErrIdempotencyConflict
		}
		return f.operationID, append([]BulkItemReceipt(nil), f.receipts...), true, nil
	}
	f.operationID, f.digest, f.receipts = operation.OperationID, append([]byte(nil), operation.RequestDigest...), append([]BulkItemReceipt(nil), initial...)
	return f.operationID, append([]BulkItemReceipt(nil), f.receipts...), false, nil
}

func (f *fakeBulkStore) SaveBulkItemReceipt(_ context.Context, _, operationID, workItemID string, receipt BulkItemReceipt, _ time.Time) error {
	if operationID != f.operationID {
		return ErrBulkRequest
	}
	for index := range f.receipts {
		if f.receipts[index].WorkItemID == workItemID {
			f.receipts[index] = receipt
			return nil
		}
	}
	return ErrBulkRequest
}

func (f *fakeBulkStore) FindBulkCommandAudit(_ context.Context, _, commandID string) (string, int64, bool, error) {
	audit, ok := f.audits[commandID]
	return audit.action, audit.version, ok, nil
}

func (f *fakeBulkStore) Delegate(ctx context.Context, item domain.WorkItem, commandID, _, _, _ string) error {
	f.delegateCalls++
	f.audits[commandID] = struct {
		action  string
		version int64
	}{action: "DELEGATED", version: item.Version}
	return f.fakeStore.Delegate(ctx, item, commandID, "", "", "")
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

var testPolicyProvider = RuntimePolicyProviderFunc(func(string) (RuntimePolicy, error) {
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
	policy := RuntimePolicyProviderFunc(func(string) (RuntimePolicy, error) {
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

func TestDelegateReportsConflictWhenAssignmentChangesDuringAuthorization(t *testing.T) {
	item := assignedItem()
	item.Version = 1
	item.Assignment = domain.Assignment{AssigneeID: "winner"}
	latest := item
	latest.Version = 2
	store := &fakeStore{item: item, latestReadItem: &latest}
	service, err := NewService(store, &recordingEngine{}, testPolicyProvider)
	if err != nil {
		t.Fatal(err)
	}
	err = service.Delegate(context.Background(), DelegateRequest{
		TenantID: "tenant-a", WorkItemID: item.ID, CommandID: "delegate-loser",
		ExpectedVersion: 1, Actor: ActorCredential{ActorID: "loser", OriginalSignedToken: []byte("signed")},
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected a stale assignment conflict, got %v", err)
	}
}

func TestBulkPartialReceiptReplaysWithoutRepeatingSuccessfulMutation(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", Status: domain.WorkItemActive, Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 4}
	store := &fakeBulkStore{fakeStore: &fakeStore{item: item}, audits: make(map[string]struct {
		action  string
		version int64
	})}
	service, err := NewService(store, &recordingEngine{}, testPolicyProvider)
	if err != nil {
		t.Fatal(err)
	}
	operation := BulkOperation{TenantID: "tenant-a", ActorID: "alice", OperationID: "operation-1", IdempotencyKey: "idem-1", Action: string(BulkDelegate), Mode: "PARTIAL", RequestDigest: bytes.Repeat([]byte{7}, 32), OccurredAt: time.Unix(100, 0).UTC()}
	items := []BulkItem{{WorkItemID: item.ID, ExpectedVersion: item.Version, Assignment: domain.Assignment{AssigneeID: "bob"}, Reason: "coverage"}}
	actor := ActorCredential{ActorID: "alice", OriginalSignedToken: []byte("signed")}
	operationID, receipts, duplicate, err := service.BulkMutate(context.Background(), operation, items, "correlation-1", actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate || operationID != operation.OperationID || len(receipts) != 1 || receipts[0].Status != "SUCCEEDED" || receipts[0].ResultingVersion != item.Version+1 || store.delegateCalls != 1 {
		t.Fatalf("first bulk attempt did not persist the successful item result: id=%s duplicate=%v receipts=%+v calls=%d", operationID, duplicate, receipts, store.delegateCalls)
	}
	operationID, receipts, duplicate, err = service.BulkMutate(context.Background(), operation, items, "correlation-1", actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate || operationID != operation.OperationID || len(receipts) != 1 || receipts[0].Status != "SUCCEEDED" || store.delegateCalls != 1 {
		t.Fatalf("replay repeated a successful mutation or lost its receipt: id=%s duplicate=%v receipts=%+v calls=%d", operationID, duplicate, receipts, store.delegateCalls)
	}
}

func assignedItem() domain.WorkItem {
	return domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1",
		NodeID: "review", Status: domain.WorkItemActive,
		Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}
}
