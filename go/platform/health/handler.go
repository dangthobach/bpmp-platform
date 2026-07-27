package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Check func(context.Context) error

func Handler(timeout time.Duration, checks ...Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, "live")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		for _, check := range checks {
			if err := check(ctx); err != nil {
				writeStatus(w, http.StatusServiceUnavailable, "not-ready")
				return
			}
		}
		writeStatus(w, http.StatusOK, "ready")
	})
	return mux
}

func writeStatus(w http.ResponseWriter, code int, value string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": value}); err != nil {
		return
	}
}
