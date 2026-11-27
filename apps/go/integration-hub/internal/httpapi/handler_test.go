package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/integration-hub/internal/application"
)

func TestHTTPIntegrationEndpointsAndSSRF(t *testing.T) {
	svc := application.NewService(application.Config{AllowedEgressHosts: []string{"esb.bank.example"}})
	h := NewHandler(svc)

	// SSRF blocked with 403
	badBody, _ := json.Marshal(map[string]any{
		"name": "Local", "system_type": "WEBHOOK", "base_url": "https://127.0.0.1/secret", "secret_ref": "vault://x",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/endpoints", bytes.NewReader(badBody))
	req.Header.Set("X-BPMP-Tenant-ID", "t1")
	req.Header.Set("X-BPMP-Actor-ID", "a1")
	req.Header.Set("X-BPMP-Capabilities", "integration.endpoint.write,integration.endpoint.read")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 SSRF blocked, got %d", rec.Code)
	}

	// Valid endpoint returns 201
	goodBody, _ := json.Marshal(map[string]any{
		"name": "T24 ESB", "system_type": "T24_ESB", "base_url": "https://esb.bank.example/v1", "secret_ref": "vault://t24",
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/endpoints", bytes.NewReader(goodBody))
	req.Header.Set("X-BPMP-Tenant-ID", "t1")
	req.Header.Set("X-BPMP-Actor-ID", "a1")
	req.Header.Set("X-BPMP-Capabilities", "integration.endpoint.write,integration.endpoint.read")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}
