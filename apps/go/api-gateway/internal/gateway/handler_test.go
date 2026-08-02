package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (function doerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

type staticPolicyProvider struct{}

func (staticPolicyProvider) Policy(string) (RuntimePolicy, error) {
	return RuntimePolicy{
		RateLimitRequests: 10, RateLimitWindow: time.Minute,
		UpstreamTimeout: time.Second, CircuitBreakerFailureThreshold: 3,
		CircuitBreakerOpen: time.Second, BulkheadMaxConcurrency: 4,
		MaxRequestBodyBytes: 4096, MaxUpstreamResponseBytes: 4096,
		BatchChunkSize: 10, BatchConcurrency: 2,
		UpstreamRetry: RetryPolicy{
			MaxAttempts: 2, InitialBackoff: time.Millisecond,
			MaxBackoff: 10 * time.Millisecond, MultiplierMillis: 2000,
		},
		EncryptionKeyScope: "tenant-a/workflows",
		ConfigVersion:      "config-v7",
		PolicyVersion:      "policy-v3",
		ContentETag:        "abcdef",
	}, nil
}

type recordingEngine struct {
	enginev1.EngineCommandServiceClient
	envelope *enginev1.CommandEnvelope
	metadata metadata.MD
}

func (c *recordingEngine) HandleCommand(ctx context.Context, envelope *enginev1.CommandEnvelope, _ ...grpc.CallOption) (*enginev1.CommandReceipt, error) {
	c.envelope = envelope
	c.metadata, _ = metadata.FromOutgoingContext(ctx)
	return &enginev1.CommandReceipt{CommandId: envelope.CommandId}, nil
}

type recordingHuman struct {
	humanv1.HumanRuntimeServiceClient
	listRequest *humanv1.ListWorkItemsRequest
	metadata    metadata.MD
}

func (c *recordingHuman) ListWorkItems(ctx context.Context, request *humanv1.ListWorkItemsRequest, _ ...grpc.CallOption) (*humanv1.ListWorkItemsResponse, error) {
	c.listRequest = request
	c.metadata, _ = metadata.FromOutgoingContext(ctx)
	return &humanv1.ListWorkItemsResponse{
		WorkItems: []*humanv1.WorkItem{{
			TenantId:   request.TenantId,
			WorkItemId: "work-1",
			Status:     "ACTIVE",
		}},
		NextPageToken: "next",
	}, nil
}

func TestGatewayPreservesActorProofAndIdempotencyKey(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_000, 0).UTC()
	claims := jwt.MapClaims{"iss": "issuer", "sub": "actor-1", "aud": []string{"gateway"}, "exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix(), "tenant_id": "tenant-a"}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingEngine{}
	handler, err := NewHandler(engine, &recordingHuman{}, testVerifier(t, public), &workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute}, newModelRateLimiter(10, time.Minute), staticPolicyProvider{})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodPost, "/v1/workflows/order/instances", bytes.NewBufferString(`{"instance_id":"instance-1","workflow_version":"1","start_node_id":"start"}`))
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	request.Header.Set("X-Command-ID", "command-1")
	request.Header.Set("Idempotency-Key", "client-idempotency-77")
	request.Header.Set("X-Correlation-ID", "correlation-1")
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	if engine.envelope.GetIdempotencyKey() != "client-idempotency-77" {
		t.Fatal("gateway changed idempotency key")
	}
	if string(engine.envelope.GetAuthorizationContext().GetActorProof().GetSignedProof()) != raw {
		t.Fatal("gateway changed original actor proof")
	}
	if values := engine.metadata.Get("x-bpmp-correlation-id"); len(values) != 1 || values[0] != "correlation-1" {
		t.Fatalf("correlation metadata was not preserved: %v", values)
	}
	if values := engine.metadata.Get("traceparent"); len(values) != 1 || values[0] != request.Header.Get("traceparent") {
		t.Fatalf("trace metadata was not preserved: %v", values)
	}
}

func TestBrowserConfigurationReturnsSafeFieldsAndHonorsETag(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_000, 0).UTC()
	claims := jwt.MapClaims{
		"iss": "issuer", "sub": "actor-1", "aud": []string{"gateway"},
		"exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix(),
		"tenant_id": "tenant-a",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(
		&recordingEngine{},
		&recordingHuman{},
		testVerifier(t, public),
		&workloadSigner{
			id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute,
		},
		newModelRateLimiter(10, time.Minute),
		staticPolicyProvider{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/v1/runtime/browser-configuration", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	request.Header.Set("X-Correlation-ID", "correlation-1")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"abcdef"` {
		t.Fatalf("unexpected browser configuration response: %d %s", response.Code, response.Body)
	}
	var body map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 4 || body["config_version"] != "config-v7" ||
		body["policy_version"] != "policy-v3" {
		t.Fatalf("browser response leaked or omitted fields: %#v", body)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/runtime/browser-configuration", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	request.Header.Set("X-Correlation-ID", "correlation-2")
	request.Header.Set("If-None-Match", `"abcdef"`)
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("ETag request was not short-circuited: %d %s", response.Code, response.Body)
	}
}

func TestListWorkItemsForwardsTenantActorAndCursor(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_000, 0).UTC()
	claims := jwt.MapClaims{"iss": "issuer", "sub": "actor-1", "aud": []string{"gateway"}, "exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix(), "tenant_id": "tenant-a"}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	human := &recordingHuman{}
	handler, err := NewHandler(&recordingEngine{}, human, testVerifier(t, public), &workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute}, newModelRateLimiter(10, time.Minute), staticPolicyProvider{})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/v1/work-items?page_size=25&page_token=cursor-1", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	request.Header.Set("X-Correlation-ID", "correlation-1")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	if human.listRequest.GetTenantId() != "tenant-a" ||
		human.listRequest.GetPageSize() != 25 ||
		human.listRequest.GetPageToken() != "cursor-1" ||
		string(human.listRequest.GetActorProof().GetSignedProof()) != raw {
		t.Fatalf("query scope was not forwarded: %+v", human.listRequest)
	}
	if values := human.metadata.Get("x-bpmp-correlation-id"); len(values) != 1 || values[0] != "correlation-1" {
		t.Fatalf("correlation metadata was not preserved: %v", values)
	}
}

func TestConfigurationFacadePreservesAuthenticatedCommandScope(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_000, 0).UTC()
	claims := jwt.MapClaims{
		"iss": "issuer", "sub": "actor-1", "aud": []string{"gateway"},
		"exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix(),
		"tenant_id": "tenant-a",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}

	var forwarded *http.Request
	var forwardedBody []byte
	client := doerFunc(func(request *http.Request) (*http.Response, error) {
		forwarded = request
		forwardedBody, err = io.ReadAll(request.Body)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"profile-1"}`)),
		}, err
	})
	handler, err := NewHandler(
		&recordingEngine{},
		&recordingHuman{},
		testVerifier(t, public),
		&workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute},
		newModelRateLimiter(10, time.Minute),
		staticPolicyProvider{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	handler.configurationProxy, err = newConfigurationProxy(
		client,
		"https://configuration.internal",
	)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/configuration/profiles",
		strings.NewReader(`{"name":"default"}`),
	)
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	request.Header.Set("X-Command-ID", "command-1")
	request.Header.Set("Idempotency-Key", "idempotency-1")
	request.Header.Set("X-Correlation-ID", "correlation-1")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	if forwarded == nil || forwarded.URL.String() != "https://configuration.internal/v1/configuration/profiles" {
		t.Fatalf("unexpected upstream request: %+v", forwarded)
	}
	for name, expected := range map[string]string{
		"Authorization":    "Bearer " + raw,
		"X-BPMP-Tenant-ID": "tenant-a",
		"X-Command-ID":     "command-1",
		"Idempotency-Key":  "idempotency-1",
		"X-Correlation-ID": "correlation-1",
	} {
		if actual := forwarded.Header.Get(name); actual != expected {
			t.Fatalf("%s was not preserved: %q", name, actual)
		}
	}
	if requestID := forwarded.Header.Get("X-Request-ID"); !requestmeta.ValidID(requestID) {
		t.Fatalf("canonical request ID was not injected: %q", requestID)
	}
	if string(forwardedBody) != `{"name":"default"}` {
		t.Fatalf("body changed: %s", forwardedBody)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("configuration response must not be cached")
	}
}

func testVerifier(t *testing.T, public ed25519.PublicKey) *verifier {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "kid": "actor-key", "crv": "Ed25519",
		"x": base64.RawURLEncoding.EncodeToString(public),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := jwtauth.NewFromJWKS(jwtauth.Config{
		Issuers: []string{"issuer"}, Audiences: []string{"gateway"},
		Algorithms: []string{"EdDSA"}, MaxTokenBytes: 4096, MaxJWKSKeys: 1,
	}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return &verifier{value: value}
}

func TestProperty33ErrorResponsesAreRedactedAndCorrelated(t *testing.T) {
	property := func(seed uint64, errorClass uint8) bool {
		correlationID := fmt.Sprintf("correlation-%d", seed)
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Header.Set("X-Correlation-ID", correlationID)
		response := httptest.NewRecorder()
		setCorrelationHeader(response, request)
		sensitive := fmt.Sprintf("password=secret-%d\nstack trace: internal.go:42", seed)
		var err error
		switch errorClass % 4 {
		case 0:
			err = errors.New(sensitive)
		case 1:
			err = fmt.Errorf("%w: %s", errForbidden, sensitive)
		case 2:
			err = fmt.Errorf("%w: %s", errRateLimited, sensitive)
		default:
			err = fmt.Errorf("%w: %s", errUpstream, sensitive)
		}
		writeError(response, err)

		var body requestmeta.Problem
		if json.Unmarshal(response.Body.Bytes(), &body) != nil {
			return false
		}
		encoded := response.Body.String()
		return response.Header().Get("X-Correlation-ID") == correlationID &&
			body.CorrelationID == correlationID &&
			body.RequestID != "" && body.Code != "" &&
			response.Header().Get("Content-Type") == "application/problem+json" &&
			!strings.Contains(encoded, sensitive) &&
			!strings.Contains(encoded, "password=") &&
			!strings.Contains(encoded, "stack trace")
	}

	// Feature: rust-bpm-platform, Property 33: errors are redacted and retain correlation ID
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestProperty34OnlySchemaValidRequestsReachProcessing(t *testing.T) {
	property := func(seed uint64, bodyClass uint8, invalidIdentifier bool) bool {
		identifier := fmt.Sprintf("instance-%d", seed)
		if invalidIdentifier {
			identifier = fmt.Sprintf("invalid instance %d", seed)
		}
		var body string
		switch bodyClass % 5 {
		case 0:
			body = fmt.Sprintf(`{"instance_id":"%s","workflow_version":"1","start_node_id":"start"}`, identifier)
		case 1:
			body = fmt.Sprintf(`{"instance_id":"%s","workflow_version":"1","start_node_id":"start","unknown":true}`, identifier)
		case 2:
			body = fmt.Sprintf(`{"instance_id":"%s","workflow_version":"1","start_node_id":"start"} {}`, identifier)
		case 3:
			body = `{"instance_id":`
		default:
			body = fmt.Sprintf(`{"instance_id":"%s","workflow_version":"%s","start_node_id":"start"}`, identifier, strings.Repeat("x", 512))
		}
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		response := httptest.NewRecorder()
		var decoded startRequest
		err := decodeBody(response, request, &decoded, 256)
		accepted := err == nil &&
			validID(decoded.InstanceID) &&
			validID(decoded.WorkflowVersion) &&
			validID(decoded.StartNodeID)
		return accepted == (bodyClass%5 == 0 && !invalidIdentifier)
	}

	// Feature: rust-bpm-platform, Property 34: malformed or schema-invalid requests are rejected
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
