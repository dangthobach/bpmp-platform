package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/internal/eventstream"
	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/internal/realtime"
	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	"github.com/dangthobach/bpmp-platform/go/platform/health"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/servermiddleware"
	"github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

func main() {
	configPath := flag.String(
		"config", os.Getenv("COCKPIT_GATEWAY_CONFIG"),
		"path to cockpit-gateway JSON configuration",
	)
	flag.Parse()
	if *configPath == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*configPath); err != nil {
		slog.Error("cockpit-gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	value, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tracer, err := telemetry.Start(ctx, telemetry.Config{
		ServiceName:    value.Telemetry.ServiceName,
		ServiceVersion: value.Telemetry.ServiceVersion,
		Endpoint:       value.Telemetry.Endpoint, Insecure: value.Telemetry.Insecure,
		SampleRatio:   value.Telemetry.SampleRatio,
		ExportTimeout: milliseconds(value.Telemetry.ExportTimeoutMS),
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(
			context.Background(), milliseconds(value.Telemetry.ExportTimeoutMS),
		)
		defer cancel()
		if shutdownErr := tracer.Shutdown(shutdown); shutdownErr != nil {
			slog.Error("flush cockpit telemetry", "error", shutdownErr)
		}
	}()
	identity, err := jwtauth.New(jwtauth.Config{
		JWKSPath: value.Identity.JWKSPath, Issuers: value.Identity.Issuers,
		Audiences: value.Identity.Audiences, Algorithms: value.Identity.Algorithms,
		MaxTokenBytes: value.Identity.MaxTokenBytes,
		MaxJWKSKeys:   value.Identity.MaxJWKSKeys,
		ClockSkew:     milliseconds(value.Identity.ClockSkewSeconds * 1000),
	})
	if err != nil {
		return err
	}
	hub, err := subscription.New(subscription.Config{
		MaxSubscriptions: value.Realtime.MaxSubscriptions,
		BufferSize:       value.Realtime.OutboundBufferSize,
		ReplaySize:       value.Realtime.ReplaySizePerStream,
		MaxReplayStreams: value.Realtime.MaxReplayStreams,
	})
	if err != nil {
		return err
	}
	eventKafka, err := runtimeconfig.NewKafkaClient(value.Kafka)
	if err != nil {
		return err
	}
	defer eventKafka.Close()
	consumer, err := eventstream.NewConsumer(eventKafka, hub, value.Kafka.BatchSize)
	if err != nil {
		return err
	}
	realtimeHandler, err := realtime.New(realtime.Config{
		Path:                  value.Realtime.Path,
		AllowedSignalNames:    value.Realtime.AllowedSignalNames,
		AllowedOrigins:        value.HTTP.AllowedOrigins,
		MaxNamesPerConnection: value.Realtime.MaxNamesPerConnection,
		MaxSignalNamesBytes:   value.Realtime.MaxSignalNamesBytes,
		MaxConnections:        value.Realtime.MaxConnections,
		HeartbeatInterval:     milliseconds(value.Realtime.HeartbeatIntervalMS),
	}, hub, identity)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	realtimeHandler.Register(mux)
	admissionLimiter, err := servermiddleware.NewTokenBucket(
		value.HTTP.AdmissionRateRPS, value.HTTP.AdmissionBurst,
	)
	if err != nil {
		return err
	}
	publicTransport, err := servermiddleware.NewHTTP(servermiddleware.HTTPConfig{
		Service:        value.Telemetry.ServiceName,
		RequestTimeout: milliseconds(value.HTTP.RequestTimeoutMS),
		TimeoutExempt: func(request *http.Request) bool {
			return request.URL.Path == value.Realtime.Path
		},
		SecurityHeaders: true, RateLimiter: admissionLimiter,
	}, mux)
	if err != nil {
		return err
	}
	public := &http.Server{
		Addr:              value.ListenAddress,
		Handler:           telemetry.HTTPHandler(value.Telemetry.ServiceName, publicTransport),
		ReadHeaderTimeout: milliseconds(value.HTTP.ReadHeaderTimeoutMS),
		IdleTimeout:       milliseconds(value.HTTP.IdleTimeoutMS),
		MaxHeaderBytes:    value.HTTP.MaxHeaderBytes,
	}
	healthHandler := health.Handler(
		milliseconds(value.Health.ReadinessTimeoutMS),
		func(ctx context.Context) error { return eventKafka.Ping(ctx) },
	)
	healthTransport, err := servermiddleware.NewHTTP(servermiddleware.HTTPConfig{
		Service:        value.Telemetry.ServiceName + ".health",
		RequestTimeout: milliseconds(value.Health.ReadinessTimeoutMS), SecurityHeaders: true,
	}, healthHandler)
	if err != nil {
		return err
	}
	healthServer := &http.Server{
		Addr: value.HealthAddress,
		Handler: telemetry.HTTPHandler(
			"cockpit-gateway.health",
			healthTransport,
		),
		ReadHeaderTimeout: milliseconds(value.Health.ReadinessTimeoutMS),
		ReadTimeout:       milliseconds(value.Health.ReadinessTimeoutMS),
		WriteTimeout:      milliseconds(value.Health.ReadinessTimeoutMS),
		IdleTimeout:       milliseconds(value.Health.ReadinessTimeoutMS),
		MaxHeaderBytes:    value.Health.MaxHeaderBytes,
	}
	certificate, err := tls.LoadX509KeyPair(
		value.TLS.Certificate, value.TLS.PrivateKey,
	)
	if err != nil {
		return fmt.Errorf("load cockpit gateway TLS identity: %w", err)
	}
	public.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
	}
	errs := make(chan error, 3)
	go func() { errs <- consumer.Run(ctx) }()
	go func() {
		errs <- public.ListenAndServeTLS("", "")
	}()
	go func() { errs <- healthServer.ListenAndServe() }()
	slog.Info("cockpit-gateway started",
		"listen_address", value.ListenAddress,
		"health_address", value.HealthAddress,
	)
	select {
	case <-ctx.Done():
	case runErr := <-errs:
		if !errors.Is(runErr, http.ErrServerClosed) {
			err = runErr
		}
	}
	shutdown, cancel := context.WithTimeout(
		context.Background(), milliseconds(value.HTTP.ShutdownTimeoutMS),
	)
	defer cancel()
	return errors.Join(
		err, public.Shutdown(shutdown), healthServer.Shutdown(shutdown),
	)
}
