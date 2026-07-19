package humangrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testQuery struct{ item domain.WorkItem }

func (q testQuery) GetWorkItem(context.Context, string, string) (domain.WorkItem, error) {
	return q.item, nil
}
func (q testQuery) ListWorkItems(context.Context, string, string, []string, int, *application.PageCursor) ([]domain.WorkItem, *application.PageCursor, error) {
	return []domain.WorkItem{q.item}, nil, nil
}
func (testQuery) GetCase(context.Context, string, string) (application.CaseView, error) {
	return application.CaseView{}, errors.New("not found")
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
func (s *testStore) Delegate(_ context.Context, item domain.WorkItem, _, _, _ string) error {
	s.item = item
	return nil
}
func (*testStore) ProjectCase(context.Context, application.CommittedCase) (bool, error) {
	return false, nil
}
func (*testStore) CommitCaseTransition(context.Context, application.CommittedCaseTransition) error {
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
	service, _ := application.NewService(store, engine)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "mallory", Groups: map[string]struct{}{}}}
	server, _ := New(service, testQuery{item: item}, verifier, func() time.Time { return time.Unix(1, 0).UTC() })
	_, err := server.GetWorkItem(context.Background(), &humanv1.GetWorkItemRequest{TenantId: "tenant-a", WorkItemId: "work-1", ActorProof: originalJWT("signed-jwt")})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if verifier.tenant != "tenant-a" || string(verifier.proof) != "signed-jwt" {
		t.Fatalf("proof verification boundary lost scope: %#v", verifier)
	}
}

func TestCompleteForwardsVerifiedOriginalProof(t *testing.T) {
	item := domain.WorkItem{TenantID: "tenant-a", ID: "work-1", InstanceID: "instance-1", NodeID: "review", Status: domain.WorkItemActive, Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}
	store := &testStore{item: item}
	engine := &testEngine{}
	service, _ := application.NewService(store, engine)
	verifier := &testVerifier{actor: application.ActorIdentity{ActorID: "alice", Groups: map[string]struct{}{}}}
	server, _ := New(service, testQuery{item: item}, verifier, func() time.Time { return time.Unix(2, 0).UTC() })
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
	service, _ := application.NewService(store, &testEngine{})
	server, _ := New(service, testQuery{item: item}, &testVerifier{}, time.Now)
	_, err := server.GetWorkItem(context.Background(), &humanv1.GetWorkItemRequest{TenantId: "tenant-a", WorkItemId: "work-1"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
}

func originalJWT(value string) *authv1.ActorProof {
	return &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(value)}
}
