package humangrpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type testQuery struct {
	item   domain.WorkItem
	audits []application.AuditRecord
	next   *application.AuditCursor
}

type assignmentPolicyQuery struct {
	testQuery
	called                        bool
	workflowType, workflowVersion string
	pageSize                      int
}

func (q *assignmentPolicyQuery) ListAssignmentPolicies(_ context.Context, tenantID, workflowType, workflowVersion string, pageSize int, _ *application.AssignmentPolicyCursor) ([]domain.AssignmentPolicy, *application.AssignmentPolicyCursor, error) {
	q.called = tenantID == "tenant-a"
	q.workflowType, q.workflowVersion, q.pageSize = workflowType, workflowVersion, pageSize
	return []domain.AssignmentPolicy{{TenantID: tenantID, Reference: "review-default", WorkflowType: workflowType, WorkflowVersion: workflowVersion, NodeID: "review", Assignment: domain.Assignment{CandidateGroup: "finance"}, ConfigVersion: "cfg-1", Version: 2}}, nil, nil
}

var testPolicy = application.RuntimePolicyProviderFunc(func(string) (application.RuntimePolicy, error) {
	return application.RuntimePolicy{
		MaxAssignmentCandidates: 16,
		MaxDelegationDepth:      3,
		QueryDefaultPageSize:    50,
		QueryMaxPageSize:        200,
	}, nil
})

func (q testQuery) GetWorkItem(context.Context, string, string) (domain.WorkItem, error) {
	return q.item, nil
}
func (q testQuery) ListWorkItems(context.Context, string, string, []string, application.WorkItemListScope, int, *application.PageCursor) ([]domain.WorkItem, *application.PageCursor, error) {
	return []domain.WorkItem{q.item}, nil, nil
}
func (testQuery) GetCase(context.Context, string, string) (application.CaseView, error) {
	return application.CaseView{}, errors.New("not found")
}
func (q testQuery) ListAuditRecords(context.Context, string, string, string, int, *application.AuditCursor) ([]application.AuditRecord, *application.AuditCursor, error) {
	return q.audits, q.next, nil
}
func (testQuery) ListAssignmentGroups(context.Context, string) ([]application.AssignmentGroup, error) {
	return nil, nil
}

type testVerifier struct {
	tenant string
	proof  []byte
	actor  application.ActorIdentity
	err    error
}

func (v *testVerifier) VerifyActor(_ context.Context, request application.ActorVerificationRequest) (application.ActorIdentity, error) {
	v.tenant = request.TenantID
	v.proof = append([]byte(nil), request.Credential.OriginalSignedToken...)
	return v.actor, v.err
}

type testStore struct{ item domain.WorkItem }

type bulkTestStore struct {
	*testStore
	operationID string
	digest      []byte
	receipts    []application.BulkItemReceipt
	audits      map[string]struct {
		action  string
		version int64
	}
	delegateCalls int
}

func (s *bulkTestStore) BeginBulkOperation(_ context.Context, operation application.BulkOperation, items []application.BulkItemReceipt) (string, []application.BulkItemReceipt, bool, error) {
	if s.operationID != "" {
		if string(s.digest) != string(operation.RequestDigest) {
			return "", nil, false, application.ErrIdempotencyConflict
		}
		return s.operationID, append([]application.BulkItemReceipt(nil), s.receipts...), true, nil
	}
	s.operationID, s.digest, s.receipts = operation.OperationID, append([]byte(nil), operation.RequestDigest...), append([]application.BulkItemReceipt(nil), items...)
	return s.operationID, append([]application.BulkItemReceipt(nil), s.receipts...), false, nil
}

func (s *bulkTestStore) SaveBulkItemReceipt(_ context.Context, _, operationID, workItemID string, receipt application.BulkItemReceipt, _ time.Time) error {
	if operationID != s.operationID {
		return application.ErrBulkRequest
	}
	for index := range s.receipts {
		if s.receipts[index].WorkItemID == workItemID {
			s.receipts[index] = receipt
			return nil
		}
	}
	return application.ErrBulkRequest
}

func (s *bulkTestStore) FindBulkCommandAudit(_ context.Context, _, commandID string) (string, int64, bool, error) {
	audit, ok := s.audits[commandID]
	return audit.action, audit.version, ok, nil
}

func (s *bulkTestStore) Delegate(ctx context.Context, item domain.WorkItem, commandID, correlationID, actorID, reason string) error {
	s.delegateCalls++
	s.audits[commandID] = struct {
		action  string
		version int64
	}{action: "DELEGATED", version: item.Version}
	return s.testStore.Delegate(ctx, item, commandID, correlationID, actorID, reason)
}

func (s *testStore) GetWorkItem(context.Context, string, string) (domain.WorkItem, error) {
	return s.item, nil
}
func (s *testStore) ProjectActivation(context.Context, domain.Activation) (domain.WorkItem, bool, error) {
	return s.item, false, nil
}
func (s *testStore) RequestCompletion(_ context.Context, item domain.WorkItem, _, _, _ string) error {
	s.item = item
	return nil
}
func (*testStore) CommitCompletion(context.Context, application.CommittedCompletion) error {
	return nil
}
func (*testStore) CommitCancellation(context.Context, application.CommittedCancellation) error {
	return nil
}
func (s *testStore) Delegate(_ context.Context, item domain.WorkItem, _, _, _, _ string) error {
	s.item = item
	return nil
}
func (*testStore) ProjectCase(context.Context, application.CommittedCase) (bool, error) {
	return false, nil
}
func (*testStore) CommitCaseTransition(context.Context, application.CommittedCaseTransition) error {
	return nil
}
func (*testStore) CommitCaseCompletion(context.Context, application.CommittedCaseCompletion) error {
	return nil
}
func (*testStore) TransitionCaseStage(context.Context, string, string, string, domain.PlanItemStatus, string, time.Time) error {
	return nil
}
func (*testStore) AchieveCaseMilestone(context.Context, string, string, string, string, time.Time) error {
	return nil
}

type testEngine struct {
	command application.EngineCompleteCommand
}

func (e *testEngine) CompleteUserTask(_ context.Context, command application.EngineCompleteCommand) error {
	e.command = command
	return nil
}

func TestGetWorkItemRejectsUnassignedActor(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", Status: domain.WorkItemActive, Assignment: domain.Assignment{AssigneeID: "alice"}}
	store := &testStore{item: item}
	engine := &testEngine{}
	service, _ := application.NewService(store, engine, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "mallory", Groups: map[string]struct{}{}}}
	server, _ := New(service, testQuery{item: item}, verifier, testPolicy, func() time.Time { return time.Unix(1, 0).UTC() })
	_, err := server.GetWorkItem(context.Background(), &humanv1.GetWorkItemRequest{TenantId: "tenant-a", WorkItemId: "work-1", ActorProof: originalJWT("signed-jwt")})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if verifier.tenant != "tenant-a" || string(verifier.proof) != "signed-jwt" {
		t.Fatalf("proof verification boundary lost scope: %#v", verifier)
	}
}

func TestListAssignmentPoliciesRequiresOwnerCapability(t *testing.T) {
	store := &testStore{}
	service, _ := application.NewService(store, &testEngine{}, testPolicy)
	query := &assignmentPolicyQuery{}
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "manager", Capabilities: map[string]struct{}{"human.assignment-policy.read": {}}}}
	server, _ := New(service, query, verifier, testPolicy, func() time.Time { return time.Unix(2, 0).UTC() })
	request := &humanv1.ListAssignmentPoliciesRequest{TenantId: "tenant-a", WorkflowType: "purchase", WorkflowVersion: "v1", PageSize: 15, ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte("signed")}}
	response, err := server.ListAssignmentPolicies(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !query.called || query.workflowType != "purchase" || query.workflowVersion != "v1" || query.pageSize != 15 || len(response.Policies) != 1 || response.Policies[0].CandidateGroup != "finance" {
		t.Fatalf("assignment policy query did not preserve scope: query=%+v response=%+v", query, response)
	}
	verifier.actor.Capabilities = map[string]struct{}{}
	if _, err = server.ListAssignmentPolicies(context.Background(), request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("missing assignment-policy capability was not denied: %v", err)
	}
}

func TestCompleteForwardsVerifiedOriginalProof(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1", NodeID: "review", Status: domain.WorkItemActive, Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}
	store := &testStore{item: item}
	engine := &testEngine{}
	service, _ := application.NewService(store, engine, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Groups: map[string]struct{}{}}}
	server, _ := New(service, testQuery{item: item}, verifier, testPolicy, func() time.Time { return time.Unix(2, 0).UTC() })
	_, err := server.CompleteWorkItem(context.Background(), &humanv1.CompleteWorkItemRequest{TenantId: "tenant-a", WorkItemId: "work-1", CommandId: "command-1", Decision: "approved", ExpectedVersion: 1, ActorProof: originalJWT("signed-jwt")})
	if err != nil {
		t.Fatal(err)
	}
	if engine.command.ActorID != "alice" || string(engine.command.OriginalToken) != "signed-jwt" || engine.command.Decision != "approved" {
		t.Fatalf("verified actor proof was not forwarded unchanged: %#v", engine.command)
	}
}

func TestMissingProofIsUnauthenticated(t *testing.T) {
	item := domain.WorkItem{Assignment: domain.Assignment{AssigneeID: "alice"}}
	store := &testStore{item: item}
	service, _ := application.NewService(store, &testEngine{}, testPolicy)
	server, _ := New(service, testQuery{item: item}, &testVerifier{}, testPolicy, time.Now)
	_, err := server.GetWorkItem(context.Background(), &humanv1.GetWorkItemRequest{TenantId: "tenant-a", WorkItemId: "work-1"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
}

func TestListWorkItemsRejectsCursorFromDifferentListScope(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1"}
	service, _ := application.NewService(&testStore{item: item}, &testEngine{}, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Groups: map[string]struct{}{}}}
	server, _ := New(service, testQuery{item: item}, verifier, testPolicy, time.Now)
	request := &humanv1.ListWorkItemsRequest{
		TenantId:   "tenant-a",
		Scope:      humanv1.WorkItemListScope_WORK_ITEM_LIST_SCOPE_ASSIGNED_TO_ME,
		PageToken:  encodeCursor(&application.PageCursor{UpdatedAt: time.Unix(1, 0).UTC(), WorkItemID: "work-0", Scope: application.WorkItemListScopeAll}),
		ActorProof: originalJWT("signed"),
	}
	if _, err := server.ListWorkItems(context.Background(), request); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected cursor scope mismatch to be rejected, got %v", err)
	}
}

func TestAuditQueryRequiresExplicitCapability(t *testing.T) {
	item := domain.WorkItem{Assignment: domain.Assignment{AssigneeID: "alice"}}
	store := &testStore{item: item}
	service, _ := application.NewService(store, &testEngine{}, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Groups: map[string]struct{}{}}}
	server, _ := New(service, testQuery{item: item}, verifier, testPolicy, time.Now)
	_, err := server.ListAuditRecords(context.Background(), &humanv1.ListAuditRecordsRequest{TenantId: "tenant-a", ActorProof: originalJWT("signed")})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

type testHistoryReader struct {
	response *enginev1.ReadHistoryPageResponse
}

func (r testHistoryReader) ReadHistoryPage(context.Context, string, string, uint64, uint32) (*enginev1.ReadHistoryPageResponse, error) {
	return r.response, nil
}

func TestWorkItemHistoryMergesEngineEventsAndHumanAuditInTimeOrder(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1", NodeID: "review"}
	query := testQuery{item: item, audits: []application.AuditRecord{{
		AuditID: "audit-1", WorkItemID: item.ID, ActorID: "alice", Action: "DELEGATED",
		OccurredAt: time.UnixMilli(100), CommandID: "command-a", CorrelationID: "correlation-a",
	}}}
	encoded, err := proto.Marshal(&enginev1.EventEnvelope{Metadata: &enginev1.EventMetadata{
		TenantId: item.TenantID, InstanceId: item.InstanceID, Sequence: 7,
		OccurredAtEpochMs: 200, ActorId: "alice", CausationCommandId: "command-b", CorrelationId: "correlation-b",
	}, Event: &enginev1.EventEnvelope_UserTaskCompleted{UserTaskCompleted: &enginev1.UserTaskCompleted{NodeId: "review"}}})
	if err != nil {
		t.Fatal(err)
	}
	service, _ := application.NewService(&testStore{item: item}, &testEngine{}, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Capabilities: map[string]struct{}{"audit.read": {}}}}
	server, _ := New(service, query, verifier, testPolicy, time.Now)
	server.SetHistoryReader(testHistoryReader{response: &enginev1.ReadHistoryPageResponse{
		Events: []*enginev1.StoredWorkflowEvent{{Sequence: 7, EventEnvelope: encoded}}, Watermark: 7,
	}})
	response, err := server.GetWorkItemHistory(context.Background(), &humanv1.GetWorkItemHistoryRequest{
		TenantId: item.TenantID, WorkItemId: item.ID, PageSize: 10, ActorProof: originalJWT("signed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEntries()) != 2 || response.GetEntries()[0].GetSource() != "HUMAN_RUNTIME" || response.GetEntries()[1].GetSource() != "ENGINE" {
		t.Fatalf("history sources were not merged in chronological order: %+v", response.GetEntries())
	}
	if response.GetEngineWatermark() != 7 || response.GetEntries()[1].GetEngineSequence() != 7 || response.GetEntries()[1].GetCommandId() != "command-b" {
		t.Fatalf("Engine event metadata was not preserved: %+v", response)
	}
}

func TestWorkItemHistoryRejectsCursorBoundToAnotherTask(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1"}
	service, _ := application.NewService(&testStore{item: item}, &testEngine{}, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Capabilities: map[string]struct{}{"audit.read": {}}}}
	server, _ := New(service, testQuery{item: item}, verifier, testPolicy, time.Now)
	server.SetHistoryReader(testHistoryReader{response: &enginev1.ReadHistoryPageResponse{}})
	otherTask, _ := json.Marshal(workItemHistoryCursor{TenantID: item.TenantID, WorkItemID: "work-other"})
	_, err := server.GetWorkItemHistory(context.Background(), &humanv1.GetWorkItemHistoryRequest{
		TenantId: item.TenantID, WorkItemId: item.ID, PageToken: base64.RawURLEncoding.EncodeToString(otherTask), ActorProof: originalJWT("signed"),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("cross-task history cursor was not rejected: %v", err)
	}
}

func TestBulkMutationPersistsAndReplaysSuccessfulItemReceipt(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", Status: domain.WorkItemActive, Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}
	store := &bulkTestStore{testStore: &testStore{item: item}, audits: make(map[string]struct {
		action  string
		version int64
	})}
	service, _ := application.NewService(store, &testEngine{}, testPolicy)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Capabilities: map[string]struct{}{"human.task.bulk.delegate": {}}}}
	server, _ := New(service, testQuery{item: item}, verifier, testPolicy, func() time.Time { return time.Unix(10, 0).UTC() })
	request := &humanv1.BulkMutateWorkItemsRequest{
		TenantId: item.TenantID, OperationId: "operation-1", IdempotencyKey: "idem-1", CorrelationId: "correlation-1",
		Mode:       humanv1.WorkItemBulkMode_WORK_ITEM_BULK_MODE_PARTIAL,
		Action:     humanv1.WorkItemBulkAction_WORK_ITEM_BULK_ACTION_DELEGATE,
		ActorProof: originalJWT("signed"),
		Items:      []*humanv1.BulkWorkItemMutation{{WorkItemId: item.ID, ExpectedVersion: 1, AssigneeId: "bob", Reason: "coverage"}},
	}
	response, err := server.BulkMutateWorkItems(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStatus() != "SUCCEEDED" || response.GetOperationId() != "operation-1" || len(response.GetReceipts()) != 1 || response.GetReceipts()[0].GetStatus() != "SUCCEEDED" || store.delegateCalls != 1 {
		t.Fatalf("unexpected partial bulk response: %+v calls=%d", response, store.delegateCalls)
	}
	response, err = server.BulkMutateWorkItems(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetDuplicate() || response.GetReceipts()[0].GetStatus() != "SUCCEEDED" || store.delegateCalls != 1 {
		t.Fatalf("bulk replay did not return the durable item receipt: %+v calls=%d", response, store.delegateCalls)
	}

	atomicRequest := proto.Clone(request).(*humanv1.BulkMutateWorkItemsRequest)
	atomicRequest.Mode = humanv1.WorkItemBulkMode_WORK_ITEM_BULK_MODE_ATOMIC
	if _, err = server.BulkMutateWorkItems(context.Background(), atomicRequest); status.Code(err) != codes.FailedPrecondition || store.delegateCalls != 1 {
		t.Fatalf("unsupported atomic mode was not rejected before effects: err=%v calls=%d", err, store.delegateCalls)
	}
}

func originalJWT(value string) *authv1.ActorProof {
	return &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(value)}
}
