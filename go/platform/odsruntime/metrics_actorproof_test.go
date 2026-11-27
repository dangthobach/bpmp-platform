package odsruntime

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetricsMiddlewareExposesREDMetrics(t *testing.T) {
	mc := NewMetricsCollector("document-adapter", func() []GaugeSample {
		return []GaugeSample{{Name: "document_quarantine_pending_count", Value: 3}}
	})
	h := mc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/documents/uploads", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	mRec := httptest.NewRecorder()
	h.ServeHTTP(mRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if mRec.Code != http.StatusOK {
		t.Fatalf("expected 200 /metrics, got %d", mRec.Code)
	}
	body := mRec.Body.String()
	for _, want := range []string{
		`http_server_requests_total{service="document-adapter",method="POST",status="201"} 1`,
		`http_server_request_duration_seconds_count{service="document-adapter"} 1`,
		`document_quarantine_pending_count 3`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, body)
		}
	}
}

func TestActorProofMiddlewareFailClosedAndBypassRoutes(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "actor.pub")
	if err := os.WriteFile(keyFile, pub, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	verifier, err := LoadActorVerifier("internal-actor-v1", "document-adapter", keyFile)
	if err != nil {
		t.Fatalf("LoadActorVerifier: %v", err)
	}

	called := false
	h := ActorProofMiddleware(verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Protected route without signed proof must fail closed with 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/documents", nil))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("expected 401 fail-closed without proof, got code=%d called=%v", rec.Code, called)
	}

	// Health/readyz/metrics/callback bypass browser actor proof.
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/v1/documents/scan-callbacks/clamav"} {
		called = false
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !called {
			t.Fatalf("expected bypass on %s, got code=%d called=%v", path, rec.Code, called)
		}
	}
}
