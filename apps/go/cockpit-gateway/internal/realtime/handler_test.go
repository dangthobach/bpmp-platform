package realtime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/proto"
)

type sseRecorder struct {
	mu      sync.Mutex
	header  http.Header
	status  int
	buf     bytes.Buffer
	flushed chan struct{}
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{
		header:  make(http.Header),
		flushed: make(chan struct{}, 32),
	}
}

func (r *sseRecorder) Header() http.Header { return r.header }
func (r *sseRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = code
}
func (r *sseRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}
func (r *sseRecorder) Flush() {
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}
func (r *sseRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func newRealtimeTestSetup(t *testing.T, now time.Time) (*Handler, *subscription.Hub, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "OKP",
			"crv": "Ed25519",
			"kid": "jwt-key",
			"x":   base64.RawURLEncoding.EncodeToString(public),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	jwtVerifier, err := jwtauth.NewFromJWKS(jwtauth.Config{
		Issuers:       []string{"issuer"},
		Audiences:     []string{"cockpit"},
		Algorithms:    []string{"EdDSA"},
		MaxTokenBytes: 8192,
		MaxJWKSKeys:   4,
		ClockSkew:     time.Second,
	}, jwks)
	if err != nil {
		t.Fatal(err)
	}
	proofVerifier, err := actorcontext.New(actorcontext.Config{
		WorkloadID: "cockpit-gateway",
		MaxBytes:   8192,
		MaxRoles:   32,
		MaxCaps:    64,
		ClockSkew:  time.Second,
		Keys:       map[string]ed25519.PublicKey{"proof-key": public},
	})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := subscription.New(subscription.Config{
		MaxSubscriptions: 16,
		BufferSize:       16,
		ReplaySize:       16,
		MaxReplayStreams: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{
		Path:                  "/realtime/v1/events",
		AllowedSignalNames:    []string{"work-item.changed", "notification.inapp"},
		AllowedOrigins:        []string{"https://cockpit.example.test"},
		MaxNamesPerConnection: 2,
		MaxSignalNamesBytes:   256,
		MaxConnections:        8,
		HeartbeatInterval:     time.Hour,
	}, hub, jwtVerifier, proofVerifier)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	return handler, hub, private
}

func signTestJWT(t *testing.T, private ed25519.PrivateKey, now time.Time, tenantID, subject string, caps []string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss":          "issuer",
		"sub":          subject,
		"aud":          []string{"cockpit"},
		"exp":          now.Add(time.Hour).Unix(),
		"iat":          now.Add(-time.Minute).Unix(),
		"tenant_id":    tenantID,
		"capabilities": caps,
	})
	token.Header["kid"] = "jwt-key"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signTestActorContext(t *testing.T, private ed25519.PrivateKey, now time.Time, tenantID, actorID string, caps []string) string {
	t.Helper()
	sortedCaps := append([]string(nil), caps...)
	sort.Strings(sortedCaps)
	proof := &authv1.SignedActorContext{
		SchemaVersion:      1,
		TenantId:           tenantID,
		ActorId:            actorID,
		AudienceWorkloadId: "cockpit-gateway",
		SigningKeyId:       "proof-key",
		Capabilities:       sortedCaps,
		IssuedAtEpochMs:    uint64(now.Add(-time.Second).UnixMilli()),
		ExpiresAtEpochMs:   uint64(now.Add(time.Minute).UnixMilli()),
	}
	unsigned, err := (proto.MarshalOptions{Deterministic: true}).Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(private, digest[:])
	signed, err := (proto.MarshalOptions{Deterministic: true}).Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signed)
}

func TestNotificationInAppRejectsMissingCapability(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	handler, _, private := newRealtimeTestSetup(t, now)
	jwtNoCap := signTestJWT(t, private, now, "tenant-a", "user-a", []string{"task.read"})

	req := httptest.NewRequest(http.MethodGet, "/realtime/v1/events?names=notification.inapp", nil)
	req.Header.Set("Authorization", "Bearer "+jwtNoCap)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing notification.inbox.read capability, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestNotificationInAppDeliversOnlyToVerifiedRecipientAndIgnoresClientSpoofing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	handler, hub, private := newRealtimeTestSetup(t, now)

	tokenA := signTestActorContext(t, private, now, "tenant-a", "user-a", []string{"notification.inbox.read"})
	tokenB := signTestJWT(t, private, now, "tenant-a", "user-b", []string{"notification.read"})

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	reqA := httptest.NewRequest(http.MethodGet, "/realtime/v1/events?names=notification.inapp", nil).WithContext(ctxA)
	reqA.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	reqA.Header.Set("X-BPMP-Signed-Actor-Context", tokenA)
	recA := newSSERecorder()

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	// User B tries to spoof recipient_id via query param and header.
	reqB := httptest.NewRequest(http.MethodGet, "/realtime/v1/events?names=notification.inapp&recipient_id=user-a&actor_id=user-a", nil).WithContext(ctxB)
	reqB.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	reqB.Header.Set("Authorization", "Bearer "+tokenB)
	reqB.Header.Set("X-Recipient-ID", "user-a")
	recB := newSSERecorder()

	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() {
		defer close(doneA)
		handler.ServeHTTP(recA, reqA)
	}()
	go func() {
		defer close(doneB)
		handler.ServeHTTP(recB, reqB)
	}()

	// Wait for both streams to flush ": connected"
	<-recA.flushed
	<-recB.flushed

	hub.PublishToIndexedSubscribers(subscription.Signal{
		Cursor:      "hints:0:1",
		TenantID:    "tenant-a",
		Name:        "notification.inapp",
		RecipientID: "user-a",
		Payload:     []byte(`{"sequence":1,"message_id":"msg-a-1","category_key":"alerts"}`),
	})
	hub.PublishToIndexedSubscribers(subscription.Signal{
		Cursor:      "hints:0:2",
		TenantID:    "tenant-a",
		Name:        "notification.inapp",
		RecipientID: "user-b",
		Payload:     []byte(`{"sequence":2,"message_id":"msg-b-1","category_key":"alerts"}`),
	})

	<-recA.flushed
	<-recB.flushed
	cancelA()
	cancelB()
	<-doneA
	<-doneB

	bodyA := recA.String()
	bodyB := recB.String()
	if !strings.Contains(bodyA, "event: notification.inapp") || !strings.Contains(bodyA, `"message_id":"msg-a-1"`) {
		t.Fatalf("user-a did not receive its own hint: %s", bodyA)
	}
	if strings.Contains(bodyA, "msg-b-1") {
		t.Fatalf("user-a received user-b hint: %s", bodyA)
	}
	if !strings.Contains(bodyB, `"message_id":"msg-b-1"`) {
		t.Fatalf("user-b did not receive its own hint: %s", bodyB)
	}
	if strings.Contains(bodyB, "msg-a-1") {
		t.Fatalf("user-b received user-a hint despite verified identity isolation: %s", bodyB)
	}

	// Reconnect replay: user B reconnecting with user A's Last-Event-ID must still never receive user A's subsequent hint.
	hub.PublishToIndexedSubscribers(subscription.Signal{
		Cursor:      "hints:0:3",
		TenantID:    "tenant-a",
		Name:        "notification.inapp",
		RecipientID: "user-a",
		Payload:     []byte(`{"sequence":3,"message_id":"msg-a-2","category_key":"alerts"}`),
	})
	ctxReplayB, cancelReplayB := context.WithCancel(context.Background())
	defer cancelReplayB()
	reqReplayB := httptest.NewRequest(http.MethodGet, "/realtime/v1/events?names=notification.inapp&recipient_id=user-a", nil).WithContext(ctxReplayB)
	reqReplayB.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	reqReplayB.Header.Set("Authorization", "Bearer "+tokenB)
	reqReplayB.Header.Set("Last-Event-ID", "hints:0:1")
	recReplayB := newSSERecorder()
	doneReplayB := make(chan struct{})
	go func() {
		defer close(doneReplayB)
		handler.ServeHTTP(recReplayB, reqReplayB)
	}()
	<-recReplayB.flushed
	<-recReplayB.flushed
	cancelReplayB()
	<-doneReplayB

	replayBodyB := recReplayB.String()
	if !strings.Contains(replayBodyB, "msg-b-1") {
		t.Fatalf("user-b replay missed its own hint: %s", replayBodyB)
	}
	if strings.Contains(replayBodyB, "msg-a-1") || strings.Contains(replayBodyB, "msg-a-2") {
		t.Fatalf("user-b replay leaked user-a hints: %s", replayBodyB)
	}
}
