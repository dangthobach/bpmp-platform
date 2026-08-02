package requestmeta

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	HTTPHeaderRequestID     = "X-Request-ID"
	HTTPHeaderCorrelationID = "X-Correlation-ID"
	HTTPHeaderTraceID       = "X-Trace-ID"
	HTTPHeaderTenantID      = "X-BPMP-Tenant-ID"
	HTTPHeaderCommandID     = "X-Command-ID"
)

type Problem struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Status        int    `json:"status"`
	Detail        string `json:"detail,omitempty"`
	Code          string `json:"code"`
	Instance      string `json:"instance,omitempty"`
	RequestID     string `json:"request_id"`
	CorrelationID string `json:"correlation_id"`
	TraceID       string `json:"trace_id,omitempty"`
	Retryable     bool   `json:"retryable"`
}

func InjectHTTP(ctx context.Context, header http.Header) {
	ctx = Ensure(ctx)
	values, _ := FromContext(ctx)
	header.Set(HTTPHeaderRequestID, values.RequestID)
	header.Set(HTTPHeaderCorrelationID, values.CorrelationID)
	if values.TenantID != "" {
		header.Set(HTTPHeaderTenantID, values.TenantID)
	}
	if values.CommandID != "" {
		header.Set(HTTPHeaderCommandID, values.CommandID)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
}

func HTTPMiddleware(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ctx := WithValues(r.Context(), Values{
			RequestID:     r.Header.Get(HTTPHeaderRequestID),
			CorrelationID: r.Header.Get(HTTPHeaderCorrelationID),
		})
		values, _ := FromContext(ctx)
		setHTTPResponseHeaders(w.Header(), values)
		capture := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		request := r.WithContext(ctx)
		defer func() {
			route := request.Pattern
			if route == "" {
				route = "unmatched"
			}
			attrs := SlogAttrs(ctx)
			attrs = append(attrs,
				slog.String("service", service),
				slog.String("transport", "http"),
				slog.String("http_method", r.Method),
				slog.String("http_route", route),
				slog.Int("http_status", capture.status),
				slog.Int64("http_response_bytes", capture.bytes),
				slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			)
			if capture.status >= http.StatusInternalServerError {
				slog.WarnContext(ctx, "HTTP request completed", attrs...)
			} else {
				slog.InfoContext(ctx, "HTTP request completed", attrs...)
			}
		}()
		next.ServeHTTP(capture, request)
	})
}

func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string, retryable bool) {
	ctx := Ensure(r.Context())
	values, _ := FromContext(ctx)
	writeProblem(w, ctx, values, r.URL.Path, status, code, title, detail, retryable)
}

// WriteProblemResponse is intended for legacy handler seams that do not pass the
// request to their error mapper. HTTPMiddleware has already installed these IDs.
func WriteProblemResponse(w http.ResponseWriter, status int, code, title, detail string, retryable bool) {
	values := Values{
		RequestID:     w.Header().Get(HTTPHeaderRequestID),
		CorrelationID: w.Header().Get(HTTPHeaderCorrelationID),
		TraceID:       w.Header().Get(HTTPHeaderTraceID),
	}
	ctx := WithValues(context.Background(), values)
	values, _ = FromContext(ctx)
	writeProblem(w, ctx, values, "", status, code, title, detail, retryable)
}

func writeProblem(w http.ResponseWriter, ctx context.Context, values Values, instance string, status int, code, title, detail string, retryable bool) {
	problem := Problem{
		Type:          "https://docs.bpmp.dev/problems/" + code,
		Title:         title,
		Status:        status,
		Detail:        detail,
		Code:          code,
		Instance:      instance,
		RequestID:     values.RequestID,
		CorrelationID: values.CorrelationID,
		TraceID:       values.TraceID,
		Retryable:     retryable,
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(problem); err != nil {
		slog.ErrorContext(ctx, "encode HTTP problem", append(SlogAttrs(ctx), "error", err)...)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	setHTTPResponseHeaders(w.Header(), values)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.WarnContext(ctx, "write HTTP problem", append(SlogAttrs(ctx), "error", err)...)
	}
}

func setHTTPResponseHeaders(header http.Header, values Values) {
	header.Set(HTTPHeaderRequestID, values.RequestID)
	header.Set(HTTPHeaderCorrelationID, values.CorrelationID)
	if values.TraceID != "" {
		header.Set(HTTPHeaderTraceID, values.TraceID)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int64
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(value []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(value)
	w.bytes += int64(written)
	return written, err
}

func (w *statusWriter) ResponseCommitted() bool { return w.wroteHeader }

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
