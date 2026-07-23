package main

import (
	"context"
	"crypto/ed25519"
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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/actorverifier"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/enginegrpc"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/eventprojection"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/humangrpc"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/kafkaconsumer"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/kafkapublisher"
	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/workloadsecurity"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	platformgrpc "github.com/dangthobach/bpmp-platform/go/platform/grpcclient"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	platformtelemetry "github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

func main() {
	configPath := flag.String("config", os.Getenv("HUMAN_RUNTIME_CONFIG"), "path to human-runtime JSON configuration")
	flag.Parse()
	if *configPath == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*configPath); err != nil {
		slog.Error("human-runtime stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	config, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tracerProvider, err := platformtelemetry.Start(ctx, platformtelemetry.Config{
		ServiceName:    config.Telemetry.ServiceName,
		ServiceVersion: config.Telemetry.ServiceVersion,
		Endpoint:       config.Telemetry.Endpoint,
		Insecure:       config.Telemetry.Insecure,
		SampleRatio:    config.Telemetry.SampleRatio,
		ExportTimeout:  config.Telemetry.exportTimeout(),
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Telemetry.exportTimeout())
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
	store, err := postgresadapter.NewStore(pool)
	if err != nil {
		return err
	}

	clientTLS, serverTLS, err := loadTLS(config.TLS)
	if err != nil {
		return err
	}
	engineInterceptor, err := reliabilityInterceptor(config.Reliability)
	if err != nil {
		return err
	}
	engineConn, err := grpc.NewClient(config.EngineAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
		grpc.WithUnaryInterceptor(engineInterceptor),
		grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(config.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(config.GRPC.MaxSendBytes)),
	)
	if err != nil {
		return fmt.Errorf("connect engine: %w", err)
	}
	defer engineConn.Close()
	engineConn.Connect()

	privateKey, err := readPrivateKey(config.Workload.PrivateKeyPath)
	if err != nil {
		return err
	}
	security, err := workloadsecurity.New(workloadsecurity.Config{
		WorkloadID: config.Workload.ID, SigningKeyID: config.Workload.SigningKeyID,
		PrivateKey: privateKey, ProofTTL: time.Duration(config.Workload.ProofTTLMS) * time.Millisecond,
	}, store, time.Now)
	if err != nil {
		return err
	}
	engineClient, err := enginegrpc.New(enginev1.NewEngineCommandServiceClient(engineConn), security)
	if err != nil {
		return err
	}
	service, err := application.NewService(store, engineClient)
	if err != nil {
		return err
	}
	verifier, err := loadActorVerifier(config.Identity, store)
	if err != nil {
		return err
	}
	humanServer, err := humangrpc.New(service, store, verifier, time.Now)
	if err != nil {
		return err
	}

	kafkaClient, err := kgo.NewClient(
		kgo.SeedBrokers(config.Kafka.Brokers...),
		kgo.ConsumerGroup(config.Kafka.ConsumerGroup),
		kgo.ConsumeTopics(config.Kafka.CommittedEventTopic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return err
	}
	defer kafkaClient.Close()
	healthHandler := platformhealth.Handler(
		config.Health.readinessTimeout(),
		func(ctx context.Context) error { return pool.Ping(ctx) },
		func(ctx context.Context) error { return kafkaClient.Ping(ctx) },
		func(context.Context) error {
			if engineConn.GetState() != connectivity.Ready {
				return fmt.Errorf("engine connection state is %s", engineConn.GetState())
			}
			return nil
		},
	)
	healthServer := &http.Server{
		Addr:              config.Health.ListenAddress,
		Handler:           healthHandler,
		ReadHeaderTimeout: config.Health.readinessTimeout(),
		ReadTimeout:       config.Health.readinessTimeout(),
		WriteTimeout:      config.Health.readinessTimeout(),
		IdleTimeout:       config.Health.readinessTimeout(),
	}
	projection, err := eventprojection.New(service)
	if err != nil {
		return err
	}
	consumer, err := kafkaconsumer.New(kafkaClient, projection, config.Kafka.BatchSize)
	if err != nil {
		return err
	}
	escalationPublisher, err := kafkapublisher.NewEscalationPublisher(kafkaClient, config.Kafka.EscalationTopic)
	if err != nil {
		return err
	}
	escalationWorker, err := application.NewEscalationWorker(store, escalationPublisher, config.Escalation.WorkerID, config.Escalation.BatchSize, config.Escalation.lease(), config.Escalation.retry())
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.StatsHandler(platformtelemetry.GRPCServerStatsHandler()),
		grpc.MaxRecvMsgSize(config.GRPC.MaxReceiveBytes),
		grpc.MaxSendMsgSize(config.GRPC.MaxSendBytes),
	)
	humanv1.RegisterHumanRuntimeServiceServer(grpcServer, humanServer)

	errorsChannel := make(chan error, 4)
	go func() { errorsChannel <- consumer.Run(ctx) }()
	go func() { errorsChannel <- runEscalations(ctx, escalationWorker, config.Escalation.poll()) }()
	go func() { errorsChannel <- grpcServer.Serve(listener) }()
	go func() { errorsChannel <- healthServer.ListenAndServe() }()
	slog.Info("human-runtime started", "listen_address", config.ListenAddress)
	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Health.readinessTimeout())
		defer cancel()
		return healthServer.Shutdown(shutdownCtx)
	case runErr := <-errorsChannel:
		grpcServer.GracefulStop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Health.readinessTimeout())
		defer cancel()
		if shutdownErr := healthServer.Shutdown(shutdownCtx); shutdownErr != nil {
			return errors.Join(runErr, shutdownErr)
		}
		if errors.Is(runErr, http.ErrServerClosed) {
			return nil
		}
		return runErr
	}
}

func reliabilityInterceptor(value reliabilityConfig) (grpc.UnaryClientInterceptor, error) {
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

func runEscalations(ctx context.Context, worker *application.EscalationWorker, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := worker.RunOnce(ctx, now.UTC()); err != nil {
				return err
			}
		}
	}
}

func loadActorVerifier(config identityConfig, revocations actorverifier.RevokeEpochProvider) (*actorverifier.Verifier, error) {
	jwks, err := os.ReadFile(config.JWKSPath)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey, len(config.InternalKeys))
	for id, path := range config.InternalKeys {
		bytes, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		if len(bytes) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("internal key %s must contain 32 bytes", id)
		}
		keys[id] = ed25519.PublicKey(bytes)
	}
	return actorverifier.New(actorverifier.Config{
		Issuers: set(config.Issuers), Audiences: set(config.Audiences), AllowedJWTMethods: set(config.AllowedJWTMethods),
		WorkloadID: config.WorkloadID, MaxProofBytes: config.MaxProofBytes, MaxJWKSKeys: config.MaxJWKSKeys,
		MaxRoles: config.MaxRoles, MaxCapabilities: config.MaxCapabilities, ClockSkew: time.Duration(config.ClockSkewMS) * time.Millisecond,
	}, jwks, keys, revocations)
}

func set(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(bytes) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(bytes), nil
	}
	if len(bytes) != ed25519.PrivateKeySize {
		return nil, errors.New("workload signing key must contain a 32-byte seed or 64-byte private key")
	}
	return ed25519.PrivateKey(bytes), nil
}

func loadTLS(config tlsConfig) (*tls.Config, *tls.Config, error) {
	clientPair, err := tls.LoadX509KeyPair(config.ClientCertificate, config.ClientPrivateKey)
	if err != nil {
		return nil, nil, err
	}
	serverPair, err := tls.LoadX509KeyPair(config.ServerCertificate, config.ServerPrivateKey)
	if err != nil {
		return nil, nil, err
	}
	engineRoots, err := certificatePool(config.EngineCA)
	if err != nil {
		return nil, nil, err
	}
	clientRoots, err := certificatePool(config.ClientCA)
	if err != nil {
		return nil, nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, ServerName: config.EngineServerName, RootCAs: engineRoots, Certificates: []tls.Certificate{clientPair}},
		&tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots, Certificates: []tls.Certificate{serverPair}}, nil
}

func certificatePool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("CA file contains no certificates")
	}
	return pool, nil
}
