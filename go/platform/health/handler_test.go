package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLivenessDoesNotDependOnReadiness(t *testing.T) {
	handler := Handler(time.Second, func(context.Context) error {
		return errors.New("dependency down")
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected liveness status: %d", response.Code)
	}
}

func TestReadinessFailsClosedOnDependencyError(t *testing.T) {
	handler := Handler(time.Second, func(context.Context) error {
		return errors.New("dependency down")
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected readiness status: %d", response.Code)
	}
}
