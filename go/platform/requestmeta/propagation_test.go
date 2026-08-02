package requestmeta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestHTTPMiddlewareEchoesValidatedIdentifiersAndTraceID(t *testing.T) {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3}, SpanID: trace.SpanID{4, 5}, TraceFlags: trace.FlagsSampled,
	})
	var captured Values
	handler := HTTPMiddleware("test-service", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	request = request.WithContext(trace.ContextWithSpanContext(request.Context(), spanContext))
	request.Header.Set(HTTPHeaderRequestID, "request-1")
	request.Header.Set(HTTPHeaderCorrelationID, "correlation-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if captured.RequestID != "request-1" || captured.CorrelationID != "correlation-1" {
		t.Fatalf("unexpected request metadata: %+v", captured)
	}
	if got := response.Header().Get(HTTPHeaderTraceID); got != spanContext.TraceID().String() {
		t.Fatalf("trace ID = %q, want %q", got, spanContext.TraceID().String())
	}
}

func TestUnaryClientInterceptorReplacesDuplicateMetadata(t *testing.T) {
	ctx := metadata.AppendToOutgoingContext(context.Background(), CorrelationID, "stale")
	ctx = WithValues(ctx, Values{RequestID: "request-1", CorrelationID: "correlation-1"})
	interceptor := UnaryClientInterceptor()
	err := interceptor(ctx, "/bpmp.test.v1.Service/Call", struct{}{}, &struct{}{}, nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			outgoing, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				t.Fatal("outgoing metadata is missing")
			}
			if values := outgoing.Get(CorrelationID); len(values) != 1 || values[0] != "correlation-1" {
				t.Fatalf("correlation metadata = %v", values)
			}
			if values := outgoing.Get(RequestID); len(values) != 1 || values[0] != "request-1" {
				t.Fatalf("request metadata = %v", values)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInjectHTTPUsesValidatedContextMetadata(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{9, 8, 7}, SpanID: trace.SpanID{6, 5}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = WithValues(ctx, Values{
		RequestID: "request-1", CorrelationID: "correlation-1",
		TenantID: "tenant-1", CommandID: "command-1",
	})
	header := http.Header{"Traceparent": []string{"untrusted"}}
	InjectHTTP(ctx, header)
	if header.Get(HTTPHeaderRequestID) != "request-1" ||
		header.Get(HTTPHeaderCorrelationID) != "correlation-1" ||
		header.Get(HTTPHeaderTenantID) != "tenant-1" ||
		header.Get(HTTPHeaderCommandID) != "command-1" {
		t.Fatalf("unexpected HTTP metadata: %v", header)
	}
	if got := header.Get(TraceParent); got == "" || got == "untrusted" {
		t.Fatalf("W3C trace context was not replaced: %q", got)
	}
}

func TestKafkaPropagationRoundTrip(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{9, 8, 7}, SpanID: trace.SpanID{6, 5}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = WithValues(ctx, Values{
		RequestID: "request-1", CorrelationID: "correlation-1", TenantID: "tenant-1",
	})
	record := &kgo.Record{Topic: "bpmp.events"}
	InjectKafka(ctx, record)

	extracted := ExtractKafka(context.Background(), record)
	values, ok := FromContext(extracted)
	if !ok || values.RequestID != "request-1" || values.CorrelationID != "correlation-1" || values.TenantID != "tenant-1" {
		t.Fatalf("unexpected Kafka metadata: %+v", values)
	}
	if got := trace.SpanContextFromContext(extracted).TraceID(); got != spanContext.TraceID() {
		t.Fatalf("trace ID = %s, want %s", got, spanContext.TraceID())
	}
}

func TestWriteProblemResponseUsesCanonicalContract(t *testing.T) {
	response := httptest.NewRecorder()
	response.Header().Set(HTTPHeaderRequestID, "request-1")
	response.Header().Set(HTTPHeaderCorrelationID, "correlation-1")
	WriteProblemResponse(response, http.StatusBadRequest, "invalid_request", "Invalid request", "", false)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q", got)
	}
}
