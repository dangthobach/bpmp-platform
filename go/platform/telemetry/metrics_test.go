package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsHandlerReturnsValidJSONSnapshot(t *testing.T) {
	handler := MetricsHandler()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if ct := response.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var snapshot map[string]any
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	requiredKeys := []string{
		"uptime_seconds", "goroutines",
		"requests_total", "request_errors_total", "requests_in_flight",
		"kafka_messages_consumed_total", "kafka_consumer_errors_total",
		"traces_propagated_total", "traces_root_created_total",
	}
	for _, key := range requiredKeys {
		if _, ok := snapshot[key]; !ok {
			t.Errorf("missing key %q in metrics snapshot", key)
		}
	}
}

func TestMetricsHandlerRejectsMutatingMethods(t *testing.T) {
	handler := MetricsHandler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		request := httptest.NewRequest(method, "/metrics", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: status = %d, want 405", method, response.Code)
		}
	}
}

func TestMetricsHandlerHeadReturnsNoBody(t *testing.T) {
	handler := MetricsHandler()
	request := httptest.NewRequest(http.MethodHead, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", response.Code)
	}
	if response.Body.Len() != 0 {
		t.Errorf("HEAD response must have empty body, got %d bytes", response.Body.Len())
	}
}

func TestMetricsHandlerCountersReflectAtomicIncrements(t *testing.T) {
	// Save and restore global counters to avoid test cross-contamination.
	savedTotal := Counters.RequestsTotal.Load()
	savedErrors := Counters.RequestErrors.Load()
	t.Cleanup(func() {
		Counters.RequestsTotal.Store(savedTotal)
		Counters.RequestErrors.Store(savedErrors)
	})

	Counters.RequestsTotal.Add(7)
	Counters.RequestErrors.Add(2)

	handler := MetricsHandler()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var snapshot map[string]any
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// JSON numbers decode as float64.
	if got := snapshot["requests_total"].(float64); got != float64(savedTotal+7) {
		t.Errorf("requests_total = %v, want %v", got, savedTotal+7)
	}
	if got := snapshot["request_errors_total"].(float64); got != float64(savedErrors+2) {
		t.Errorf("request_errors_total = %v, want %v", got, savedErrors+2)
	}
}

// TestMetricsServerPortIsolation confirms that MetricsServer creates a server
// bound to the specified address and that its handler exposes /metrics and
// rejects unknown paths.
func TestMetricsServerPortIsolation(t *testing.T) {
	// We test the handler tree, not actual TCP binding.
	server := MetricsServer(":9090")
	if server.Addr != ":9090" {
		t.Errorf("server addr = %q, want :9090", server.Addr)
	}

	// /metrics must be registered.
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", response.Code)
	}

	// An unknown path must return 404, not accidentally leak any data.
	request = httptest.NewRequest(http.MethodGet, "/admin/secret", nil)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Errorf("/admin/secret status = %d, want 404", response.Code)
	}
}
