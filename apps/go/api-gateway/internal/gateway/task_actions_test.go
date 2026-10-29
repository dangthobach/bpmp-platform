package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type mockHumanTaskService struct {
	humanv1.HumanRuntimeServiceClient
	lastDelegateReq *humanv1.DelegateWorkItemRequest
	lastCompleteReq *humanv1.CompleteWorkItemRequest
	metadata        metadata.MD
}

func (m *mockHumanTaskService) DelegateWorkItem(ctx context.Context, req *humanv1.DelegateWorkItemRequest, _ ...grpc.CallOption) (*humanv1.DelegateWorkItemResponse, error) {
	m.lastDelegateReq = req
	m.metadata, _ = metadata.FromOutgoingContext(ctx)
	return &humanv1.DelegateWorkItemResponse{
		CommandId:       req.CommandId,
		WorkItemVersion: req.ExpectedVersion + 1,
	}, nil
}

func (m *mockHumanTaskService) CompleteWorkItem(ctx context.Context, req *humanv1.CompleteWorkItemRequest, _ ...grpc.CallOption) (*humanv1.CompleteWorkItemResponse, error) {
	m.lastCompleteReq = req
	m.metadata, _ = metadata.FromOutgoingContext(ctx)
	return &humanv1.CompleteWorkItemResponse{
		CommandId:       req.CommandId,
		WorkItemVersion: req.ExpectedVersion + 1,
	}, nil
}

func testAuthToken(t *testing.T, private ed25519.PrivateKey, actorID, tenantID string, now time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":       "issuer",
		"sub":       actorID,
		"aud":       []string{"gateway"},
		"exp":       now.Add(time.Hour).Unix(),
		"iat":       now.Add(-time.Minute).Unix(),
		"tenant_id": tenantID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func setupTaskHandler(t *testing.T, human *mockHumanTaskService, now time.Time) (*Handler, ed25519.PrivateKey) {
	t.Helper()
	public, private, _ := ed25519.GenerateKey(nil)
	handler, err := NewHandler(
		&recordingEngine{}, human, testVerifier(t, public),
		&workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute},
		newModelRateLimiter(10, time.Minute), staticPolicyProvider{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	return handler, private
}

func TestClaimWorkItemBFF(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	mockHuman := &mockHumanTaskService{}
	handler, privateKey := setupTaskHandler(t, mockHuman, now)
	token := testAuthToken(t, privateKey, "alice-officer", "tenant-alpha", now)

	payload, _ := json.Marshal(map[string]any{"expected_version": 2})
	request := httptest.NewRequest(http.MethodPost, "/v1/work-items/task-99/claim", bytes.NewReader(payload))
	request.SetPathValue("workItemID", "task-99")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-alpha")
	request.Header.Set("X-Correlation-ID", "corr-claim-1")
	request.Header.Set("X-Command-ID", "cmd-claim-1")
	request.Header.Set("Idempotency-Key", "idem-claim-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.claimWorkItem(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", response.Code, response.Body.String())
	}
	if mockHuman.lastDelegateReq == nil {
		t.Fatal("expected delegate request to be received by Human service")
	}
	// BFF must automatically populate AssigneeId with actor's identity from authenticated token
	if mockHuman.lastDelegateReq.AssigneeId != "alice-officer" {
		t.Errorf("expected AssigneeId alice-officer, got %s", mockHuman.lastDelegateReq.AssigneeId)
	}
	if mockHuman.lastDelegateReq.CandidateGroup != "" {
		t.Errorf("expected empty CandidateGroup on claim, got %s", mockHuman.lastDelegateReq.CandidateGroup)
	}
	if mockHuman.lastDelegateReq.WorkItemId != "task-99" {
		t.Errorf("expected WorkItemId task-99, got %s", mockHuman.lastDelegateReq.WorkItemId)
	}
	if mockHuman.lastDelegateReq.ExpectedVersion != 2 {
		t.Errorf("expected ExpectedVersion 2, got %d", mockHuman.lastDelegateReq.ExpectedVersion)
	}
	if mockHuman.lastDelegateReq.IdempotencyKey != "idem-claim-1" {
		t.Errorf("expected IdempotencyKey idem-claim-1, got %s", mockHuman.lastDelegateReq.IdempotencyKey)
	}
}

func TestUnclaimWorkItemBFF(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	mockHuman := &mockHumanTaskService{}
	handler, privateKey := setupTaskHandler(t, mockHuman, now)
	token := testAuthToken(t, privateKey, "alice-officer", "tenant-alpha", now)

	payload, _ := json.Marshal(map[string]any{
		"expected_version": 3,
		"candidate_group":  "credit-reviewers",
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/work-items/task-99/unclaim", bytes.NewReader(payload))
	request.SetPathValue("workItemID", "task-99")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-alpha")
	request.Header.Set("X-Correlation-ID", "corr-unclaim-1")
	request.Header.Set("X-Command-ID", "cmd-unclaim-1")
	request.Header.Set("Idempotency-Key", "idem-unclaim-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.unclaimWorkItem(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", response.Code, response.Body.String())
	}
	if mockHuman.lastDelegateReq == nil {
		t.Fatal("expected delegate request to be received by Human service")
	}
	if mockHuman.lastDelegateReq.AssigneeId != "" {
		t.Errorf("expected empty AssigneeId on unclaim, got %s", mockHuman.lastDelegateReq.AssigneeId)
	}
	if mockHuman.lastDelegateReq.CandidateGroup != "credit-reviewers" {
		t.Errorf("expected CandidateGroup credit-reviewers, got %s", mockHuman.lastDelegateReq.CandidateGroup)
	}
}

func TestClaimWorkItemRejectsEmptyBodyOrMalformedID(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	mockHuman := &mockHumanTaskService{}
	handler, privateKey := setupTaskHandler(t, mockHuman, now)
	token := testAuthToken(t, privateKey, "alice", "tenant-alpha", now)

	// Malformed ID
	request := httptest.NewRequest(http.MethodPost, "/v1/work-items/invalid%20id/claim", bytes.NewReader([]byte(`{"expected_version":1}`)))
	request.SetPathValue("workItemID", "invalid id")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-alpha")
	response := httptest.NewRecorder()
	handler.claimWorkItem(response, request)
	if response.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed work item ID, got %d", response.Code)
	}
}

func TestCompleteWorkItemBFF(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	mockHuman := &mockHumanTaskService{}
	handler, privateKey := setupTaskHandler(t, mockHuman, now)
	token := testAuthToken(t, privateKey, "alice-officer", "tenant-alpha", now)

	payload, _ := json.Marshal(map[string]any{
		"decision":         "APPROVED",
		"expected_version": 4,
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/work-items/task-100/complete", bytes.NewReader(payload))
	request.SetPathValue("workItemID", "task-100")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-alpha")
	request.Header.Set("X-Correlation-ID", "corr-comp-1")
	request.Header.Set("X-Command-ID", "cmd-comp-1")
	request.Header.Set("Idempotency-Key", "idem-comp-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.completeWorkItem(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", response.Code, response.Body.String())
	}
	if mockHuman.lastCompleteReq == nil {
		t.Fatal("expected complete request to be received by Human service")
	}
	if mockHuman.lastCompleteReq.Decision != "APPROVED" {
		t.Errorf("expected Decision APPROVED, got %s", mockHuman.lastCompleteReq.Decision)
	}
	if mockHuman.lastCompleteReq.ExpectedVersion != 4 {
		t.Errorf("expected ExpectedVersion 4, got %d", mockHuman.lastCompleteReq.ExpectedVersion)
	}
}

func TestDelegateWorkItemBFF(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	mockHuman := &mockHumanTaskService{}
	handler, privateKey := setupTaskHandler(t, mockHuman, now)
	token := testAuthToken(t, privateKey, "alice-officer", "tenant-alpha", now)

	payload, _ := json.Marshal(map[string]any{
		"assignee_id":      "bob-specialist",
		"expected_version": 5,
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/work-items/task-101/delegate", bytes.NewReader(payload))
	request.SetPathValue("workItemID", "task-101")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-alpha")
	request.Header.Set("X-Correlation-ID", "corr-del-1")
	request.Header.Set("X-Command-ID", "cmd-del-1")
	request.Header.Set("Idempotency-Key", "idem-del-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.delegateWorkItem(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", response.Code, response.Body.String())
	}
	if mockHuman.lastDelegateReq == nil {
		t.Fatal("expected delegate request to be received by Human service")
	}
	if mockHuman.lastDelegateReq.AssigneeId != "bob-specialist" {
		t.Errorf("expected AssigneeId bob-specialist, got %s", mockHuman.lastDelegateReq.AssigneeId)
	}
}
