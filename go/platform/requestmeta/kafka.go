package requestmeta

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var kafkaTracer = otel.Tracer("github.com/dangthobach/bpmp-platform/go/platform/requestmeta")

type KafkaOperation struct {
	trace.Span
	ctx       context.Context
	started   time.Time
	operation string
	topic     string
	partition int32
	offset    int64
	err       error
}

func StartKafkaConsumerSpan(ctx context.Context, record *kgo.Record) (context.Context, *KafkaOperation) {
	started := time.Now()
	ctx = ExtractKafka(ctx, record)
	ctx, span := kafkaTracer.Start(ctx, "kafka consume "+record.Topic,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", record.Topic),
			attribute.Int("messaging.kafka.partition", int(record.Partition)),
			attribute.Int64("messaging.kafka.offset", record.Offset),
		),
	)
	return ctx, &KafkaOperation{
		Span: span, ctx: ctx, started: started, operation: "consume",
		topic: record.Topic, partition: record.Partition, offset: record.Offset,
	}
}

func StartKafkaProducerSpan(ctx context.Context, record *kgo.Record) (context.Context, *KafkaOperation) {
	started := time.Now()
	ctx = Ensure(ctx)
	ctx, span := kafkaTracer.Start(ctx, "kafka publish "+record.Topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", record.Topic),
		),
	)
	return ctx, &KafkaOperation{
		Span: span, ctx: ctx, started: started, operation: "publish", topic: record.Topic,
	}
}

func RecordSpanError(span *KafkaOperation, err error) {
	if err == nil {
		return
	}
	span.err = err
	span.Span.RecordError(err)
	span.Span.SetStatus(codes.Error, err.Error())
}

func (operation *KafkaOperation) End(options ...trace.SpanEndOption) {
	attrs := SlogAttrs(operation.ctx)
	attrs = append(attrs,
		slog.String("transport", "kafka"),
		slog.String("messaging_operation", operation.operation),
		slog.String("messaging_topic", operation.topic),
		slog.Int64("duration_ms", time.Since(operation.started).Milliseconds()),
	)
	if operation.operation == "consume" {
		attrs = append(attrs,
			slog.Int("messaging_partition", int(operation.partition)),
			slog.Int64("messaging_offset", operation.offset),
		)
	}
	if operation.err != nil {
		attrs = append(attrs, slog.Any("error", operation.err))
		slog.WarnContext(operation.ctx, "Kafka operation completed", attrs...)
	} else {
		slog.InfoContext(operation.ctx, "Kafka operation completed", attrs...)
	}
	operation.Span.End(options...)
}

func InjectKafka(ctx context.Context, record *kgo.Record) {
	ctx = Ensure(ctx)
	values, _ := FromContext(ctx)
	carrier := kafkaCarrier{record: record}
	carrier.Set(RequestID, values.RequestID)
	carrier.Set(CorrelationID, values.CorrelationID)
	if values.TenantID != "" {
		carrier.Set(TenantID, values.TenantID)
	}
	if values.CommandID != "" {
		carrier.Set(CommandID, values.CommandID)
	}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
}

func ExtractKafka(ctx context.Context, record *kgo.Record) context.Context {
	carrier := kafkaCarrier{record: record}
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)
	return WithValues(ctx, Values{
		RequestID:     carrier.Get(RequestID),
		CorrelationID: carrier.Get(CorrelationID),
		TenantID:      carrier.Get(TenantID),
		CommandID:     carrier.Get(CommandID),
	})
}

type kafkaCarrier struct{ record *kgo.Record }

func (c kafkaCarrier) Get(key string) string {
	for index := len(c.record.Headers) - 1; index >= 0; index-- {
		if strings.EqualFold(c.record.Headers[index].Key, key) {
			return string(c.record.Headers[index].Value)
		}
	}
	return ""
}

func (c kafkaCarrier) Set(key, value string) {
	filtered := c.record.Headers[:0]
	for _, header := range c.record.Headers {
		if !strings.EqualFold(header.Key, key) {
			filtered = append(filtered, header)
		}
	}
	c.record.Headers = append(filtered, kgo.RecordHeader{Key: key, Value: []byte(value)})
}

func (c kafkaCarrier) Keys() []string {
	keys := make([]string, 0, len(c.record.Headers))
	for _, header := range c.record.Headers {
		keys = append(keys, header.Key)
	}
	return keys
}
