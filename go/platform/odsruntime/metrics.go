package odsruntime

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var defaultLatencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// MetricsCollector tracks low-cardinality RED metrics matching platform/observability/alerts/ods-slo-alerts.yaml.
// Never uses tenant_id, user_id, or resource_id as Prometheus labels.
type MetricsCollector struct {
	service   string
	mu        sync.Mutex
	requests  map[string]uint64 // "method|status" -> count
	buckets   []uint64          // cumulative counts matching defaultLatencyBuckets
	totalObs  uint64
	sumSec    float64
	extraFn   func() []GaugeSample
}

type GaugeSample struct {
	Name   string
	Labels string // e.g. `service="document-adapter"`
	Value  float64
}

func NewMetricsCollector(service string, extraFn func() []GaugeSample) *MetricsCollector {
	return &MetricsCollector{
		service:  service,
		requests: make(map[string]uint64),
		buckets:  make([]uint64, len(defaultLatencyBuckets)),
		extraFn:  extraFn,
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Middleware instruments all HTTP requests and serves Prometheus text format on GET /metrics.
func (m *MetricsCollector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/metrics" {
			m.serveMetrics(w)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		m.observe(r.Method, rec.status, time.Since(start).Seconds())
	})
}

func (m *MetricsCollector) observe(method string, status int, durSec float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := method + "|" + strconv.Itoa(status)
	m.requests[key]++
	m.totalObs++
	m.sumSec += durSec
	for i, bound := range defaultLatencyBuckets {
		if durSec <= bound {
			m.buckets[i]++
		}
	}
}

func (m *MetricsCollector) serveMetrics(w http.ResponseWriter) {
	m.mu.Lock()
	reqCopy := make(map[string]uint64, len(m.requests))
	for k, v := range m.requests {
		reqCopy[k] = v
	}
	bucketsCopy := append([]uint64(nil), m.buckets...)
	totalObs := m.totalObs
	sumSec := m.sumSec
	m.mu.Unlock()

	var b strings.Builder
	b.WriteString("# HELP http_server_requests_total Total HTTP requests handled by ODS service.\n")
	b.WriteString("# TYPE http_server_requests_total counter\n")
	keys := make([]string, 0, len(reqCopy))
	for k := range reqCopy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 2)
		fmt.Fprintf(&b, "http_server_requests_total{service=%q,method=%q,status=%q} %d\n", m.service, parts[0], parts[1], reqCopy[k])
	}

	b.WriteString("# HELP http_server_request_duration_seconds HTTP request duration histogram.\n")
	b.WriteString("# TYPE http_server_request_duration_seconds histogram\n")
	for i, bound := range defaultLatencyBuckets {
		fmt.Fprintf(&b, "http_server_request_duration_seconds_bucket{service=%q,le=\"%g\"} %d\n", m.service, bound, bucketsCopy[i])
	}
	fmt.Fprintf(&b, "http_server_request_duration_seconds_bucket{service=%q,le=\"+Inf\"} %d\n", m.service, totalObs)
	fmt.Fprintf(&b, "http_server_request_duration_seconds_sum{service=%q} %.6f\n", m.service, sumSec)
	fmt.Fprintf(&b, "http_server_request_duration_seconds_count{service=%q} %d\n", m.service, totalObs)

	if m.extraFn != nil {
		for _, g := range m.extraFn() {
			if g.Labels != "" {
				fmt.Fprintf(&b, "%s{%s} %g\n", g.Name, g.Labels, g.Value)
			} else {
				fmt.Fprintf(&b, "%s %g\n", g.Name, g.Value)
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}
