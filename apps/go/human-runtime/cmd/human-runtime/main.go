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
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/runtimepolicy"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/workloadsecurity"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	platformgrpc "github.com/dangthobach/bpmp-platform/go/platform/grpcclient"
	platformgrpcserver "github.com/dangthobach/bpmp-platform/go/platform/grpcserver"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	platformruntimeconfig "github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/servermiddleware"
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
	configurationTLS := clientTLS.Clone()
	configurationTLS.ServerName = config.TLS.ConfigurationServerName
	configurationConn, err := grpc.NewClient(
		config.RuntimeConfig.ResolverAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(configurationTLS)),
		grpc.WithUnaryInterceptor(requestmeta.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(requestmeta.StreamClientInterceptor()),
		grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(config.GRPC.MaxReceiveBytes),
			grpc.MaxCallSendMsgSize(config.GRPC.MaxSendBytes),
		),
	)
	if err != nil {
		return fmt.Errorf("connect configuration resolver: %w", err)
	}
	defer configurationConn.Close()
	configurationConn.Connect()
	configurationKafka, err := platformruntimeconfig.NewKafkaClient(config.RuntimeConfig.Kafka)
	if err != nil {
		return err
	}
	defer configurationKafka.Close()
	configurationCache, err := platformruntimeconfig.NewCache(
		configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME,
	)
	if err != nil {
		return err
	}
	configurationReloader, err := platformruntimeconfig.New(platformruntimeconfig.Config{
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME,
		PlatformReference:    config.RuntimeConfig.PlatformReference,
		EnvironmentReference: config.RuntimeConfig.EnvironmentReference,
		InitialTenantIDs:     []string{config.RuntimeConfig.TenantID},
		ResolveTimeout:       time.Duration(config.RuntimeConfig.ResolveTimeoutMS) * time.Millisecond,
		Kafka:                config.RuntimeConfig.Kafka,
	}, configurationv1.NewConfigurationResolverServiceClient(configurationConn),
		configurationKafka, configurationCache)
	if err != nil {
		return err
	}
	bootstrapStarted := time.Now()
	slog.Info("bootstrapping Human Runtime configuration", "tenant_id", config.RuntimeConfig.TenantID)
	if err = configurationReloader.Bootstrap(ctx); err != nil {
		return err
	}
	slog.Info("Human Runtime configuration ready", "elapsed", time.Since(bootstrapStarted))
	policyProvider, err := runtimepolicy.New(configurationCache)
	if err != nil {
		return err
	}
	engineInterceptor, err := dynamicReliabilityInterceptor()
	if err != nil {
		return err
	}
	engineConn, err := grpc.NewClient(config.EngineAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
		grpc.WithChainUnaryInterceptor(requestmeta.UnaryClientInterceptor(), engineInterceptor),
		grpc.WithStreamInterceptor(requestmeta.StreamClientInterceptor()),
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
	}, store)
	if err != nil {
		return err
	}
	engineClient, err := enginegrpc.New(
		enginev1.NewEngineCommandServiceClient(engineConn),
		security,
	)
	if err != nil {
		return err
	}
	service, err := application.NewService(store, engineClient, policyProvider)
	if err != nil {
		return err
	}
	verifier, err := loadActorVerifier(config.Identity, store)
	if err != nil {
		return err
	}
	humanServer, err := humangrpc.New(service, store, verifier, policyProvider, time.Now)
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
		func(ctx context.Context) error { return configurationKafka.Ping(ctx) },
		func(context.Context) error {
			return configurationCache.Ready([]string{config.RuntimeConfig.TenantID})
		},
		func(context.Context) error {
			if engineConn.GetState() != connectivity.Ready {
				return fmt.Errorf("engine connection state is %s", engineConn.GetState())
			}
			return nil
		},
	)
	healthTransport, err := servermiddleware.NewHTTP(servermiddleware.HTTPConfig{
		Service:        config.Telemetry.ServiceName + ".health",
		RequestTimeout: config.Health.readinessTimeout(), SecurityHeaders: true,
	}, healthHandler)
	if err != nil {
		return err
	}
	healthServer := &http.Server{
		Addr:              config.Health.ListenAddress,
		Handler:           platformtelemetry.HTTPHandler(config.Telemetry.ServiceName+".health", healthTransport),
		ReadHeaderTimeout: config.Health.readinessTimeout(),
		ReadTimeout:       config.Health.readinessTimeout(),
		WriteTimeout:      config.Health.readinessTimeout(),
		IdleTimeout:       config.Health.readinessTimeout(),
		MaxHeaderBytes:    config.Health.MaxHeaderBytes,
	}
	projection, err := eventprojection.New(service)
	if err != nil {
		return err
	}
	consumer, err := kafkaconsumer.NewDynamic(
		kafkaClient,
		projection,
		func() (int, error) {
			policy, policyErr := policyProvider.WorkerPolicy()
			return policy.ProjectionBatchSize, policyErr
		},
	)
	if err != nil {
		return err
	}
	escalationPublisher, err := kafkapublisher.NewEscalationPublisher(kafkaClient, config.Kafka.EscalationTopic)
	if err != nil {
		return err
	}
	escalationWorker, err := application.NewDynamicEscalationWorker(
		store,
		escalationPublisher,
		config.Escalation.WorkerID,
		func() (int, time.Duration, time.Duration, error) {
			policy, policyErr := policyProvider.WorkerPolicy()
			return policy.EscalationBatchSize, policy.EscalationLease,
				policy.EscalationRetry, policyErr
		},
	)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return err
	}
	workloadAuthorizer, err := servermiddleware.NewMTLSAuthorizer(servermiddleware.MTLSConfig{
		AllowedCertificateSHA256: config.GRPC.AuthorizedClientCertificateSHA256,
		AllowedMethods:           config.GRPC.AuthorizedMethods,
	})
	if err != nil {
		return err
	}
	grpcAdmissionLimiter, err := servermiddleware.NewTokenBucket(
		config.GRPC.AdmissionRateRPS, config.GRPC.AdmissionBurst,
	)
	if err != nil {
		return err
	}
	unaryMiddleware, err := servermiddleware.UnaryServerInterceptor(servermiddleware.GRPCConfig{
		Service:        config.Telemetry.ServiceName,
		UnaryTimeout:   time.Duration(config.GRPC.UnaryTimeoutMS) * time.Millisecond,
		AuthorizeUnary: workloadAuthorizer.AuthorizeUnary,
		RateLimitUnary: func(context.Context, string, any) error {
			if !grpcAdmissionLimiter.Allow() {
				return errors.New("gRPC admission limit exceeded")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	streamMiddleware, err := servermiddleware.StreamServerInterceptor(servermiddleware.GRPCConfig{
		Service: config.Telemetry.ServiceName, AuthorizeStream: workloadAuthorizer.AuthorizeStream,
		RateLimitStream: func(context.Context, string) error {
			if !grpcAdmissionLimiter.Allow() {
				return errors.New("gRPC admission limit exceeded")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.ChainUnaryInterceptor(unaryMiddleware),
		grpc.ChainStreamInterceptor(streamMiddleware),
		grpc.StatsHandler(platformtelemetry.GRPCServerStatsHandler()),
		grpc.MaxRecvMsgSize(config.GRPC.MaxReceiveBytes),
		grpc.MaxSendMsgSize(config.GRPC.MaxSendBytes),
	)
	humanv1.RegisterHumanRuntimeServiceServer(grpcServer, humanServer)
	platformgrpcserver.RegisterReflection(grpcServer, config.GRPC.ReflectionEnabled)

	errorsChannel := make(chan error, 5)
	go func() { errorsChannel <- consumer.Run(ctx) }()
	go func() { errorsChannel <- runEscalations(ctx, escalationWorker, policyProvider) }()
	go func() { errorsChannel <- configurationReloader.Run(ctx) }()
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

func dynamicReliabilityInterceptor() (grpc.UnaryClientInterceptor, error) {
	return platformgrpc.DynamicUnaryClientInterceptorForContext(func(ctx context.Context) (platformgrpc.Config, error) {
		policy, err := enginegrpc.RuntimePolicyFromContext(ctx)
		if err != nil {
			return platformgrpc.Config{}, err
		}
		retryable, err := platformgrpc.RetryableCodes(policy.EngineRetryableCodes)
		if err != nil {
			return platformgrpc.Config{}, err
		}
		return platformgrpc.Config{
			MaxAttempts:      policy.EngineRetry.MaxAttempts,
			InitialBackoff:   policy.EngineRetry.InitialBackoff,
			MaxBackoff:       policy.EngineRetry.MaxBackoff,
			MultiplierMillis: policy.EngineRetry.MultiplierMillis,
			AttemptTimeout:   policy.EngineCommandTimeout,
			FailureThreshold: policy.EngineCircuitThreshold,
			OpenDuration:     policy.EngineCircuitOpen,
			RetryableCodes:   retryable,
		}, nil
	})
}

func runEscalations(
	ctx context.Context,
	worker *application.EscalationWorker,
	policies *runtimepolicy.Provider,
) error {
	for {
		policy, err := policies.WorkerPolicy()
		if err != nil {
			return err
		}
		timer := time.NewTimer(policy.EscalationPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case now := <-timer.C:
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
