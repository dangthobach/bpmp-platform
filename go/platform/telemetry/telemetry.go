package telemetry

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"google.golang.org/grpc/stats"
)

type Config struct {
	ServiceName    string
	ServiceVersion string
	Endpoint       string
	Insecure       bool
	SampleRatio    float64
	ExportTimeout  time.Duration
}

func (c Config) Validate() error {
	if c.ServiceName == "" ||
		c.ServiceVersion == "" ||
		c.Endpoint == "" ||
		c.SampleRatio < 0 ||
		c.SampleRatio > 1 ||
		c.ExportTimeout <= 0 {
		return errors.New("OpenTelemetry configuration is invalid")
	}
	return nil
}

func Start(ctx context.Context, config Config) (*sdktrace.TracerProvider, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	options := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.Endpoint),
		otlptracegrpc.WithTimeout(config.ExportTimeout),
	}
	if config.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithExportTimeout(config.ExportTimeout)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
		)),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider, nil
}

func HTTPHandler(operation string, handler http.Handler) http.Handler {
	return otelhttp.NewHandler(handler, operation)
}

func GRPCClientStatsHandler() stats.Handler {
	return otelgrpc.NewClientHandler()
}

func GRPCServerStatsHandler() stats.Handler {
	return otelgrpc.NewServerHandler()
}
