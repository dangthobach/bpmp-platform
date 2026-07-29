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
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/grpcapi"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/httpapi"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/kafkapublisher"
	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/tenantconsumer"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/application"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	platformgrpcserver "github.com/dangthobach/bpmp-platform/go/platform/grpcserver"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	platformruntimeconfig "github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
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
		if err = applyMigrations(ctx, pool, config.MigrationPath); err != nil {
			return err
		}
	}

	store, err := postgresadapter.New(pool)
	if err != nil {
		return err
	}
	kafkaPublisher, err := kafkapublisher.New(config.Kafka)
	if err != nil {
		return fmt.Errorf("configure Kafka publisher: %w", err)
	}
	defer kafkaPublisher.Close()
	readinessKafka, err := kafkapublisher.New(config.TenantReadiness)
	if err != nil {
		return fmt.Errorf("configure tenant readiness publisher: %w", err)
	}
	defer readinessKafka.Close()
	tenantKafka, err := platformruntimeconfig.NewKafkaClient(config.TenantLifecycle)
	if err != nil {
		return fmt.Errorf("configure tenant lifecycle consumer: %w", err)
	}
	defer tenantKafka.Close()
	tenantLifecycle, err := tenantconsumer.New(
		tenantKafka,
		store,
		config.TenantLifecycle.BatchSize,
	)
	if err != nil {
		return err
	}
	publisher, err := application.NewPublisher(store, kafkaPublisher, application.PublisherConfig{
		WorkerID: config.Outbox.WorkerID, BatchSize: config.Outbox.BatchSize,
		LeaseDuration:     milliseconds(config.Outbox.LeaseDurationMS),
		InitialRetryDelay: milliseconds(config.Outbox.InitialRetryDelayMS),
		MaxRetryDelay:     milliseconds(config.Outbox.MaxRetryDelayMS),
		RetryMultiplier:   config.Outbox.RetryMultiplier,
	})
	if err != nil {
		return err
	}
	readinessPublisher, err := application.NewTenantReadinessPublisher(
		store,
		readinessKafka,
		application.PublisherConfig{
			WorkerID:          config.Outbox.WorkerID + "-tenant-readiness",
			BatchSize:         config.Outbox.BatchSize,
			LeaseDuration:     milliseconds(config.Outbox.LeaseDurationMS),
			InitialRetryDelay: milliseconds(config.Outbox.InitialRetryDelayMS),
			MaxRetryDelay:     milliseconds(config.Outbox.MaxRetryDelayMS),
			RetryMultiplier:   config.Outbox.RetryMultiplier,
		},
	)
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
	serverCertificate, err := tls.LoadX509KeyPair(
		config.TLS.Certificate,
		config.TLS.PrivateKey,
	)
	if err != nil {
		return fmt.Errorf("load configuration service certificate: %w", err)
	}
	tlsSettings := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		Certificates: []tls.Certificate{serverCertificate},
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
	platformgrpcserver.RegisterReflection(grpcServer, config.GRPC.ReflectionEnabled)
	grpcListener, err := net.Listen("tcp", config.GRPC.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen configuration gRPC: %w", err)
	}
	defer grpcListener.Close()
	health := platformhealth.Handler(
		milliseconds(config.API.ReadinessTimeoutMS),
		func(ctx context.Context) error { return pool.Ping(ctx) },
		func(ctx context.Context) error { return kafkaPublisher.Ping(ctx) },
		func(ctx context.Context) error { return tenantKafka.Ping(ctx) },
		func(ctx context.Context) error { return readinessKafka.Ping(ctx) },
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
	errs := make(chan error, 5)
	go func() {
		errs <- server.ListenAndServeTLS(config.TLS.Certificate, config.TLS.PrivateKey)
	}()
	go func() {
		errs <- grpcServer.Serve(grpcListener)
	}()
	go func() {
		errs <- runPublisher(ctx, publisher, milliseconds(config.Outbox.PollIntervalMS))
	}()
	go func() {
		errs <- tenantLifecycle.Run(ctx)
	}()
	go func() {
		errs <- runTenantReadinessPublisher(
			ctx,
			readinessPublisher,
			milliseconds(config.Outbox.PollIntervalMS),
		)
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

type migrationExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func applyMigrations(ctx context.Context, executor migrationExecutor, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	paths := []string{path}
	if info.IsDir() {
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return readErr
		}
		paths = paths[:0]
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
				paths = append(paths, filepath.Join(path, entry.Name()))
			}
		}
		sort.Strings(paths)
	}
	if len(paths) == 0 {
		return errors.New("configuration migration path contains no SQL files")
	}
	for _, migrationPath := range paths {
		migration, readErr := os.ReadFile(migrationPath)
		if readErr != nil {
			return readErr
		}
		if _, err = executor.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply migration %s: %w", filepath.Base(migrationPath), err)
		}
	}
	return nil
}

func runPublisher(ctx context.Context, publisher *application.Publisher, pollInterval time.Duration) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			published, err := publisher.RunOnce(ctx)
			if err != nil {
				slog.Error("publish configuration outbox", "error", err)
			} else if published > 0 {
				slog.Info("published configuration outbox batch", "count", published)
			}
			timer.Reset(pollInterval)
		}
	}
}

func runTenantReadinessPublisher(
	ctx context.Context,
	publisher *application.TenantReadinessPublisher,
	pollInterval time.Duration,
) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			published, err := publisher.RunOnce(ctx)
			if err != nil {
				slog.Error("publish tenant readiness outbox", "error", err)
			} else if published > 0 {
				slog.Info("published tenant readiness outbox batch", "count", published)
			}
			timer.Reset(pollInterval)
		}
	}
}
