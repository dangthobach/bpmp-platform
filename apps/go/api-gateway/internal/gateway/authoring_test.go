package gateway

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
)

type recordingAuthoring struct {
	authoringv1.AuthoringServiceClient
	create *authoringv1.CreateProcessModelRequest
	save   *authoringv1.SaveProcessModelDraftRequest
}

func (c *recordingAuthoring) CreateProcessModel(_ context.Context, request *authoringv1.CreateProcessModelRequest, _ ...grpc.CallOption) (*authoringv1.CreateProcessModelResponse, error) {
	c.create = request
	return &authoringv1.CreateProcessModelResponse{Model: &authoringv1.ProcessModel{Id: "74636a01-8143-4a03-b3fd-20cbbbc42bf7", TenantId: request.TenantId, ModelKey: request.ModelKey, Name: request.Name}}, nil
}

func (c *recordingAuthoring) SaveProcessModelDraft(_ context.Context, request *authoringv1.SaveProcessModelDraftRequest, _ ...grpc.CallOption) (*authoringv1.SaveProcessModelDraftResponse, error) {
	c.save = request
	return &authoringv1.SaveProcessModelDraftResponse{Draft: &authoringv1.ProcessModelDraft{ProcessModelId: request.ProcessModelId, Revision: request.ExpectedRevision + 1, BpmnXml: request.BpmnXml}}, nil
}

func TestAuthoringRoutesPassAuthenticatedScopeAndIdempotency(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000, 0).UTC()
	claims := jwt.MapClaims{"iss": "issuer", "sub": "actor-1", "aud": []string{"gateway"}, "exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix(), "tenant_id": "tenant-a", "capabilities": []string{"authoring.createProcessModel", "authoring.saveProcessModelDraft"}}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	authoring := &recordingAuthoring{}
	handler, err := NewHandler(&recordingEngine{}, &recordingHuman{}, testVerifier(t, public), &workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute}, newModelRateLimiter(10, time.Minute), staticPolicyProvider{}, WithAuthoring(authoring))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	newRequest := func(method, path, body string) *http.Request {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+raw)
		request.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
		request.Header.Set("X-Correlation-ID", "correlation-1")
		request.Header.Set("X-Command-ID", "command-1")
		request.Header.Set("Idempotency-Key", "idem-1")
		request.Header.Set("Content-Type", "application/json")
		return request
	}
	createResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(createResponse, newRequest(http.MethodPost, "/v1/authoring/process-models", `{"model_key":"invoice","name":"Invoice","initial_bpmn_xml":"<definitions/>"}`))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", createResponse.Code, createResponse.Body.String())
	}
	if authoring.create == nil || authoring.create.TenantId != "tenant-a" || authoring.create.ActorProof == nil || authoring.create.CommandId != "command-1" || authoring.create.IdempotencyKey != "idem-1" || authoring.create.ModelKey != "invoice" {
		t.Fatalf("Gateway did not bind authenticated create context: %+v", authoring.create)
	}
	saveResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(saveResponse, newRequest(http.MethodPut, "/v1/authoring/process-models/74636a01-8143-4a03-b3fd-20cbbbc42bf7/draft", `{"expected_revision":"1","bpmn_xml":"<definitions/>"}`))
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("save status %d: %s", saveResponse.Code, saveResponse.Body.String())
	}
	if authoring.save == nil || authoring.save.TenantId != "tenant-a" || authoring.save.ProcessModelId != "74636a01-8143-4a03-b3fd-20cbbbc42bf7" || authoring.save.ExpectedRevision != 1 || authoring.save.ActorProof == nil || authoring.save.IdempotencyKey != "idem-1" {
		t.Fatalf("Gateway did not bind authenticated save context: %+v", authoring.save)
	}
}
