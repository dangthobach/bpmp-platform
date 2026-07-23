package gateway

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
)

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
	handler, err := NewHandler(engine, &recordingHuman{}, &verifier{keys: map[string]crypto.PublicKey{"actor-key": public}, issuers: map[string]struct{}{"issuer": {}}, audiences: map[string]struct{}{"gateway": {}}, methods: []string{"EdDSA"}, maxTokenBytes: 4096}, &workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute}, newModelRateLimiter(10, time.Minute), map[string]string{"tenant-a": "tenant-a/workflows"}, 4096)
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
