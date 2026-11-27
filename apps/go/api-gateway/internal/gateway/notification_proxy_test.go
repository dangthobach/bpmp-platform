package gateway

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newNotificationTestHandler(
	t *testing.T,
	now time.Time,
	capabilities []string,
	upstream doerFunc,
	withAuthz bool,
) (*Handler, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{
		"iss":          "issuer",
		"sub":          "actor-1",
		"aud":          []string{"gateway"},
		"exp":          now.Add(time.Hour).Unix(),
		"iat":          now.Add(-time.Minute).Unix(),
		"tenant_id":    "tenant-a",
		"capabilities": capabilities,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "actor-key"
	rawJWT, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	options := []HandlerOption{
		WithNotificationServiceProxy(upstream, "https://notification.internal"),
	}
	if withAuthz {
		authzClient := doerFunc(func(r *http.Request) (*http.Response, error) {
			var body struct {
				TenantID           string `json:"tenant_id"`
				AudienceWorkloadID string `json:"audience_workload_id"`
				CommandID          string `json:"command_id"`
			}
			if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
				return nil, decodeErr
			}
			if body.AudienceWorkloadID != "notification-service" {
				t.Errorf("unexpected actor context audience %q", body.AudienceWorkloadID)
			}
			nowMS := uint64(now.UnixMilli())
			payload, _ := json.Marshal(actorContextResponse{
				TenantID:           body.TenantID,
				ActorID:            "actor-1",
				AudienceWorkloadID: body.AudienceWorkloadID,
				CommandID:          body.CommandID,
				IssuedAtEpochMS:    nowMS - 1_000,
				ExpiresAtEpochMS:   nowMS + 30_000,
				Roles:              []string{"notification-admin"},
				Capabilities:       capabilities,
				SignedActorContext: base64.StdEncoding.EncodeToString([]byte("signed-proof-bytes")),
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(payload)),
			}, nil
		})
		options = append(options, WithAuthorizationAdmin(authzClient, "https://authz.internal", "authz-service"))
	}
	handler, err := NewHandler(
		&recordingEngine{},
		&recordingHuman{},
		testVerifier(t, public),
		&workloadSigner{id: "api-gateway", keyID: "workload-key", key: private, ttl: time.Minute},
		newModelRateLimiter(100, time.Minute),
		staticPolicyProvider{},
		options...,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	return handler, rawJWT
}

func TestWithNotificationServiceProxyRejectsInvalidOrigin(t *testing.T) {
	noop := doerFunc(func(*http.Request) (*http.Response, error) { return nil, nil })
	for _, rawURL := range []string{
		"",
		"http://notification.internal",
		"https://notification.internal/v1",
		"https://notification.internal?a=1",
		"https://user:pass@notification.internal",
	} {
		if _, err := newNotificationProxy(noop, rawURL); err == nil {
			t.Fatalf("expected invalid notification upstream %q to be rejected", rawURL)
		}
	}
	if _, err := newNotificationProxy(nil, "https://notification.internal"); err == nil {
		t.Fatal("expected nil client to be rejected")
	}
}

func TestNotificationProxyUnauthenticatedAndMissingActorProof(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	called := false
	upstream := doerFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	handler, rawJWT := newNotificationTestHandler(t, now, []string{"notification.inbox.read"}, upstream, false)

	// Unauthenticated request -> 401
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/inbox", nil)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-1")
	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d: %s", rec.Code, rec.Body.String())
	}

	// Authenticated with JWT but no authorizationProxy to issue signed actor proof -> 502 (cannot issue proof)
	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/inbox", nil)
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-2")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when actor proof cannot be issued, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("upstream must not be called when authentication or actor proof fails")
	}
}

func TestNotificationProxyForwardsHeadersQueryAndRequiresIdempotencyOnMutations(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	var gotReq *http.Request
	var gotBody []byte
	upstream := doerFunc(func(r *http.Request) (*http.Response, error) {
		gotReq = r.Clone(r.Context())
		if r.Body != nil {
			gotBody, _ = io.ReadAll(r.Body)
		}
		respHeader := make(http.Header)
		respHeader.Set("Content-Type", "application/json")
		respHeader.Set("ETag", `"v1"`)
		respHeader.Set("Cache-Control", "public, max-age=3600")
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     respHeader,
			Body:       io.NopCloser(strings.NewReader(`{"id":"req-1"}`)),
		}, nil
	})
	handler, rawJWT := newNotificationTestHandler(t, now, []string{"notification.operations.send", "notification.inbox.read"}, upstream, true)

	// Missing Idempotency-Key / X-Command-ID on POST -> 400
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/send", strings.NewReader(`{"template_key":"welcome"}`))
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-missing-idem")
	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing idempotency headers, got %d: %s", rec.Code, rec.Body.String())
	}

	// Valid POST with all headers and query string
	req = httptest.NewRequest(http.MethodPost, "/v1/notifications/send?dry_run=false&lane=transactional", strings.NewReader(`{"template_key":"welcome"}`))
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-send-1")
	req.Header.Set("Idempotency-Key", "idem-1")
	req.Header.Set("X-Command-ID", "cmd-1")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("tracestate", "vendor=value")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("expected private, no-store Cache-Control, got %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("ETag") != `"v1"` {
		t.Fatalf("expected ETag passthrough, got %q", rec.Header().Get("ETag"))
	}
	if gotReq.URL.String() != "https://notification.internal/v1/notifications/send?dry_run=false&lane=transactional" {
		t.Fatalf("unexpected upstream URL: %s", gotReq.URL.String())
	}
	if gotReq.Header.Get("X-BPMP-Tenant-ID") != "tenant-a" ||
		gotReq.Header.Get("X-Correlation-ID") != "corr-send-1" ||
		gotReq.Header.Get("Idempotency-Key") != "idem-1" ||
		gotReq.Header.Get("X-Command-ID") != "cmd-1" ||
		gotReq.Header.Get("traceparent") != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" ||
		gotReq.Header.Get("tracestate") != "vendor=value" ||
		gotReq.Header.Get("X-BPMP-Signed-Actor-Context") != base64.StdEncoding.EncodeToString([]byte("signed-proof-bytes")) {
		t.Fatalf("forwarded headers mismatch: %+v", gotReq.Header)
	}
	if string(gotBody) != `{"template_key":"welcome"}` {
		t.Fatalf("unexpected forwarded body: %q", string(gotBody))
	}

	// GET with keyset cursor & filters
	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/inbox?cursor=eyJpZCI6MTB9&limit=25&unread_only=true", nil)
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-inbox-1")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected upstream status, got %d", rec.Code)
	}
	if gotReq.URL.RawQuery != "cursor=eyJpZCI6MTB9&limit=25&unread_only=true" {
		t.Fatalf("query string not preserved: %q", gotReq.URL.RawQuery)
	}
}

func TestNotificationProxyCapabilityEnforcementAndUnknownSubpathDenial(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	upstream := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})

	cases := []struct {
		name       string
		method     string
		target     string
		allowCap   string
		denyCap    string
	}{
		{"inbox read", http.MethodGet, "/v1/notifications/inbox", "notification.inbox.read", "notification.audit.read"},
		{"inbox write", http.MethodPatch, "/v1/notifications/inbox/msg-1/read", "notification.inbox.write", "notification.inbox.read"},
		{"preferences read", http.MethodGet, "/v1/notifications/preferences", "notification.preference.read", "notification.audit.read"},
		{"preferences write", http.MethodPut, "/v1/notifications/preferences", "notification.preference.write", "notification.preference.read"},
		{"send dry_run plan", http.MethodPost, "/v1/notifications/send?dry_run=true", "notification.operations.plan", "notification.inbox.read"},
		{"test-send", http.MethodPost, "/v1/notifications/test-send", "notification.operations.test_send", "notification.operations.send"},
		{"preview", http.MethodPost, "/v1/notifications/preview", "notification.preview.render", "notification.template.read"},
		{"templates validate", http.MethodPost, "/v1/notifications/templates/validate", "notification.template.validate", "notification.template.read"},
		{"templates batch", http.MethodPost, "/v1/notifications/templates/batch-actions", "notification.template.batch", "notification.template.read"},
		{"templates read", http.MethodGet, "/v1/notifications/templates/t-1", "notification.template.read", "notification.audit.read"},
		{"templates publish", http.MethodPost, "/v1/notifications/templates/t-1/publish", "notification.template.publish", "notification.template.write"},
		{"templates release activate", http.MethodPost, "/v1/notifications/templates/t-1/releases", "notification.release.activate", "notification.template.write"},
		{"templates release rollback", http.MethodPost, "/v1/notifications/templates/t-1/releases/rollback", "notification.release.rollback", "notification.template.write"},
		{"templates write", http.MethodDelete, "/v1/notifications/templates/t-1", "notification.template.write", "notification.template.read"},
		{"variable-mapping read", http.MethodGet, "/v1/notifications/variable-mapping", "notification.variable_mapping.read", "notification.template.read"},
		{"trigger-rules read", http.MethodGet, "/v1/notifications/trigger-rules", "notification.rule.read", "notification.template.read"},
		{"trigger-rules activate", http.MethodPost, "/v1/notifications/trigger-rules/r-1/revisions/1/activate", "notification.rule.activate", "notification.rule.write"},
		{"trigger-rules write", http.MethodPut, "/v1/notifications/trigger-rules/r-1/revisions", "notification.rule.write", "notification.rule.read"},
		{"applications read", http.MethodGet, "/v1/notifications/applications", "notification.application.read", "notification.audit.read"},
		{"applications rotate key", http.MethodPost, "/v1/notifications/applications/a-1/keys/rotate", "notification.application.rotate_key", "notification.application.write"},
		{"applications write", http.MethodPatch, "/v1/notifications/applications/a-1", "notification.application.write", "notification.application.read"},
		{"providers read", http.MethodGet, "/v1/notifications/providers/health", "notification.provider.read", "notification.audit.read"},
		{"providers breaker override", http.MethodPost, "/v1/notifications/providers/p-1/circuit-breaker/override", "notification.breaker.override", "notification.provider.write"},
		{"providers breaker probe", http.MethodPost, "/v1/notifications/providers/p-1/circuit-breaker/probe", "notification.breaker.probe", "notification.provider.write"},
		{"providers probe test-connection", http.MethodPost, "/v1/notifications/providers/p-1/test-connection", "notification.provider.probe", "notification.provider.write"},
		{"providers probe simulate-failure", http.MethodPost, "/v1/notifications/providers/p-1/simulate-failure", "notification.provider.probe", "notification.provider.write"},
		{"providers activate", http.MethodPost, "/v1/notifications/providers/p-1/activate", "notification.provider.activate", "notification.provider.write"},
		{"providers write", http.MethodPost, "/v1/notifications/providers", "notification.provider.write", "notification.provider.read"},
		{"routing-policies read", http.MethodGet, "/v1/notifications/routing-policies", "notification.routing.read", "notification.provider.read"},
		{"routing-policies write", http.MethodPut, "/v1/notifications/routing-policies/rp-1", "notification.routing.write", "notification.routing.read"},
		{"deliveries read", http.MethodGet, "/v1/notifications/deliveries/d-1", "notification.delivery.read", "notification.audit.read"},
		{"deliveries resend", http.MethodPost, "/v1/notifications/deliveries/d-1/resend", "notification.delivery.resend", "notification.delivery.read"},
		{"requests read", http.MethodGet, "/v1/notifications/requests/r-1", "notification.delivery.read", "notification.audit.read"},
		{"metrics query", http.MethodPost, "/v1/notifications/metrics/query", "notification.metrics.read", "notification.audit.read"},
		{"audit read", http.MethodGet, "/v1/notifications/audit", "notification.audit.read", "notification.metrics.read"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowHandler, allowJWT := newNotificationTestHandler(t, now, []string{tc.allowCap}, upstream, true)
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+allowJWT)
			req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
			req.Header.Set("X-Correlation-ID", "corr-allow")
			if tc.method != http.MethodGet {
				req.Header.Set("Idempotency-Key", "idem-allow")
				req.Header.Set("X-Command-ID", "cmd-allow")
			}
			rec := httptest.NewRecorder()
			allowHandler.Routes().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 with capability %q, got %d: %s", tc.allowCap, rec.Code, rec.Body.String())
			}

			denyHandler, denyJWT := newNotificationTestHandler(t, now, []string{tc.denyCap}, upstream, true)
			req = httptest.NewRequest(tc.method, tc.target, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+denyJWT)
			req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
			req.Header.Set("X-Correlation-ID", "corr-deny")
			if tc.method != http.MethodGet {
				req.Header.Set("Idempotency-Key", "idem-deny")
				req.Header.Set("X-Command-ID", "cmd-deny")
			}
			rec = httptest.NewRecorder()
			denyHandler.Routes().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 without capability %q, got %d: %s", tc.allowCap, rec.Code, rec.Body.String())
			}
		})
	}

	// Unknown subpaths must be denied by default
	allCapsHandler, allCapsJWT := newNotificationTestHandler(t, now, []string{"notification.inbox.read", "notification.audit.read"}, upstream, true)
	for _, unknown := range []string{"/v1/notifications", "/v1/notifications/unknown-endpoint", "/v1/notifications/secret/admin"} {
		req := httptest.NewRequest(http.MethodGet, unknown, nil)
		req.Header.Set("Authorization", "Bearer "+allCapsJWT)
		req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
		req.Header.Set("X-Correlation-ID", "corr-unknown")
		rec := httptest.NewRecorder()
		allCapsHandler.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for unknown subpath %q, got %d", unknown, rec.Code)
		}
	}
}

func TestNotificationProxyRejectsTraversalOversizedBodyAndMapsUpstreamErrors(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	upstreamErr := errors.New("connection refused")
	upstreamMode := "error"
	upstream := doerFunc(func(*http.Request) (*http.Response, error) {
		switch upstreamMode {
		case "error":
			return nil, upstreamErr
		case "oversized":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("a"), 5000))),
			}, nil
		case "not_found":
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
				Body:       io.NopCloser(strings.NewReader(`{"code":"not_found"}`)),
			}, nil
		default:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
	})
	handler, rawJWT := newNotificationTestHandler(t, now, []string{"notification.template.read", "notification.template.write"}, upstream, true)

	// Path traversal rejection (invoke handler directly to bypass ServeMux path cleaning)
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/templates", nil)
	req.URL = &url.URL{Path: "/v1/notifications/templates/../audit", RawPath: "/v1/notifications/templates/%2e%2e/audit"}
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-trav")
	rec := httptest.NewRecorder()
	handler.proxyNotification(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d: %s", rec.Code, rec.Body.String())
	}

	// Oversized request body (> 4096 bytes from staticPolicyProvider) -> 400
	req = httptest.NewRequest(http.MethodPost, "/v1/notifications/templates", bytes.NewReader(bytes.Repeat([]byte("x"), 5000)))
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-big")
	req.Header.Set("Idempotency-Key", "idem-big")
	req.Header.Set("X-Command-ID", "cmd-big")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized request body, got %d: %s", rec.Code, rec.Body.String())
	}

	// Upstream transport failure -> 502
	upstreamMode = "error"
	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/templates", nil)
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-err")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for upstream error, got %d", rec.Code)
	}

	// Oversized upstream response (> 4096 bytes) -> 502
	upstreamMode = "oversized"
	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/templates", nil)
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-big-resp")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for oversized upstream response, got %d", rec.Code)
	}

	// Upstream 404 problem response passthrough with private, no-store
	upstreamMode = "not_found"
	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/templates/missing", nil)
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.Header.Set("X-BPMP-Tenant-ID", "tenant-a")
	req.Header.Set("X-Correlation-ID", "corr-404")
	rec = httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("expected 404 passthrough with no-store, got %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}
