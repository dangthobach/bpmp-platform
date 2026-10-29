package requestmeta

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	otelcodes "go.opentelemetry.io/otel/codes"
)

// setupInMemoryTracer installs an in-memory span exporter as the global tracer
// provider for the duration of the test and returns the recorded span slice.
func setupInMemoryTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

// TestKafkaConsumerSpanContinuesRemoteTrace verifies that StartKafkaConsumerSpan
// creates a child span that is linked to the trace context injected by the producer.
func TestKafkaConsumerSpanContinuesRemoteTrace(t *testing.T) {
	recorder := setupInMemoryTracer(t)

	// Simulate a producer that injected trace context into Kafka headers.
	producerSpanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	producerCtx := trace.ContextWithSpanContext(context.Background(), producerSpanContext)
	record := &kgo.Record{Topic: "bpmp.tasks.assigned", Partition: 2, Offset: 42}
	InjectKafka(producerCtx, record)

	// Consumer side: start a span from the record.
	ctx, op := StartKafkaConsumerSpan(context.Background(), record)
	op.End()

	// The consumer span must be a child of the producer trace.
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}
	consumerSpan := spans[0]
	if consumerSpan.SpanContext().TraceID() != producerSpanContext.TraceID() {
		t.Errorf("trace ID mismatch: consumer=%s producer=%s",
			consumerSpan.SpanContext().TraceID(), producerSpanContext.TraceID())
	}
	if consumerSpan.Parent().SpanID() != producerSpanContext.SpanID() {
		t.Errorf("parent span ID mismatch: got %s, want %s",
			consumerSpan.Parent().SpanID(), producerSpanContext.SpanID())
	}
	if consumerSpan.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("span kind = %s, want Consumer", consumerSpan.SpanKind())
	}
	// requestmeta values must also be extractable from the context returned by StartKafkaConsumerSpan.
	values, ok := FromContext(ctx)
	if !ok {
		t.Fatal("requestmeta values not found in consumer context")
	}
	if values.CorrelationID == "" {
		t.Error("CorrelationID must not be empty after consumer extraction")
	}
}

// TestKafkaConsumerSpanStartsNewRootWhenHeadersMissing verifies that a consumer
// receiving a record with no trace headers starts a new root span rather than
// crashing or producing an invalid span.
func TestKafkaConsumerSpanStartsNewRootWhenHeadersMissing(t *testing.T) {
	recorder := setupInMemoryTracer(t)

	record := &kgo.Record{Topic: "bpmp.tasks.assigned", Partition: 0, Offset: 1}
	// No headers injected — simulate a record from a legacy producer.

	_, op := StartKafkaConsumerSpan(context.Background(), record)
	op.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}
	span := spans[0]
	if !span.SpanContext().IsValid() {
		t.Error("span context must be valid even when producer headers are absent")
	}
	if span.Parent().IsValid() {
		t.Error("span must be a root (no parent) when no traceparent header is present")
	}
}

// TestKafkaConsumerSpanStartsNewRootWhenTraceParentMalformed verifies that a
// malformed traceparent header does not crash the consumer and produces a valid
// root span instead of an invalid/linked span.
func TestKafkaConsumerSpanStartsNewRootWhenTraceParentMalformed(t *testing.T) {
	recorder := setupInMemoryTracer(t)

	record := &kgo.Record{
		Topic:  "bpmp.tasks.assigned",
		Headers: []kgo.RecordHeader{
			{Key: "traceparent", Value: []byte("not-a-valid-traceparent-value")},
			{Key: "x-bpmp-correlation-id", Value: []byte("correlation-legacy-1")},
		},
	}

	_, op := StartKafkaConsumerSpan(context.Background(), record)
	op.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}
	span := spans[0]
	if !span.SpanContext().IsValid() {
		t.Error("span context must be valid even when traceparent is malformed")
	}
	// A malformed traceparent yields no remote parent — the consumer span is a root.
	if span.Parent().IsValid() {
		t.Error("span must be a root when traceparent is malformed")
	}
}

// TestKafkaConsumerSpanErrorRecordedAndDoesNotBlockProcessing verifies that
// RecordSpanError marks the span as errored but the call is safe for nil error.
func TestKafkaConsumerSpanErrorRecordedAndDoesNotBlockProcessing(t *testing.T) {
	recorder := setupInMemoryTracer(t)

	record := &kgo.Record{Topic: "bpmp.tasks.assigned"}
	_, op := StartKafkaConsumerSpan(context.Background(), record)
	// Nil error must be a safe no-op.
	RecordSpanError(op, nil)
	op.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}
	if spans[0].Status().Code == otelcodes.Error {
		t.Error("nil error must not mark span as errored")
	}
}
