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

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/eventprojection"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/grpcapi"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/kafkaconsumer"
	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/runtimepolicy"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	platformruntimeconfig "github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
	platformtelemetry "github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

func main() {
	configPath := flag.String("config", os.Getenv("PROJECTION_SERVICE_CONFIG"), "path to projection-service JSON configuration")
	flag.Parse()
	if *configPath == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*configPath); err != nil {
		slog.Error("projection-service stopped", "error", err)
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
	tracer, err := platformtelemetry.Start(ctx, platformtelemetry.Config{
		ServiceName: value.Telemetry.ServiceName, ServiceVersion: value.Telemetry.ServiceVersion,
		Endpoint: value.Telemetry.Endpoint, Insecure: value.Telemetry.Insecure,
		SampleRatio: value.Telemetry.SampleRatio, ExportTimeout: milliseconds(value.Telemetry.ExportTimeoutMS),
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), milliseconds(value.Telemetry.ExportTimeoutMS))
		defer cancel()
		if shutdownErr := tracer.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Error("flush projection telemetry", "error", shutdownErr)
		}
	}()

	pool, err := pgxpool.New(ctx, value.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open projection PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping projection PostgreSQL: %w", err)
	}
	if value.ApplyMigrations {
		migration, readErr := os.ReadFile(value.MigrationPath)
		if readErr != nil {
			return readErr
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply projection migration: %w", err)
		}
	}

	serverTLS, configurationTLS, err := loadTLS(value.TLS)
	if err != nil {
		return err
	}
	configurationConn, err := grpc.NewClient(
		value.RuntimeConfig.ResolverAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(configurationTLS)),
		grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes),
			grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes),
		),
	)
	if err != nil {
		return fmt.Errorf("connect projection configuration resolver: %w", err)
	}
	defer configurationConn.Close()
	configurationConn.Connect()
	configurationKafka, err := platformruntimeconfig.NewKafkaClient(value.RuntimeConfig.Kafka)
	if err != nil {
		return err
	}
	defer configurationKafka.Close()
	cache, err := platformruntimeconfig.NewCache(
		configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_PROJECTION,
	)
	if err != nil {
		return err
	}
	reloader, err := platformruntimeconfig.New(platformruntimeconfig.Config{
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_PROJECTION,
		PlatformReference:    value.RuntimeConfig.PlatformReference,
		EnvironmentReference: value.RuntimeConfig.EnvironmentReference,
		InitialTenantIDs:     append([]string(nil), value.RuntimeConfig.InitialTenantIDs...),
		ResolveTimeout:       milliseconds(value.RuntimeConfig.ResolveTimeoutMS),
		Kafka:                value.RuntimeConfig.Kafka,
	}, configurationv1.NewConfigurationResolverServiceClient(configurationConn),
		configurationKafka, cache)
	if err != nil {
		return err
	}
	if err = reloader.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap projection runtime configuration: %w", err)
	}
	policies, err := runtimepolicy.New(cache)
	if err != nil {
		return err
	}
	store, err := postgresadapter.New(pool)
	if err != nil {
		return err
	}
	service, err := application.NewService(store, policies)
	if err != nil {
		return err
	}
	handler, err := eventprojection.New(service, value.Kafka.ConsumerGroup)
	if err != nil {
		return err
	}
	eventKafka, err := platformruntimeconfig.NewKafkaClient(value.Kafka)
	if err != nil {
		return err
	}
	defer eventKafka.Close()
	consumer, err := kafkaconsumer.New(eventKafka, handler, func() (int, error) {
		return policies.MinimumConsumeBatchSize(value.RuntimeConfig.InitialTenantIDs)
	})
	if err != nil {
		return err
	}
	query, err := grpcapi.New(service)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", value.ListenAddress)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.StatsHandler(platformtelemetry.GRPCServerStatsHandler()),
		grpc.MaxRecvMsgSize(value.GRPC.MaxReceiveBytes),
		grpc.MaxSendMsgSize(value.GRPC.MaxSendBytes),
	)
	projectionv1.RegisterProjectionQueryServiceServer(grpcServer, query)

	health := platformhealth.Handler(
		milliseconds(value.Health.ReadinessTimeoutMS),
		func(ctx context.Context) error { return pool.Ping(ctx) },
		func(ctx context.Context) error { return eventKafka.Ping(ctx) },
		func(ctx context.Context) error { return configurationKafka.Ping(ctx) },
		func(context.Context) error { return cache.Ready(value.RuntimeConfig.InitialTenantIDs) },
	)
	healthServer := &http.Server{
		Addr: value.HealthAddress, Handler: health,
		ReadHeaderTimeout: milliseconds(value.Health.ReadinessTimeoutMS),
		ReadTimeout:       milliseconds(value.Health.ReadinessTimeoutMS),
		WriteTimeout:      milliseconds(value.Health.ReadinessTimeoutMS),
		IdleTimeout:       milliseconds(value.Health.ReadinessTimeoutMS),
	}
	errs := make(chan error, 4)
	go func() { errs <- consumer.Run(ctx) }()
	go func() { errs <- reloader.Run(ctx) }()
	go func() { errs <- grpcServer.Serve(listener) }()
	go func() { errs <- healthServer.ListenAndServe() }()
	slog.Info("projection-service started", "listen_address", value.ListenAddress, "health_address", value.HealthAddress)
	select {
	case <-ctx.Done():
	case runErr := <-errs:
		if !errors.Is(runErr, http.ErrServerClosed) {
			err = runErr
		}
	}
	grpcServer.GracefulStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), milliseconds(value.Health.ShutdownTimeoutMS))
	defer cancel()
	return errors.Join(err, healthServer.Shutdown(shutdownCtx))
}

func loadTLS(value tlsConfig) (*tls.Config, *tls.Config, error) {
	serverPair, err := tls.LoadX509KeyPair(value.ServerCertificate, value.ServerPrivateKey)
	if err != nil {
		return nil, nil, err
	}
	clientPair, err := tls.LoadX509KeyPair(value.ClientCertificate, value.ClientPrivateKey)
	if err != nil {
		return nil, nil, err
	}
	clientRoots, err := certificatePool(value.ClientCA)
	if err != nil {
		return nil, nil, err
	}
	configurationRoots, err := certificatePool(value.ConfigurationCA)
	if err != nil {
		return nil, nil, err
	}
	return &tls.Config{
			MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs: clientRoots, Certificates: []tls.Certificate{serverPair},
		}, &tls.Config{
			MinVersion: tls.VersionTLS13, ServerName: value.ConfigurationServerName,
			RootCAs: configurationRoots, Certificates: []tls.Certificate{clientPair},
		}, nil
}

func certificatePool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("projection CA file contains no certificates")
	}
	return pool, nil
}
