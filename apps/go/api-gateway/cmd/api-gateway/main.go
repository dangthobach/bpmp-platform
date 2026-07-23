package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/adapter/redislimit"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/gateway"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	platformgrpc "github.com/dangthobach/bpmp-platform/go/platform/grpcclient"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	platformtelemetry "github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

func main() {
	path := flag.String("config", os.Getenv("API_GATEWAY_CONFIG"), "path to API gateway JSON configuration")
	flag.Parse()
	if *path == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*path); err != nil {
		slog.Error("api-gateway stopped", "error", err)
		os.Exit(1)
	}
}
func run(path string) error {
	value, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tracerProvider, err := platformtelemetry.Start(ctx, platformtelemetry.Config{
		ServiceName:    value.Telemetry.ServiceName,
		ServiceVersion: value.Telemetry.ServiceVersion,
		Endpoint:       value.Telemetry.Endpoint,
		Insecure:       value.Telemetry.Insecure,
		SampleRatio:    value.Telemetry.SampleRatio,
		ExportTimeout:  value.Telemetry.ExportTimeout(),
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), value.Telemetry.ExportTimeout())
		defer cancel()
		if shutdownErr := tracerProvider.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Error("flush OpenTelemetry", "error", shutdownErr)
		}
	}()
	clientCertificate, err := tls.LoadX509KeyPair(value.UpstreamTLS.Certificate, value.UpstreamTLS.PrivateKey)
	if err != nil {
		return err
	}
	roots, err := certPool(value.UpstreamTLS.CA)
	if err != nil {
		return err
	}
	engineInterceptor, err := reliabilityInterceptor(value.Reliability)
	if err != nil {
		return err
	}
	humanInterceptor, err := reliabilityInterceptor(value.Reliability)
	if err != nil {
		return err
	}
	engineConn, err := grpc.NewClient(value.EngineAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.EngineServerName, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}})), grpc.WithUnaryInterceptor(engineInterceptor), grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes)))
	if err != nil {
		return err
	}
	defer engineConn.Close()
	humanConn, err := grpc.NewClient(value.HumanAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.HumanServerName, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}})), grpc.WithUnaryInterceptor(humanInterceptor), grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes)))
	if err != nil {
		return err
	}
	defer humanConn.Close()
	redisPassword, err := readOptionalSecret(value.RateLimit.RedisPasswordFile)
	if err != nil {
		return fmt.Errorf("read Redis password: %w", err)
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr:     value.RateLimit.RedisAddress,
		Username: value.RateLimit.RedisUsername,
		Password: redisPassword,
		DB:       value.RateLimit.RedisDatabase,
	})
	defer redisClient.Close()
	rateLimitTimeout := time.Duration(value.RateLimit.OperationTimeoutMS) * time.Millisecond
	rateLimiter, err := redislimit.New(redisClient, redislimit.Config{
		Prefix:           value.RateLimit.RedisKeyPrefix,
		Requests:         value.RateLimit.Requests,
		Window:           time.Duration(value.RateLimit.WindowMS) * time.Millisecond,
		OperationTimeout: rateLimitTimeout,
	})
	if err != nil {
		return err
	}
	pingCtx, cancelPing := context.WithTimeout(ctx, rateLimitTimeout)
	err = redisClient.Ping(pingCtx).Err()
	cancelPing()
	if err != nil {
		return fmt.Errorf("ping rate-limit Redis: %w", err)
	}
	handler, err := gateway.New(enginev1.NewEngineCommandServiceClient(engineConn), humanv1.NewHumanRuntimeServiceClient(humanConn), rateLimiter, value)
	if err != nil {
		return err
	}
	engineConn.Connect()
	humanConn.Connect()
	healthHandler := platformhealth.Handler(
		value.Health.ReadinessTimeout(),
		connectionReady(engineConn),
		connectionReady(humanConn),
		func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },
	)
	routes := http.NewServeMux()
	routes.Handle("/livez", healthHandler)
	routes.Handle("/readyz", healthHandler)
	routes.Handle("/", handler.Routes())
	server := &http.Server{Addr: value.ListenAddress, Handler: platformtelemetry.HTTPHandler(value.Telemetry.ServiceName, routes), ReadHeaderTimeout: value.HTTP.ReadHeaderTimeout(), ReadTimeout: value.HTTP.ReadTimeout(), WriteTimeout: value.HTTP.WriteTimeout(), IdleTimeout: value.HTTP.IdleTimeout()}
	errorsChannel := make(chan error, 1)
	go func() {
		errorsChannel <- server.ListenAndServeTLS(value.PublicTLS.Certificate, value.PublicTLS.PrivateKey)
	}()
	slog.Info("api-gateway started", "listen_address", value.ListenAddress)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), value.HTTP.ShutdownTimeout())
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case runErr := <-errorsChannel:
		if errors.Is(runErr, http.ErrServerClosed) {
			return nil
		}
		return runErr
	}
}

func readOptionalSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value = bytes.TrimSuffix(value, []byte("\n"))
	value = bytes.TrimSuffix(value, []byte("\r"))
	return string(value), nil
}

func reliabilityInterceptor(value config.Reliability) (grpc.UnaryClientInterceptor, error) {
	retryable, err := platformgrpc.RetryableCodes(value.RetryableCodes)
	if err != nil {
		return nil, err
	}
	return platformgrpc.UnaryClientInterceptor(platformgrpc.Config{
		MaxAttempts:      value.MaxAttempts,
		InitialBackoff:   time.Duration(value.InitialBackoffMS) * time.Millisecond,
		MaxBackoff:       time.Duration(value.MaxBackoffMS) * time.Millisecond,
		AttemptTimeout:   time.Duration(value.AttemptTimeoutMS) * time.Millisecond,
		FailureThreshold: value.FailureThreshold,
		OpenDuration:     time.Duration(value.OpenDurationMS) * time.Millisecond,
		RetryableCodes:   retryable,
	})
}

func connectionReady(connection *grpc.ClientConn) platformhealth.Check {
	return func(context.Context) error {
		if connection.GetState() != connectivity.Ready {
			return fmt.Errorf("upstream connection state is %s", connection.GetState())
		}
		return nil
	}
}

func certPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("upstream CA contains no certificates")
	}
	return pool, nil
}
