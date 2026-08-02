package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
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
	if err := configureJSONLogging(config.ServiceName, config.ServiceVersion); err != nil {
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

func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

func GRPCClientStatsHandler() stats.Handler {
	return otelgrpc.NewClientHandler()
}

func GRPCServerStatsHandler() stats.Handler {
	return otelgrpc.NewServerHandler()
}

func configureJSONLogging(serviceName, serviceVersion string) error {
	level := new(slog.LevelVar)
	configured := strings.TrimSpace(os.Getenv("BPMP_LOG_LEVEL"))
	if configured != "" {
		var parsed slog.Level
		if err := parsed.UnmarshalText([]byte(configured)); err != nil {
			return errors.New("BPMP_LOG_LEVEL is invalid")
		}
		level.Set(parsed)
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler).With(
		slog.String("service.name", serviceName),
		slog.String("service.version", serviceVersion),
	))
	return nil
}
