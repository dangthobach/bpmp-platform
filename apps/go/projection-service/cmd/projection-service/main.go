package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/eventprojection"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/grpcapi"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/kafkaconsumer"
	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/queryauth"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/revokefeed"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/runtimepolicy"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	platformgrpcserver "github.com/dangthobach/bpmp-platform/go/platform/grpcserver"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	platformruntimeconfig "github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/servermiddleware"
	platformtelemetry "github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

const projectionMigrationNamespace = "projection-service"

var projectionMigrationName = regexp.MustCompile(`^([0-9]{3,})_[a-z0-9][a-z0-9_]*\.sql$`)

type projectionMigration struct {
	id       string
	version  int
	contents []byte
	checksum string
}

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
		if err = applyProjectionMigrations(ctx, pool, value.MigrationPath); err != nil {
			return err
		}
	}

	serverTLS, configurationTLS, err := loadTLS(value.TLS)
	if err != nil {
		return err
	}
	configurationConn, err := grpc.NewClient(
		value.RuntimeConfig.ResolverAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(configurationTLS)),
		grpc.WithUnaryInterceptor(requestmeta.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(requestmeta.StreamClientInterceptor()),
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
	actorVerifier, err := jwtauth.New(jwtauth.Config{
		JWKSPath: value.Identity.JWKSPath, Issuers: value.Identity.Issuers,
		Audiences: value.Identity.Audiences, Algorithms: value.Identity.Algorithms,
		MaxTokenBytes: value.Identity.MaxTokenBytes, MaxJWKSKeys: value.Identity.MaxJWKSKeys,
		ClockSkew: time.Duration(value.Identity.ClockSkewSeconds) * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create projection actor verifier: %w", err)
	}
	pdp, err := queryauth.NewHTTPPDP(value.QueryPDP, func() (string, error) {
		file, readErr := os.Open(value.QueryPDP.WorkloadTokenPath)
		if readErr != nil {
			return "", readErr
		}
		defer file.Close()
		raw, readErr := io.ReadAll(io.LimitReader(file, int64(value.Identity.MaxTokenBytes)+1))
		if len(raw) > value.Identity.MaxTokenBytes {
			return "", errors.New("PDP workload token exceeds limit")
		}
		return string(raw), readErr
	})
	if err != nil {
		return err
	}
	revokeStore, err := revokefeed.NewPostgresStore(pool)
	if err != nil {
		return err
	}
	revokeKafka, err := platformruntimeconfig.NewKafkaClient(value.QueryRevoke.Consumer)
	if err != nil {
		return err
	}
	defer revokeKafka.Close()
	dlqKafka, err := kafkaconfig.NewProducerClient(value.QueryRevoke.DLQ)
	if err != nil {
		return err
	}
	defer dlqKafka.Close()
	trust := make(map[string]ed25519.PublicKey, len(value.QueryRevoke.TrustKeys))
	for id, path := range value.QueryRevoke.TrustKeys {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if len(raw) != ed25519.PublicKeySize {
			return errors.New("revoke public key must contain 32 bytes")
		}
		trust[id] = ed25519.PublicKey(raw)
	}
	revokeConsumer, err := revokefeed.New(revokeKafka, dlqKafka, revokeStore, trust, value.QueryRevoke.Consumer.Topic, value.QueryRevoke.DLQ.Topic, value.QueryRevoke.Consumer.MaxMessageBytes, func() (revokefeed.Policy, error) {
		p, policyErr := policies.MinimumWorkerPolicy(value.RuntimeConfig.InitialTenantIDs)
		return revokefeed.Policy{BatchSize: min(p.ConsumeBatchSize, value.QueryRevoke.Consumer.BatchSize), RetryAttempts: p.QueryRevokeRetryAttempts, RetryBackoff: p.QueryRevokeRetryBackoff, OperationTimeout: p.QueryRevokeOperationTimeout}, policyErr
	})
	if err != nil {
		return err
	}
	queryAuthorizer, err := queryauth.WithDurableRevoke(queryauth.Config{
		MaxCapabilities: value.Identity.MaxCapabilities,
	}, actorVerifier, pdp, revokeStore)
	if err != nil {
		return err
	}
	service, err := application.NewService(
		store, policies, application.WithQueryAuthorizer(queryAuthorizer),
	)
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
	consumer, err := kafkaconsumer.New(
		eventKafka,
		handler,
		func() (kafkaconsumer.RuntimePolicy, error) {
			policy, policyErr := policies.MinimumWorkerPolicy(
				value.RuntimeConfig.InitialTenantIDs,
			)
			return kafkaconsumer.RuntimePolicy{
				ConsumeBatchSize:         policy.ConsumeBatchSize,
				RebuildBatchSize:         policy.RebuildBatchSize,
				RealtimePublishBatchSize: policy.RealtimePublishBatchSize,
				CheckpointFlush:          policy.CheckpointFlush,
				MaxLag:                   policy.MaxProjectionLag,
				CommitRetryAttempts:      policy.QueryRevokeRetryAttempts,
				CommitBackoff:            policy.QueryRevokeRetryBackoff,
				CommitTimeout:            policy.QueryRevokeOperationTimeout,
			}, policyErr
		},
	)
	if err != nil {
		return err
	}
	cursorCodec, err := loadCursorCodec(value.CursorSigning)
	if err != nil {
		return err
	}
	query, err := grpcapi.New(service, cursorCodec)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", value.ListenAddress)
	if err != nil {
		return err
	}
	workloadAuthorizer, err := servermiddleware.NewMTLSAuthorizer(servermiddleware.MTLSConfig{
		AllowedCertificateSHA256: value.GRPC.AuthorizedClientCertificateSHA256,
		AllowedMethods:           value.GRPC.AuthorizedMethods,
	})
	if err != nil {
		return err
	}
	grpcAdmissionLimiter, err := servermiddleware.NewTokenBucket(
		value.GRPC.AdmissionRateRPS, value.GRPC.AdmissionBurst,
	)
	if err != nil {
		return err
	}
	unaryMiddleware, err := servermiddleware.UnaryServerInterceptor(servermiddleware.GRPCConfig{
		Service:        value.Telemetry.ServiceName,
		UnaryTimeout:   milliseconds(value.GRPC.UnaryTimeoutMS),
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
		Service: value.Telemetry.ServiceName, AuthorizeStream: workloadAuthorizer.AuthorizeStream,
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
		grpc.MaxRecvMsgSize(value.GRPC.MaxReceiveBytes),
		grpc.MaxSendMsgSize(value.GRPC.MaxSendBytes),
	)
	projectionv1.RegisterProjectionQueryServiceServer(grpcServer, query)
	platformgrpcserver.RegisterReflection(grpcServer, value.GRPC.ReflectionEnabled)

	health := platformhealth.Handler(
		milliseconds(value.Health.ReadinessTimeoutMS),
		func(ctx context.Context) error { return pool.Ping(ctx) },
		func(ctx context.Context) error { return eventKafka.Ping(ctx) },
		func(ctx context.Context) error { return configurationKafka.Ping(ctx) },
		func(ctx context.Context) error { return revokeKafka.Ping(ctx) },
		func(ctx context.Context) error { return dlqKafka.Ping(ctx) },
		func(context.Context) error { return cache.Ready(value.RuntimeConfig.InitialTenantIDs) },
	)
	healthTransport, err := servermiddleware.NewHTTP(servermiddleware.HTTPConfig{
		Service:        value.Telemetry.ServiceName + ".health",
		RequestTimeout: milliseconds(value.Health.ReadinessTimeoutMS), SecurityHeaders: true,
	}, health)
	if err != nil {
		return err
	}
	healthServer := &http.Server{
		Addr: value.HealthAddress,
		Handler: platformtelemetry.HTTPHandler(
			value.Telemetry.ServiceName+".health",
			healthTransport,
		),
		ReadHeaderTimeout: milliseconds(value.Health.ReadinessTimeoutMS),
		ReadTimeout:       milliseconds(value.Health.ReadinessTimeoutMS),
		WriteTimeout:      milliseconds(value.Health.ReadinessTimeoutMS),
		IdleTimeout:       milliseconds(value.Health.ReadinessTimeoutMS),
		MaxHeaderBytes:    value.Health.MaxHeaderBytes,
	}
	errs := make(chan error, 6)
	go func() { errs <- service.RunExportWorker(ctx) }()
	go func() { errs <- revokeConsumer.Run(ctx) }()
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

func applyProjectionMigrations(ctx context.Context, pool *pgxpool.Pool, path string) error {
	migrations, err := loadProjectionMigrations(path)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin projection migrations: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The transaction-scoped lock serializes startup migrations without leaking a
	// session lock when a process crashes or its context is cancelled.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':' || current_schema() || ':projection-service-migrations', 0))`); err != nil {
		return fmt.Errorf("lock projection migrations: %w", err)
	}
	if err = ensureProjectionMigrationLedger(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT migration_id, checksum_sha256 FROM bpmp_schema_migrations WHERE namespace=$1`, projectionMigrationNamespace)
	if err != nil {
		return fmt.Errorf("read projection migration ledger: %w", err)
	}
	applied := make(map[string]string, len(migrations))
	for rows.Next() {
		var id, checksum string
		if err = rows.Scan(&id, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("scan projection migration ledger: %w", err)
		}
		applied[id] = checksum
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read projection migration ledger: %w", err)
	}
	rows.Close()

	available := make(map[string]projectionMigration, len(migrations))
	for _, migration := range migrations {
		available[migration.id] = migration
	}
	for id, recordedChecksum := range applied {
		migration, ok := available[id]
		if !ok {
			return fmt.Errorf("applied projection migration %s is missing from migration path", id)
		}
		if recordedChecksum != migration.checksum {
			return fmt.Errorf("projection migration checksum mismatch for %s: recorded=%s current=%s", id, recordedChecksum, migration.checksum)
		}
	}
	for _, migration := range migrations {
		if _, ok := applied[migration.id]; ok {
			continue
		}
		if _, err = tx.Exec(ctx, string(migration.contents)); err != nil {
			return fmt.Errorf("apply projection migration %s: %w", migration.id, err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO bpmp_schema_migrations(namespace,migration_id,checksum_sha256)
			VALUES($1,$2,$3)`, projectionMigrationNamespace, migration.id, migration.checksum); err != nil {
			return fmt.Errorf("record projection migration %s: %w", migration.id, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit projection migrations: %w", err)
	}
	return nil
}

func loadProjectionMigrations(path string) ([]projectionMigration, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("projection migration path must be a directory so the complete history can be verified")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			paths = append(paths, filepath.Join(path, entry.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("projection migration path contains no SQL files")
	}
	migrations := make([]projectionMigration, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	seenVersions := make(map[int]string, len(paths))
	for _, migrationPath := range paths {
		id := filepath.Base(migrationPath)
		nameParts := projectionMigrationName.FindStringSubmatch(id)
		if nameParts == nil {
			return nil, fmt.Errorf("projection migration filename must match NNN_name.sql: %s", id)
		}
		version, conversionErr := strconv.Atoi(nameParts[1])
		if conversionErr != nil || version < 1 {
			return nil, fmt.Errorf("invalid projection migration version: %s", id)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("duplicate projection migration id: %s", id)
		}
		if existing, ok := seenVersions[version]; ok {
			return nil, fmt.Errorf("duplicate projection migration version %d: %s and %s", version, existing, id)
		}
		seen[id] = struct{}{}
		seenVersions[version] = id
		migration, readErr := os.ReadFile(migrationPath)
		if readErr != nil {
			return nil, readErr
		}
		digest := sha256.Sum256(migration)
		migrations = append(migrations, projectionMigration{
			id: id, version: version, contents: migration,
			checksum: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for index, migration := range migrations {
		expected := index + 1
		if migration.version != expected {
			return nil, fmt.Errorf("projection migration sequence has a gap: expected %03d, found %s", expected, migration.id)
		}
	}
	return migrations, nil
}

func ensureProjectionMigrationLedger(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS bpmp_schema_migrations (
			namespace TEXT NOT NULL,
			migration_id TEXT NOT NULL,
			checksum_sha256 TEXT NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
			applied_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
			applied_by TEXT NOT NULL DEFAULT current_user,
			version BIGINT NOT NULL DEFAULT 1 CHECK (version = 1),
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE CHECK (NOT is_deleted),
			PRIMARY KEY (namespace, migration_id)
		);
		CREATE OR REPLACE FUNCTION bpmp_reject_schema_migration_mutation()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'bpmp_schema_migrations is append-only';
		END;
		$$;
		DROP TRIGGER IF EXISTS bpmp_schema_migrations_no_mutation ON bpmp_schema_migrations;
		CREATE TRIGGER bpmp_schema_migrations_no_mutation
		BEFORE UPDATE OR DELETE ON bpmp_schema_migrations
		FOR EACH ROW EXECUTE FUNCTION bpmp_reject_schema_migration_mutation();`)
	if err != nil {
		return fmt.Errorf("ensure projection migration ledger: %w", err)
	}
	return nil
}

func loadCursorCodec(config cursorSigningConfig) (*grpcapi.CursorCodec, error) {
	keys := make([]grpcapi.CursorKey, 0, len(config.Keys))
	for _, configured := range config.Keys {
		secret, err := os.ReadFile(configured.SecretPath)
		if err != nil {
			return nil, fmt.Errorf("read projection cursor signing key %q: %w", configured.ID, err)
		}
		keys = append(keys, grpcapi.CursorKey{ID: configured.ID, Secret: secret})
	}
	codec, err := grpcapi.NewCursorCodec(keys)
	if err != nil {
		return nil, fmt.Errorf("create projection cursor codec: %w", err)
	}
	return codec, nil
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
