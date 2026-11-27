package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/document-adapter/internal/application"
)

func TestHTTPDocumentFlow(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	svc, err := application.NewService(application.Config{
		URLSigningSecret: []byte("http-test-signing-secret-1234"),
		CallbackSecrets:  map[string][][]byte{"clamav": {[]byte("cb-secret")}},
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h := NewHandler(svc)

	// Health check
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 readyz, got %d", rec.Code)
	}

	// Create upload session > 50 MiB returns 413 ProblemDetails
	body, _ := json.Marshal(map[string]any{
		"owner_ref_type": "CASE", "owner_ref_id": "case-99", "file_name": "huge.pdf",
		"declared_mime": "application/pdf", "declared_size_bytes": application.MaxUploadSizeBytes + 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/documents/uploads", bytes.NewReader(body))
	req.Header.Set("X-BPMP-Tenant-ID", "t1")
	req.Header.Set("X-BPMP-Actor-ID", "a1")
	req.Header.Set("X-BPMP-Capabilities", "document.read,document.write")
	req.Header.Set("Idempotency-Key", "k1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}

	// List documents returns 200
	listReq := httptest.NewRequest(http.MethodGet, "/v1/documents", nil)
	listReq.Header.Set("X-BPMP-Tenant-ID", "t1")
	listReq.Header.Set("X-BPMP-Actor-ID", "a1")
	listReq.Header.Set("X-BPMP-Capabilities", "document.read")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, listReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 list, got %d", rec.Code)
	}
}
