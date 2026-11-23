package engineadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type metadataStub struct{}

func (metadataStub) ResolveStartMetadata(_ context.Context, _ application.StartJob) (string, string, error) {
	return "start-node", "tenant/operational", nil
}

func TestAuthzCredentialsProviderIssuesBoundFreshProof(t *testing.T) {
	initialNow := time.Now().UTC().Truncate(time.Second)
	var clockMS atomic.Int64
	clockMS.Store(initialNow.UnixMilli())
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	job := application.StartJob{TenantID: uuid.New(), CaseID: uuid.New(), ActorID: uuid.NewString()}
	job.CommandID = job.CaseID
	var calls atomic.Int32
	var wrongScope atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/authz/v1/actor-context" || r.Method != http.MethodPost {
			t.Errorf("wrong Authz request: %s %s", r.Method, r.URL)
		}
		token, parseErr := jwt.Parse(r.Header.Get("Authorization")[7:], func(token *jwt.Token) (any, error) {
			return public, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}), jwt.WithAudience("authz-service"))
		if parseErr != nil || !token.Valid {
			t.Errorf("invalid Authz delegation: %v", parseErr)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["tenant_id"] != job.TenantID.String() || body["command_id"] != job.CommandID.String() || body["audience_workload_id"] != "case-service" {
			t.Errorf("wrong actor-context request: %+v", body)
		}
		actor := &authv1.SignedActorContext{TenantId: job.TenantID.String(), ActorId: job.ActorID,
			CommandId: job.CommandID.String(), AudienceWorkloadId: "case-service",
			IssuedAtEpochMs: uint64(initialNow.UnixMilli()), ExpiresAtEpochMs: uint64(initialNow.Add(10 * time.Second).UnixMilli()),
			ContentHash: make([]byte, sha256.Size), Signature: make([]byte, ed25519.SignatureSize)}
		actorBytes, _ := proto.Marshal(actor)
		audience := "case-service"
		if wrongScope.Load() {
			audience = "other-service"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"tenant_id": job.TenantID.String(), "actor_id": job.ActorID,
			"audience_workload_id": audience, "command_id": job.CommandID.String(),
			"signed_actor_context": base64.StdEncoding.EncodeToString(actorBytes)})
	}))
	defer server.Close()
	provider, err := NewAuthzCredentialsProvider(server.Client(), server.URL, "authz-service", "case-service", "case-key", private, metadataStub{}, func() time.Time { return time.UnixMilli(clockMS.Load()).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.ResolveStart(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	var workload authv1.SignedWorkloadContext
	if err = proto.Unmarshal(first.SignedWorkloadProof, &workload); err != nil {
		t.Fatal(err)
	}
	signature := append([]byte(nil), workload.Signature...)
	digest := append([]byte(nil), workload.ContentHash...)
	workload.Signature, workload.ContentHash = nil, nil
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(&workload)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256(unsigned)
	if string(digest) != string(wantDigest[:]) || !ed25519.Verify(public, digest, signature) {
		t.Fatal("workload proof signature is invalid")
	}
	clockMS.Store(initialNow.Add(time.Second).UnixMilli())
	second, err := provider.ResolveStart(context.Background(), job)
	if err != nil || calls.Load() != 2 || string(first.SignedWorkloadProof) == string(second.SignedWorkloadProof) {
		t.Fatalf("workload proof was not refreshed: calls=%d err=%v", calls.Load(), err)
	}
	wrongScope.Store(true)
	if _, err = provider.ResolveStart(context.Background(), job); err == nil {
		t.Fatal("wrong-audience actor context was accepted")
	}
}
