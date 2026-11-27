package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"google.golang.org/protobuf/proto"
)

func makeEncodedProof(t *testing.T, priv ed25519.PrivateKey, tenantID, actorID, commandID string, caps []string, now time.Time) string {
	t.Helper()
	sorted := append([]string(nil), caps...)
	sort.Strings(sorted)
	msg := &authv1.SignedActorContext{
		SchemaVersion:      1,
		TenantId:           tenantID,
		ActorId:            actorID,
		AudienceWorkloadId: "notification-service",
		CommandId:          commandID,
		Roles:              []string{"admin"},
		Capabilities:       sorted,
		IssuedAtEpochMs:    uint64(now.Add(-time.Second).UnixMilli()),
		ExpiresAtEpochMs:   uint64(now.Add(time.Minute).UnixMilli()),
		SigningKeyId:       "k1",
	}
	unsigned, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
	sum := sha256.Sum256(unsigned)
	msg.ContentHash = sum[:]
	msg.Signature = ed25519.Sign(priv, sum[:])
	raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestHTTPAPIHeadersCapabilitiesPreviewAndEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	pub, priv, _ := ed25519.GenerateKey(nil)
	verifier, err := actorcontext.New(actorcontext.Config{
		WorkloadID: "notification-service",
		MaxBytes:   1 << 20, MaxRoles: 32, MaxCaps: 64,
		ClockSkew: 30 * time.Second,
		Keys:      map[string]ed25519.PublicKey{"k1": pub},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x66}, 32)
	ring, _ := crypto.NewKeyRing("k1", map[string][]byte{"k1": key}, key)
	svc, _ := application.NewService(application.NewMemoryStore(), queue.NewMemoryQueue(), ring, renderer.NewEngine(renderer.DefaultRenderLimits()), nil, application.Config{})
	svc.SetClock(func() time.Time { return now })

	h, err := NewHandler(svc, verifier)
	if err != nil {
		t.Fatal(err)
	}
	h.SetClock(func() time.Time { return now })
	routes := h.Routes()
	tenantID := "00000000-0000-0000-0000-000000000010"

	// 1. Missing actor context -> 401
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/templates", nil)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without headers, got %d", rec.Code)
	}

	// 2. Mutating POST without Idempotency-Key / X-Command-ID -> 400
	badPost := httptest.NewRequest(http.MethodPost, "/v1/notifications/categories", bytes.NewBufferString(`{"category_key":"sys","name":"Sys"}`))
	badPost.Header.Set("X-BPMP-Tenant-ID", tenantID)
	badPost.Header.Set("X-BPMP-Signed-Actor-Context", makeEncodedProof(t, priv, tenantID, "actor-1", "cmd-1", []string{"notification.manage"}, now))
	badRec := httptest.NewRecorder()
	routes.ServeHTTP(badRec, badPost)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when missing command/idempotency headers, got %d", badRec.Code)
	}

	// 3. Valid POST /v1/notifications/categories -> 201
	catReq := httptest.NewRequest(http.MethodPost, "/v1/notifications/categories", bytes.NewBufferString(`{"category_key":"ops","name":"Operations"}`))
	catReq.Header.Set("X-BPMP-Tenant-ID", tenantID)
	catReq.Header.Set("X-Command-ID", "cmd-cat-1")
	catReq.Header.Set("Idempotency-Key", "idem-cat-1")
	catReq.Header.Set("X-BPMP-Signed-Actor-Context", makeEncodedProof(t, priv, tenantID, "actor-1", "cmd-cat-1", []string{"notification.manage"}, now))
	catRec := httptest.NewRecorder()
	routes.ServeHTTP(catRec, catReq)
	if catRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating category, got %d: %s", catRec.Code, catRec.Body.String())
	}

	// 4. Draft Preview via POST /v1/notifications/preview -> 200 with SMS segments!
	prevBody := `{
		"mode":"DRAFT",
		"channel":"SMS",
		"locale":"vi-VN",
		"draft_variable_schema":{"type":"object","properties":{"code":{"type":"string","example":"998877"}}},
		"draft_variant":{"channel":"SMS","locale":"vi-VN","text_tmpl":"Mã xác thực của bạn là {{ .data.code }}"}
	}`
	prevReq := httptest.NewRequest(http.MethodPost, "/v1/notifications/preview", bytes.NewBufferString(prevBody))
	prevReq.Header.Set("X-BPMP-Tenant-ID", tenantID)
	prevReq.Header.Set("X-BPMP-Signed-Actor-Context", makeEncodedProof(t, priv, tenantID, "actor-1", "corr-prev", []string{"notification.read"}, now))
	prevRec := httptest.NewRecorder()
	routes.ServeHTTP(prevRec, prevReq)
	if prevRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on preview, got %d: %s", prevRec.Code, prevRec.Body.String())
	}
	var prevJSON map[string]any
	_ = json.Unmarshal(prevRec.Body.Bytes(), &prevJSON)
	if prevJSON["sms_encoding"] != "UCS2" || prevJSON["rendered_with_sample_data"] != true {
		t.Fatalf("unexpected preview response: %#v", prevJSON)
	}
}
