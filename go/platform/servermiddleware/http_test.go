package servermiddleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

func TestHTTPPipelineRecoversWithCanonicalProblem(t *testing.T) {
	handler, err := NewHTTP(HTTPConfig{Service: "test", SecurityHeaders: true},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("sensitive panic") }))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(requestmeta.HTTPHeaderRequestID, "request-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	var problem requestmeta.Problem
	if err = json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "internal_error" || problem.RequestID != "request-1" ||
		strings.Contains(response.Body.String(), "sensitive panic") {
		t.Fatalf("unexpected problem: %+v", problem)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers were not installed")
	}
}

func TestHTTPPipelineBoundsBodyAndInstallsDeadline(t *testing.T) {
	deadlineObserved := false
	handler, err := NewHTTP(HTTPConfig{
		Service: "test", RequestTimeout: time.Second, MaxBodyBytes: 4,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadlineObserved = r.Context().Deadline()
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}

	oversized := httptest.NewRequest(http.MethodPost, "/bounded", strings.NewReader("12345"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, oversized)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d", response.Code)
	}

	bounded := httptest.NewRequest(http.MethodPost, "/bounded", strings.NewReader("1234"))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, bounded)
	if response.Code != http.StatusNoContent || !deadlineObserved {
		t.Fatalf("status = %d, deadline = %v", response.Code, deadlineObserved)
	}
}

func TestHTTPPipelineLogsEnrichedAuthenticatedMetadata(t *testing.T) {
	var captured requestmeta.Values
	handler, err := NewHTTP(HTTPConfig{Service: "test"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestmeta.Enrich(r.Context(), requestmeta.Values{TenantID: "tenant-1", CommandID: "command-1"}) {
			t.Fatal("request metadata was not initialized")
		}
		captured, _ = requestmeta.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/test", nil))
	if captured.TenantID != "tenant-1" || captured.CommandID != "command-1" {
		t.Fatalf("metadata = %+v", captured)
	}
}

func TestHTTPPipelineRejectsAtAdmissionRateLimit(t *testing.T) {
	limiter, err := NewTokenBucket(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTP(HTTPConfig{Service: "test", RateLimiter: limiter},
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", response.Code)
	}
}
