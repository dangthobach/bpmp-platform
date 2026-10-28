package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
)

type browserSessionStoreFunc func(context.Context, string) (ports.BrowserSession, error)

func (function browserSessionStoreFunc) Get(
	ctx context.Context,
	token string,
) (ports.BrowserSession, error) {
	return function(ctx, token)
}

func TestBrowserSessionAuthenticatesQueryAndPreservesSignedContext(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	csrf := sha256.Sum256([]byte("csrf-token"))
	store := browserSessionStoreFunc(func(_ context.Context, token string) (ports.BrowserSession, error) {
		if token != "session-token" {
			t.Fatalf("unexpected session token %q", token)
		}
		return ports.BrowserSession{
			TenantID: "tenant-a", ActorID: "actor-1",
			SignedActorContext: []byte("portal-signed-context"),
			CSRFTokenSHA256:    csrf[:], ExpiresAt: now.Add(time.Hour),
		}, nil
	})
	human := &recordingHuman{}
	handler := newBrowserSessionHandler(t, now, human, store)
	request := httptest.NewRequest(http.MethodGet, "/v1/work-items", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-bpmp_session", Value: "session-token"})
	request.Header.Set("X-Correlation-ID", "correlation-1")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body)
	}
	proof := human.listRequest.GetActorProof()
	if proof.GetType() != authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT ||
		string(proof.GetSignedProof()) != "portal-signed-context" ||
		human.listRequest.GetTenantId() != "tenant-a" {
		t.Fatalf("session identity was not preserved: %#v", human.listRequest)
	}
}

func TestBrowserSessionRequiresCSRFForUnsafeMethod(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	csrf := sha256.Sum256([]byte("csrf-token"))
	store := browserSessionStoreFunc(func(context.Context, string) (ports.BrowserSession, error) {
		return ports.BrowserSession{
			TenantID: "tenant-a", ActorID: "actor-1",
			SignedActorContext: []byte("portal-signed-context"),
			CSRFTokenSHA256:    csrf[:], ExpiresAt: now.Add(time.Hour),
		}, nil
	})
	for name, csrfToken := range map[string]string{
		"missing": "", "incorrect": "wrong-token",
	} {
		t.Run(name, func(t *testing.T) {
			handler := newBrowserSessionHandler(t, now, &recordingHuman{}, store)
			request := newSessionCommandRequest(csrfToken)
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unexpected status %d: %s", response.Code, response.Body)
			}
		})
	}
	engine := &recordingEngine{}
	handler := newBrowserSessionHandlerWithEngine(t, now, engine, &recordingHuman{}, store)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, newSessionCommandRequest("csrf-token"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("valid CSRF status %d: %s", response.Code, response.Body)
	}
}

func TestBrowserSessionRejectsAmbiguousExpiredAndMismatchedIdentity(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	csrf := sha256.Sum256([]byte("csrf-token"))
	valid := ports.BrowserSession{
		TenantID: "tenant-a", ActorID: "actor-1",
		SignedActorContext: []byte("signed"), CSRFTokenSHA256: csrf[:],
		ExpiresAt: now.Add(time.Hour),
	}
	tests := map[string]struct {
		mutate  func(*http.Request)
		session ports.BrowserSession
		err     error
		status  int
	}{
		"ambiguous credentials": {
			mutate:  func(request *http.Request) { request.Header.Set("Authorization", "Bearer token") },
			session: valid, status: http.StatusUnauthorized,
		},
		"duplicate cookie": {
			mutate: func(request *http.Request) {
				request.AddCookie(&http.Cookie{Name: "__Host-bpmp_session", Value: "second"})
			},
			session: valid, status: http.StatusUnauthorized,
		},
		"expired": {
			session: func() ports.BrowserSession { value := valid; value.ExpiresAt = now; return value }(),
			status:  http.StatusUnauthorized,
		},
		"tenant mismatch": {
			mutate:  func(request *http.Request) { request.Header.Set("X-BPMP-Tenant-ID", "tenant-b") },
			session: valid, status: http.StatusUnauthorized,
		},
		"store failure": {
			err: errors.New("Redis unavailable"), status: http.StatusBadGateway,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := browserSessionStoreFunc(func(context.Context, string) (ports.BrowserSession, error) {
				return test.session, test.err
			})
			handler := newBrowserSessionHandler(t, now, &recordingHuman{}, store)
			request := httptest.NewRequest(http.MethodGet, "/v1/work-items", nil)
			request.AddCookie(&http.Cookie{Name: "__Host-bpmp_session", Value: "session-token"})
			request.Header.Set("X-Correlation-ID", "correlation-1")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("unexpected status %d: %s", response.Code, response.Body)
			}
		})
	}
}

func newSessionCommandRequest(csrfToken string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/workflows/order/instances", bytes.NewBufferString(`{"instance_id":"instance-1","workflow_version":"1","start_node_id":"start"}`))
	request.AddCookie(&http.Cookie{Name: "__Host-bpmp_session", Value: "session-token"})
	request.Header.Set("X-Command-ID", "command-1")
	request.Header.Set("Idempotency-Key", "idempotency-1")
	request.Header.Set("X-Correlation-ID", "correlation-1")
	if csrfToken != "" {
		request.Header.Set("X-CSRF-Token", csrfToken)
	}
	return request
}

func newBrowserSessionHandler(t *testing.T, now time.Time, human *recordingHuman, store ports.BrowserSessionStore) *Handler {
	return newBrowserSessionHandlerWithEngine(t, now, &recordingEngine{}, human, store)
}

func newBrowserSessionHandlerWithEngine(t *testing.T, now time.Time, engine *recordingEngine, human *recordingHuman, store ports.BrowserSessionStore) *Handler {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(
		engine, human, testVerifier(t, public),
		&workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute},
		newModelRateLimiter(10, time.Minute), staticPolicyProvider{},
		WithBrowserSessions(store, config.BrowserSession{
			Enabled: true, CookieName: "__Host-bpmp_session", CSRFHeaderName: "X-CSRF-Token",
			RedisKeyPrefix: "bff:session", OperationTimeoutMS: 100,
			MaxTokenBytes: 256, MaxRecordBytes: 8192,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	return handler
}
