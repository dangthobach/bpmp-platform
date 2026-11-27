package engineadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	configv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

type stubAuthoringClient struct {
	authoringv1.AuthoringServiceClient
	deployments []*authoringv1.RuntimeDeployment
	version     *authoringv1.ProcessModelVersion
	listErr     error
	versionErr  error
}

func (s *stubAuthoringClient) ListRuntimeDeployments(_ context.Context, _ *authoringv1.ListRuntimeDeploymentsRequest, _ ...grpc.CallOption) (*authoringv1.ListRuntimeDeploymentsResponse, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return &authoringv1.ListRuntimeDeploymentsResponse{Deployments: s.deployments}, nil
}

func (s *stubAuthoringClient) GetProcessModelVersion(_ context.Context, _ *authoringv1.GetProcessModelVersionRequest, _ ...grpc.CallOption) (*authoringv1.GetProcessModelVersionResponse, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	return &authoringv1.GetProcessModelVersionResponse{Version: s.version}, nil
}

type stubConfigClient struct {
	configv1.ConfigurationResolverServiceClient
	snapshot *configv1.ResolvedConfigurationSnapshot
	err      error
}

func (s *stubConfigClient) ResolveConfiguration(_ context.Context, _ *configv1.ResolveConfigurationRequest, _ ...grpc.CallOption) (*configv1.ResolveConfigurationResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &configv1.ResolveConfigurationResponse{Snapshot: s.snapshot}, nil
}

func TestOwnerAPIMetadataProviderResolvesAuthoritativeStartNodeAndKeyScope(t *testing.T) {
	tenant := uuid.New()
	job := application.StartJob{
		TenantID:        tenant,
		CaseID:          uuid.New(),
		CommandID:       uuid.New(),
		WorkflowType:    "loan-approval",
		WorkflowVersion: "2",
	}
	authoring := &stubAuthoringClient{
		deployments: []*authoringv1.RuntimeDeployment{
			{
				TenantId:              tenant.String(),
				ProcessModelId:        "pm-1",
				ProcessModelVersionId: "pmv-1",
				TargetEnvironment:     "prod",
				WorkflowType:          "loan-approval",
				WorkflowVersion:       "2",
				Status:                "ROLLED_BACK",
			},
			{
				TenantId:              tenant.String(),
				ProcessModelId:        "pm-1",
				ProcessModelVersionId: "pmv-2",
				TargetEnvironment:     "prod",
				WorkflowType:          "loan-approval",
				WorkflowVersion:       "2",
				Status:                "ACTIVE",
			},
		},
		version: &authoringv1.ProcessModelVersion{
			Id:             "pmv-2",
			TenantId:       tenant.String(),
			ProcessModelId: "pm-1",
			BpmnXml: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
				<bpmn:process id="loan-approval"><bpmn:startEvent id="StartEvent_Loan"/></bpmn:process>
			</bpmn:definitions>`,
		},
	}
	cfg := &stubConfigClient{
		snapshot: &configv1.ResolvedConfigurationSnapshot{
			Engine: &configv1.EnginePolicy{EventPayloadKeyScope: "tenant-loan/operational-v2"},
		},
	}
	proofFn := func(context.Context, application.StartJob) (*authv1.ActorProof, error) {
		return &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT, SignedProof: []byte("proof")}, nil
	}

	provider, err := NewOwnerAPIMetadataProvider(authoring, cfg, "bpmp-platform", "prod", proofFn)
	if err != nil {
		t.Fatal(err)
	}
	startNode, keyScope, err := provider.ResolveStartMetadata(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if startNode != "StartEvent_Loan" || keyScope != "tenant-loan/operational-v2" {
		t.Fatalf("unexpected resolved metadata: start=%q scope=%q", startNode, keyScope)
	}

	// Ambiguous multiple startEvent nodes must fail closed.
	authoring.version.BpmnXml = `<definitions><process><startEvent id="s1"/><startEvent id="s2"/></process></definitions>`
	if _, _, err = provider.ResolveStartMetadata(context.Background(), job); err == nil {
		t.Fatal("expected error for multiple startEvent nodes")
	}

	// Missing event_payload_key_scope must fail closed.
	authoring.version.BpmnXml = `<definitions><process><startEvent id="s1"/></process></definitions>`
	cfg.snapshot.Engine.EventPayloadKeyScope = ""
	if _, _, err = provider.ResolveStartMetadata(context.Background(), job); err == nil {
		t.Fatal("expected error for empty encryption key scope")
	}
}

func TestHTTPMetadataProviderVerifiesServiceJWTAndScope(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	var responseStatus = http.StatusOK
	var responseTenant = tenant.String()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflow-metadata" {
			http.NotFound(w, r)
			return
		}
		authHeader := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parsed, parseErr := jwt.Parse(authHeader, func(token *jwt.Token) (any, error) {
			return public, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}), jwt.WithAudience("workflow-metadata-api"))
		if parseErr != nil || !parsed.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if responseStatus != http.StatusOK {
			w.WriteHeader(responseStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tenant_id":            responseTenant,
			"workflow_type":        r.URL.Query().Get("workflow_type"),
			"workflow_version":     r.URL.Query().Get("workflow_version"),
			"start_node_id":        "StartEvent_API",
			"encryption_key_scope": "tenant/api-scope",
		})
	}))
	defer server.Close()

	provider, err := NewHTTPMetadataProvider(server.Client(), server.URL, "workflow-metadata-api", "case-service", "case-key-1", private, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	job := application.StartJob{TenantID: tenant, CaseID: uuid.New(), CommandID: uuid.New(), WorkflowType: "loan", WorkflowVersion: "1", CorrelationID: "corr-1"}
	startNode, keyScope, err := provider.ResolveStartMetadata(context.Background(), job)
	if err != nil || startNode != "StartEvent_API" || keyScope != "tenant/api-scope" {
		t.Fatalf("HTTP metadata resolve failed: start=%q scope=%q err=%v", startNode, keyScope, err)
	}

	// Mismatched tenant in response must fail closed.
	responseTenant = uuid.NewString()
	if _, _, err = provider.ResolveStartMetadata(context.Background(), job); err == nil {
		t.Fatal("expected error for mismatched tenant in metadata response")
	}

	// Non-200 HTTP response must fail closed.
	responseTenant = tenant.String()
	responseStatus = http.StatusNotFound
	if _, _, err = provider.ResolveStartMetadata(context.Background(), job); err == nil {
		t.Fatal("expected error for HTTP 404 metadata response")
	}
}
