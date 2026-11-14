package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/adapter/authz"
	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/adapter/compiler"
	engineadapter "github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/adapter/engine"
	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/adapter/grpcserver"
	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/adapter/identity"
	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	compilerv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/compiler/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
)

type config struct {
	ListenAddress  string `json:"listen_address"`
	HealthAddress  string `json:"health_address"`
	PostgresDSN    string `json:"postgres_dsn"`
	ApplyMigration bool   `json:"apply_migration"`
	MigrationPath  string `json:"migration_path"`
	ManifestPath   string `json:"owner_manifest_path"`
	OwnerID        string `json:"owner_id"`
	TLS            struct {
		ServerCertificate         string   `json:"server_certificate"`
		ServerPrivateKey          string   `json:"server_private_key"`
		ClientCA                  string   `json:"client_ca"`
		AuthorizedClientSHA256    []string `json:"authorized_client_sha256"`
		CompilerAddress           string   `json:"compiler_address"`
		CompilerServerName        string   `json:"compiler_server_name"`
		CompilerCA                string   `json:"compiler_ca"`
		CompilerClientCertificate string   `json:"compiler_client_certificate"`
		CompilerClientPrivateKey  string   `json:"compiler_client_private_key"`
		EngineAddress             string   `json:"engine_address"`
		EngineServerName          string   `json:"engine_server_name"`
		EngineCA                  string   `json:"engine_ca"`
		EngineClientCertificate   string   `json:"engine_client_certificate"`
		EngineClientPrivateKey    string   `json:"engine_client_private_key"`
	} `json:"tls"`
	JWT struct {
		JWKSPath          string            `json:"jwks_path"`
		Issuers           []string          `json:"issuers"`
		Audiences         []string          `json:"audiences"`
		Algorithms        []string          `json:"algorithms"`
		WorkloadID        string            `json:"workload_id"`
		InternalProofKeys map[string]string `json:"internal_proof_public_keys"`
		MaxTokenBytes     int               `json:"max_token_bytes"`
		MaxJWKSKeys       int               `json:"max_jwks_keys"`
		MaxRoles          int               `json:"max_roles"`
		MaxCapabilities   int               `json:"max_capabilities"`
		ClockSkewMS       int64             `json:"clock_skew_ms"`
	} `json:"identity"`
	Authz struct {
		SigningKeys            map[string]string `json:"bundle_signing_public_keys"`
		KafkaBrokers           []string          `json:"kafka_brokers"`
		KafkaCA                string            `json:"kafka_ca"`
		KafkaClientCertificate string            `json:"kafka_client_certificate"`
		KafkaClientPrivateKey  string            `json:"kafka_client_private_key"`
		BundleTopic            string            `json:"bundle_topic"`
		AcknowledgementTopic   string            `json:"acknowledgement_topic"`
		ConsumerGroupPrefix    string            `json:"consumer_group_prefix"`
	} `json:"authorization"`
}

func main() {
	path := flag.String("config", os.Getenv("AUTHORING_SERVICE_CONFIG"), "path to Authoring service JSON config")
	flag.Parse()
	if *path == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*path); err != nil {
		slog.Error("authoring service stopped", "error", err)
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

	pool, err := pgxpool.New(ctx, config.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if config.ApplyMigration {
		if err = applyMigration(ctx, pool, config.MigrationPath); err != nil {
			return err
		}
	}
	store, err := postgresadapter.New(pool)
	if err != nil {
		return err
	}

	internalKeys, err := decodePublicKeys(config.JWT.InternalProofKeys)
	if err != nil {
		return fmt.Errorf("load actor proof trust keys: %w", err)
	}
	actorVerifier, err := identity.New(identity.Config{
		JWKSPath: config.JWT.JWKSPath, Issuers: config.JWT.Issuers, Audiences: config.JWT.Audiences,
		Algorithms: config.JWT.Algorithms, MaxTokenBytes: config.JWT.MaxTokenBytes, MaxJWKSKeys: config.JWT.MaxJWKSKeys,
		MaxRoles: config.JWT.MaxRoles, MaxCapabilities: config.JWT.MaxCapabilities,
		ClockSkew:  time.Duration(config.JWT.ClockSkewMS) * time.Millisecond,
		WorkloadID: config.JWT.WorkloadID, InternalProofKeys: internalKeys,
	})
	if err != nil {
		return fmt.Errorf("configure actor verifier: %w", err)
	}

	bundleKeys, err := decodePublicKeys(config.Authz.SigningKeys)
	if err != nil {
		return fmt.Errorf("load Authz bundle trust keys: %w", err)
	}
	bundles, err := authz.NewBundleStore(bundleKeys, 16<<20, 100_000)
	if err != nil {
		return fmt.Errorf("configure Authz bundle store: %w", err)
	}
	if err = bundles.SetPersistence(store); err != nil {
		return err
	}
	if err = bundles.Restore(ctx); err != nil {
		return fmt.Errorf("restore signed Authz bundle cache: %w", err)
	}
	pep, err := authz.NewAuthorizer(bundles, config.OwnerID, config.ManifestPath, time.Now)
	if err != nil {
		return fmt.Errorf("configure Authz PEP: %w", err)
	}

	kafkaTLS, err := loadClientTLS(config.Authz.KafkaCA, config.Authz.KafkaClientCertificate, config.Authz.KafkaClientPrivateKey, "")
	if err != nil {
		return fmt.Errorf("configure Authz Kafka TLS: %w", err)
	}
	instanceID := uuid.NewString()
	kafkaClient, err := kgo.NewClient(
		kgo.SeedBrokers(config.Authz.KafkaBrokers...), kgo.ConsumerGroup(config.Authz.ConsumerGroupPrefix+"-"+instanceID),
		kgo.ConsumeTopics(config.Authz.BundleTopic), kgo.DisableAutoCommit(), kgo.DialTLSConfig(kafkaTLS),
	)
	if err != nil {
		return fmt.Errorf("configure Authz Kafka client: %w", err)
	}
	defer kafkaClient.Close()
	if err = kafkaClient.Ping(ctx); err != nil {
		return fmt.Errorf("connect Authz Kafka: %w", err)
	}

	compilerTLS, err := loadClientTLS(config.TLS.CompilerCA, config.TLS.CompilerClientCertificate, config.TLS.CompilerClientPrivateKey, config.TLS.CompilerServerName)
	if err != nil {
		return fmt.Errorf("configure compiler TLS: %w", err)
	}
	compilerConn, err := grpc.NewClient(config.TLS.CompilerAddress, grpc.WithTransportCredentials(credentials.NewTLS(compilerTLS)))
	if err != nil {
		return fmt.Errorf("configure compiler connection: %w", err)
	}
	defer compilerConn.Close()
	compilerConn.Connect()
	compilerClient, err := compiler.New(compilerv1.NewBpmnCompilerServiceClient(compilerConn))
	if err != nil {
		return err
	}
	engineTLS, err := loadClientTLS(config.TLS.EngineCA, config.TLS.EngineClientCertificate, config.TLS.EngineClientPrivateKey, config.TLS.EngineServerName)
	if err != nil {
		return fmt.Errorf("configure Engine TLS: %w", err)
	}
	engineConn, err := grpc.NewClient(config.TLS.EngineAddress, grpc.WithTransportCredentials(credentials.NewTLS(engineTLS)))
	if err != nil {
		return fmt.Errorf("configure Engine connection: %w", err)
	}
	defer engineConn.Close()
	engineConn.Connect()
	engineClient, err := engineadapter.New(enginev1.NewEngineDeploymentServiceClient(engineConn))
	if err != nil {
		return err
	}

	service, err := application.New(store, actorVerifier, pep, compilerClient, time.Now)
	if err != nil {
		return err
	}
	if err = service.SetRuntimeDeploymentEngine(engineClient); err != nil {
		return err
	}
	grpcAdapter, err := grpcserver.New(service)
	if err != nil {
		return err
	}
	serverTLS, err := loadServerTLS(config.TLS.ServerCertificate, config.TLS.ServerPrivateKey, config.TLS.ClientCA, config.TLS.AuthorizedClientSHA256)
	if err != nil {
		return fmt.Errorf("configure Authoring mTLS: %w", err)
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for Authoring gRPC: %w", err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)), grpc.MaxRecvMsgSize((1<<20)+(64<<10)), grpc.MaxSendMsgSize(2<<20))
	authoringv1.RegisterAuthoringServiceServer(grpcServer, grpcAdapter)

	health := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		checkCtx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if request.URL.Path != "/health/ready" {
			http.NotFound(writer, request)
			return
		}
		if pool.Ping(checkCtx) != nil || kafkaClient.Ping(checkCtx) != nil || compilerConn.GetState() != connectivity.Ready || engineConn.GetState() != connectivity.Ready {
			http.Error(writer, "dependency unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready"))
	})
	healthServer := &http.Server{Addr: config.HealthAddress, Handler: health, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}
	errorsCh := make(chan error, 4)
	go func() {
		if serveErr := grpcServer.Serve(listener); serveErr != nil {
			errorsCh <- serveErr
		}
	}()
	go func() {
		if serveErr := healthServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errorsCh <- serveErr
		}
	}()
	go func() {
		errorsCh <- authz.RunBundleConsumer(ctx, kafkaClient, kafkaClient, bundles, config.Authz.BundleTopic, config.Authz.AcknowledgementTopic, "authoring-"+instanceID, 100, 16<<20)
	}()
	go func() {
		errorsCh <- service.RunRuntimeDeploymentWorker(ctx, instanceID, 500*time.Millisecond, 30*time.Second)
	}()
	slog.Info("Authoring service started", "instance_id", instanceID, "listen", config.ListenAddress)
	select {
	case <-ctx.Done():
	case err = <-errorsCh:
		if err != nil {
			stop()
		}
	}
	grpcServer.GracefulStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = healthServer.Shutdown(shutdownCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func loadConfig(path string) (config, error) {
	var value config
	raw, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return value, err
	}
	if err = decoder.Decode(&struct{}{}); err != nil && !errors.Is(err, io.EOF) {
		return value, errors.New("Authoring config must contain exactly one JSON value")
	} else if err == nil {
		return value, errors.New("Authoring config must contain exactly one JSON value")
	}
	if value.ListenAddress == "" || value.HealthAddress == "" || value.PostgresDSN == "" || value.ManifestPath == "" || value.OwnerID == "" || value.TLS.CompilerAddress == "" || value.TLS.CompilerServerName == "" || value.TLS.EngineAddress == "" || value.TLS.EngineServerName == "" || len(value.TLS.AuthorizedClientSHA256) == 0 || len(value.Authz.KafkaBrokers) == 0 || value.Authz.BundleTopic == "" || value.Authz.AcknowledgementTopic == "" || value.Authz.ConsumerGroupPrefix == "" {
		return value, errors.New("authoring service configuration is incomplete")
	}
	if _, _, err = net.SplitHostPort(value.ListenAddress); err != nil {
		return value, err
	}
	if _, _, err = net.SplitHostPort(value.HealthAddress); err != nil {
		return value, err
	}
	if value.JWT.MaxTokenBytes <= 0 || value.JWT.MaxJWKSKeys <= 0 || value.JWT.MaxRoles <= 0 || value.JWT.MaxCapabilities <= 0 || value.JWT.WorkloadID == "" || len(value.JWT.InternalProofKeys) == 0 || len(value.JWT.Issuers) == 0 || len(value.JWT.Audiences) == 0 || len(value.JWT.Algorithms) == 0 || len(value.Authz.SigningKeys) == 0 {
		return value, errors.New("authoring identity or Authz trust configuration is incomplete")
	}
	if kafkaconfig.ValidateTopic(value.Authz.BundleTopic) != nil || kafkaconfig.ValidateTopic(value.Authz.AcknowledgementTopic) != nil || kafkaconfig.ValidateConsumerGroup(value.Authz.ConsumerGroupPrefix+"-authoring-instance") != nil {
		return value, errors.New("authoring Authz Kafka configuration is invalid")
	}
	return value, nil
}

func decodePublicKeys(values map[string]string) (map[string]ed25519.PublicKey, error) {
	keys := make(map[string]ed25519.PublicKey, len(values))
	for id, encoded := range values {
		raw, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			raw, err = base64.StdEncoding.DecodeString(encoded)
		}
		if err != nil || len(raw) != ed25519.PublicKeySize || id == "" {
			return nil, errors.New("invalid Ed25519 public key encoding")
		}
		keys[id] = ed25519.PublicKey(raw)
	}
	return keys, nil
}

func loadClientTLS(caPath, certPath, keyPath, serverName string) (*tls.Config, error) {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("TLS CA bundle contains no certificates")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: serverName}, nil
}

func loadServerTLS(certPath, keyPath, clientCAPath string, allowed []string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(clientCAPath)
	if err != nil {
		return nil, err
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("client CA bundle contains no certificates")
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, fingerprint := range allowed {
		decoded, decodeErr := hex.DecodeString(strings.TrimPrefix(strings.ToLower(fingerprint), "sha256:"))
		if decodeErr != nil || len(decoded) != sha256.Size {
			return nil, errors.New("invalid authorized client certificate fingerprint")
		}
		allowedSet[hex.EncodeToString(decoded)] = struct{}{}
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("client certificate is required")
		}
		fingerprint := sha256.Sum256(state.PeerCertificates[0].Raw)
		if _, ok := allowedSet[hex.EncodeToString(fingerprint[:])]; !ok {
			return errors.New("client certificate is not authorized")
		}
		return nil
	}}, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, path string) error {
	if path == "" {
		return errors.New("migration path is required when apply_migration is enabled")
	}
	root := path
	if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
		root = filepath.Dir(path)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS authoring_schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext('authoring-service-migrations'))`); err != nil {
		return err
	}
	defer func() {
		_, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('authoring-service-migrations'))`)
	}()
	migrations := []struct{ version, file string }{
		{"001_process_model_drafts", "001_process_model_drafts.sql"},
		{"002_decision_model_drafts", "002_decision_model_drafts.sql"},
		{"003_process_model_versions", "003_process_model_versions.sql"},
		{"004_decision_model_versions", "004_decision_model_versions.sql"},
		{"005_forms", "005_forms.sql"},
		{"006_runtime_deployments", "006_runtime_deployments.sql"},
		{"007_runtime_deployment_worker", "007_runtime_deployment_worker.sql"},
	}
	for _, migration := range migrations {
		var applied bool
		if err = connection.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM authoring_schema_migrations WHERE version=$1)`, migration.version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(root, migration.file))
		if readErr != nil {
			return fmt.Errorf("read Authoring migration %s: %w", migration.version, readErr)
		}
		if _, err = connection.Exec(ctx, string(raw)); err != nil {
			return fmt.Errorf("apply Authoring migration %s: %w", migration.version, err)
		}
		if _, err = connection.Exec(ctx, `INSERT INTO authoring_schema_migrations(version) VALUES($1)`, migration.version); err != nil {
			return err
		}
	}
	return nil
}
