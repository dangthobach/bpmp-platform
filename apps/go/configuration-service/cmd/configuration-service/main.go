package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/grpcapi"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/httpapi"
	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/application"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	platformtelemetry "github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

func main() {
	path := flag.String("config", os.Getenv("CONFIGURATION_SERVICE_CONFIG"), "path to configuration-service JSON configuration")
	flag.Parse()
	if *path == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*path); err != nil {
		slog.Error("configuration-service stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string) error {
	config, err := loadConfig(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tracerProvider, err := platformtelemetry.Start(ctx, platformtelemetry.Config{
		ServiceName: config.Telemetry.ServiceName, ServiceVersion: config.Telemetry.ServiceVersion,
		Endpoint: config.Telemetry.Endpoint, Insecure: config.Telemetry.Insecure,
		SampleRatio:   config.Telemetry.SampleRatio,
		ExportTimeout: milliseconds(config.Telemetry.ExportTimeoutMS),
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), milliseconds(config.Telemetry.ExportTimeoutMS))
		defer cancel()
		if shutdownErr := tracerProvider.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Error("flush OpenTelemetry", "error", shutdownErr)
		}
	}()

	pool, err := pgxpool.New(ctx, config.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if config.ApplyMigrations {
		migration, readErr := os.ReadFile(config.MigrationPath)
		if readErr != nil {
			return readErr
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply migration: %w", err)
		}
	}

	store, err := postgresadapter.New(pool)
	if err != nil {
		return err
	}
	service, err := application.New(store, application.Config{
		ReadCapability: config.Identity.ReadCapability, ManageCapability: config.Identity.ManageCapability,
		DefaultPageSize: config.API.DefaultPageSize, MaxPageSize: config.API.MaxPageSize,
	})
	if err != nil {
		return err
	}
	verifier, err := httpapi.NewVerifier(httpapi.IdentityConfig{
		JWKSPath: config.Identity.JWKSPath, Issuers: config.Identity.Issuers,
		Audiences: config.Identity.Audiences, Algorithms: config.Identity.Algorithms,
		MaxTokenBytes: config.Identity.MaxTokenBytes, MaxJWKSKeys: config.Identity.MaxJWKSKeys,
		ClockSkew: milliseconds(config.Identity.ClockSkewMS),
	})
	if err != nil {
		return err
	}
	api, err := httpapi.NewHandler(service, verifier, httpapi.HandlerConfig{MaxBodyBytes: config.API.MaxBodyBytes})
	if err != nil {
		return err
	}
	clientCABytes, err := os.ReadFile(config.TLS.ClientCA)
	if err != nil {
		return fmt.Errorf("read client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(clientCABytes) {
		return errors.New("client CA contains no certificates")
	}
	tlsSettings := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
	}
	resolver, err := grpcapi.New(service)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsSettings.Clone())),
		grpc.MaxRecvMsgSize(config.GRPC.MaxReceiveBytes),
		grpc.MaxSendMsgSize(config.GRPC.MaxSendBytes),
	)
	configurationv1.RegisterConfigurationResolverServiceServer(grpcServer, resolver)
	grpcListener, err := net.Listen("tcp", config.GRPC.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen configuration gRPC: %w", err)
	}
	defer grpcListener.Close()
	health := platformhealth.Handler(
		milliseconds(config.API.ReadinessTimeoutMS),
		func(ctx context.Context) error { return pool.Ping(ctx) },
	)
	routes := http.NewServeMux()
	routes.Handle("/livez", health)
	routes.Handle("/readyz", health)
	routes.Handle("/", api.Routes())
	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           platformtelemetry.HTTPHandler(config.Telemetry.ServiceName, routes),
		ReadHeaderTimeout: milliseconds(config.API.ReadHeaderTimeoutMS),
		ReadTimeout:       milliseconds(config.API.ReadTimeoutMS),
		WriteTimeout:      milliseconds(config.API.WriteTimeoutMS),
		IdleTimeout:       milliseconds(config.API.IdleTimeoutMS),
		TLSConfig:         tlsSettings,
	}
	errs := make(chan error, 2)
	go func() {
		errs <- server.ListenAndServeTLS(config.TLS.Certificate, config.TLS.PrivateKey)
	}()
	go func() {
		errs <- grpcServer.Serve(grpcListener)
	}()
	slog.Info(
		"configuration-service started",
		"http_listen_address", config.ListenAddress,
		"grpc_listen_address", config.GRPC.ListenAddress,
	)
	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), milliseconds(config.API.ShutdownTimeoutMS))
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case runErr := <-errs:
		if errors.Is(runErr, http.ErrServerClosed) || errors.Is(runErr, grpc.ErrServerStopped) {
			return nil
		}
		return runErr
	}
}
