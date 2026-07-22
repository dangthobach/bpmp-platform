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

	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
)

type recordingEngine struct {
	enginev1.EngineCommandServiceClient
	envelope *enginev1.CommandEnvelope
}

func (c *recordingEngine) HandleCommand(_ context.Context, envelope *enginev1.CommandEnvelope, _ ...grpc.CallOption) (*enginev1.CommandReceipt, error) {
	c.envelope = envelope
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
	handler, err := NewHandler(engine, &recordingHuman{}, &verifier{keys: map[string]crypto.PublicKey{"actor-key": public}, issuers: map[string]struct{}{"issuer": {}}, audiences: map[string]struct{}{"gateway": {}}, methods: []string{"EdDSA"}, maxTokenBytes: 4096}, &workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute}, newRateLimiter(10, time.Minute, 100), map[string]string{"tenant-a": "tenant-a/workflows"}, 4096)
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
}
