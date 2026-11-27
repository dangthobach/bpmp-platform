package gateway

import (
	"net/http"
	"net/url"
	"testing"
)

func TestODSPhase45ProxyOriginAndCapabilityGuards(t *testing.T) {
	if _, err := newHTTPSOriginProxy(http.DefaultClient, "http://insecure.local", "Document"); err == nil {
		t.Fatalf("expected non-HTTPS upstream to be rejected")
	}
	if _, err := newHTTPSOriginProxy(http.DefaultClient, "https://doc.internal:8443", "Document"); err != nil {
		t.Fatalf("expected valid HTTPS origin, got %v", err)
	}

	// Browser proxy must never forward scanner callback ingress.
	if _, ok := documentRequiredCapabilities(http.MethodPost, "/v1/documents/scan-callbacks/clamav"); ok {
		t.Fatalf("scanner callbacks must not be exposed on browser BFF proxy")
	}
	if caps, ok := documentRequiredCapabilities(http.MethodPost, "/v1/documents/doc-1/legal-hold"); !ok || caps[0] != "document.hold" {
		t.Fatalf("unexpected legal-hold capabilities: %v %v", caps, ok)
	}
	if caps, ok := integrationRequiredCapabilities(http.MethodPost, "/v1/admin/integration/deliveries/d1/retry"); !ok || caps[0] != "integration.delivery.retry" {
		t.Fatalf("unexpected integration retry capabilities: %v %v", caps, ok)
	}
	if caps, ok := masterDataRequiredCapabilities(http.MethodPost, "/v1/mdm/publications"); !ok || caps[0] != "mdm.publish" {
		t.Fatalf("unexpected mdm publish capabilities: %v %v", caps, ok)
	}
	if caps, ok := auditProjectionRequiredCapabilities(http.MethodPost, "/v1/audit/rebuild"); !ok || caps[0] != "audit.projection.rebuild" {
		t.Fatalf("unexpected audit rebuild capabilities: %v %v", caps, ok)
	}

	// Path traversal rejection.
	badURL, _ := url.Parse("/v1/documents/../../etc/passwd")
	if validPrefixedPath(badURL, "/v1/documents") {
		t.Fatalf("expected path traversal to be rejected")
	}
}
