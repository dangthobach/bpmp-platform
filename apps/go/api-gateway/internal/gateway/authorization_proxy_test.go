package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

func TestIssueActorContextAcceptsAuthzContractAndBindsRequestScope(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	nowMS := uint64(now.UnixMilli())
	var gotRequestBody map[string]string
	response := actorContextResponse{
		TenantID: "tenant-a", ActorID: "actor-a", AudienceWorkloadID: "human-runtime",
		CommandID: "command-a", IssuedAtEpochMS: nowMS - 1_000,
		ExpiresAtEpochMS: nowMS + 14_000, Roles: []string{"operator"},
		Capabilities: []string{"task.read", "task.claim"}, SignedActorContext: "cHJvb2Y=",
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	client := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/authz/v1/actor-context" {
			t.Errorf("unexpected Authz request: %s %s", request.Method, request.URL)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
			t.Error("Authz request is missing service delegation")
		}
		if decodeErr := json.NewDecoder(request.Body).Decode(&gotRequestBody); decodeErr != nil {
			t.Errorf("decode Authz request: %v", decodeErr)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(responseJSON))),
		}, nil
	})
	proxy, err := newAuthorizationProxy(client, "https://authz.example.test", "authz-service")
	if err != nil {
		t.Fatal(err)
	}
	signer := &workloadSigner{
		id: "api-gateway", keyID: "gateway-key",
		key: ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)), ttl: time.Minute,
	}
	ctx := requestmeta.WithValues(context.Background(), requestmeta.Values{CorrelationID: "request-correlation"})
	issued, err := proxy.issueActorContext(ctx, signer, "tenant-a", "actor-a", "human-runtime", "command-a", RuntimePolicy{
		UpstreamTimeout: time.Second, MaxRequestBodyBytes: 1024, MaxUpstreamResponseBytes: 4096,
	}, now)
	if err != nil {
		t.Fatalf("issue actor context: %v", err)
	}
	if string(issued.proof) != "proof" || len(issued.capabilities) != 2 {
		t.Fatalf("unexpected issued actor context: %#v", issued)
	}
	if gotRequestBody["tenant_id"] != "tenant-a" || gotRequestBody["audience_workload_id"] != "human-runtime" || gotRequestBody["command_id"] != "command-a" {
		t.Fatalf("actor context request scope mismatch: %#v", gotRequestBody)
	}
}

func TestValidActorContextClaimsRejectsExpiredOrMalformedContexts(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	nowMS := uint64(now.UnixMilli())
	base := actorContextResponse{
		IssuedAtEpochMS:  nowMS - 1_000,
		ExpiresAtEpochMS: nowMS + 14_000,
		Roles:            []string{"tenant-admin"},
		Capabilities:     []string{"identity.profile.manage"},
	}
	if !validActorContextClaims(base, now) {
		t.Fatal("valid actor context claims were rejected")
	}
	tests := map[string]actorContextResponse{
		"expired": func() actorContextResponse {
			value := base
			value.ExpiresAtEpochMS = nowMS
			return value
		}(),
		"issued too far in future": func() actorContextResponse {
			value := base
			value.IssuedAtEpochMS = nowMS + 30_001
			value.ExpiresAtEpochMS = nowMS + 40_000
			return value
		}(),
		"overlong lifetime": func() actorContextResponse {
			value := base
			value.ExpiresAtEpochMS = value.IssuedAtEpochMS + 120_001
			return value
		}(),
		"duplicate capability": func() actorContextResponse {
			value := base
			value.Capabilities = []string{"task.read", "task.read"}
			return value
		}(),
		"invalid role identifier": func() actorContextResponse {
			value := base
			value.Roles = []string{"admin\n"}
			return value
		}(),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if validActorContextClaims(value, now) {
				t.Fatal("malformed actor context claims were accepted")
			}
		})
	}
}

func TestActorContextAudienceMapsOnlyOwnedRoutes(t *testing.T) {
	tests := []struct {
		path     string
		want     string
		accepted bool
	}{
		{path: "/v1/work-items", want: "human-runtime", accepted: true},
		{path: "/v1/authoring/processes", want: "authoring-service", accepted: true},
		{path: "/v1/process-instances/abc", want: "projection-service", accepted: true},
		{path: "/v1/access/users", want: "api-gateway", accepted: true},
		{path: "/v1/unmapped", accepted: false},
	}
	for _, test := range tests {
		t.Run(test.path+"/mapped", func(t *testing.T) {
			got, ok := actorContextAudience(test.path, "api-gateway")
			if ok != test.accepted || (ok && got != test.want) {
				t.Fatalf("actorContextAudience(%q) = (%q, %t), want (%q, %t)", test.path, got, ok, test.want, test.accepted)
			}
		})
	}
	if _, ok := actorContextAudience("/v1/access/users", ""); ok {
		t.Fatal("route requiring gateway audience accepted an empty workload ID")
	}
}
