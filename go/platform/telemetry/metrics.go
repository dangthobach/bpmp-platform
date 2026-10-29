package telemetry

import (
	"encoding/json"
	"expvar"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

// GatewayCounters holds service-level atomic counters exported on the metrics
// endpoint. The counters use atomic operations so they are safe to increment
// from concurrent request goroutines without a mutex.
//
// Usage:
//
//	Counters.RequestsTotal.Add(1)
//	Counters.KafkaMessagesConsumed.Add(1)
type GatewayCounters struct {
	// HTTP gateway
	RequestsTotal   atomic.Int64
	RequestErrors   atomic.Int64
	RequestsInFlight atomic.Int64

	// Kafka consumer
	KafkaMessagesConsumed atomic.Int64
	KafkaConsumerErrors   atomic.Int64

	// Trace propagation
	TracesPropagated    atomic.Int64
	TracesRootCreated   atomic.Int64 // consumer started a new root (no upstream context)
}

// Counters is the process-wide metrics store. Initialised once at package load
// time; services call methods on it from multiple goroutines.
var Counters GatewayCounters

// startTime records when the process started for the uptime metric.
var startTime = time.Now()

// MetricsHandler returns an http.Handler that serves a JSON metrics document.
// This handler MUST be mounted only on a private/internal port, never on the
// public gateway port.
//
// The JSON schema is intentionally minimal and stable:
//
//	{
//	  "uptime_seconds": <float>,
//	  "goroutines": <int>,
//	  "requests_total": <int>,
//	  "request_errors_total": <int>,
//	  "requests_in_flight": <int>,
//	  "kafka_messages_consumed_total": <int>,
//	  "kafka_consumer_errors_total": <int>,
//	  "traces_propagated_total": <int>,
//	  "traces_root_created_total": <int>
//	}
func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshot := map[string]any{
			"uptime_seconds":                 time.Since(startTime).Seconds(),
			"goroutines":                     runtime.NumGoroutine(),
			"requests_total":                 Counters.RequestsTotal.Load(),
			"request_errors_total":           Counters.RequestErrors.Load(),
			"requests_in_flight":             Counters.RequestsInFlight.Load(),
			"kafka_messages_consumed_total":  Counters.KafkaMessagesConsumed.Load(),
			"kafka_consumer_errors_total":    Counters.KafkaConsumerErrors.Load(),
			"traces_propagated_total":        Counters.TracesPropagated.Load(),
			"traces_root_created_total":      Counters.TracesRootCreated.Load(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			http.Error(w, "encoding failed", http.StatusInternalServerError)
		}
	})
}

// ExpvarHandler returns the stdlib expvar handler for debugging. It should be
// mounted on the same private port as MetricsHandler, not the public port.
func ExpvarHandler() http.Handler {
	return expvar.Handler()
}

// MetricsServer returns a pre-configured *http.Server that serves the metrics
// and expvar endpoints on addr. The caller is responsible for calling
// ListenAndServe and for shutdown. addr MUST be a private/internal address
// (e.g. ":9090") that is not reachable from the public ingress.
func MetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", MetricsHandler())
	mux.Handle("GET /debug/vars", ExpvarHandler())
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
