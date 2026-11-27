package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/engineadapter"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/postgres"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	configv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type config struct {
	PostgresDSN             string `json:"postgres_dsn"`
	EngineAddress           string `json:"engine_address"`
	EngineServerName        string `json:"engine_server_name"`
	AuthorizationURL        string `json:"authorization_url"`
	AuthorizationServerName string `json:"authorization_server_name"`
	AuthorizationAudience   string `json:"authorization_audience"`
	WorkloadID              string `json:"workload_id"`
	WorkloadKeyID           string `json:"workload_key_id"`
	WorkloadPrivateKey      string `json:"workload_private_key"`
	TLSCertificate          string `json:"tls_certificate"`
	TLSPrivateKey           string `json:"tls_private_key"`
	TLSCA                   string `json:"tls_ca"`
	// MetadataPath is a local-only E2E fixture fallback; production deployments
	// must configure either AuthoringAddress+ConfigurationAddress or MetadataURL.
	MetadataPath            string `json:"metadata_path"`
	MetadataURL             string `json:"metadata_url"`
	MetadataServerName      string `json:"metadata_server_name"`
	MetadataAudience        string `json:"metadata_audience"`
	AuthoringAddress        string `json:"authoring_address"`
	AuthoringServerName     string `json:"authoring_server_name"`
	AuthoringAudience       string `json:"authoring_audience"`
	ConfigurationAddress    string `json:"configuration_address"`
	ConfigurationServerName string `json:"configuration_server_name"`
	PlatformReference       string `json:"platform_reference"`
	EnvironmentReference    string `json:"environment_reference"`
	PollIntervalMS          int64  `json:"poll_interval_ms"`
}

type metadataDocument struct {
	Workflows []workflowMetadata `json:"workflows"`
}
type workflowMetadata struct {
	TenantID           string `json:"tenant_id"`
	WorkflowType       string `json:"workflow_type"`
	WorkflowVersion    string `json:"workflow_version"`
	StartNodeID        string `json:"start_node_id"`
	EncryptionKeyScope string `json:"encryption_key_scope"`
}
type metadataProvider map[string]workflowMetadata

func (p metadataProvider) ResolveStartMetadata(_ context.Context, job application.StartJob) (string, string, error) {
	key := job.TenantID.String() + "\x00" + job.WorkflowType + "\x00" + job.WorkflowVersion
	value, ok := p[key]
	if !ok || value.TenantID != job.TenantID.String() {
		return "", "", errors.New("workflow version is absent from the fixture metadata catalog")
	}
	return value.StartNodeID, value.EncryptionKeyScope, nil
}

func main() {
	path := flag.String("config", os.Getenv("CASE_START_CONFIG"), "Case start worker JSON configuration")
	flag.Parse()
	if err := run(*path); err != nil {
		slog.Error("Case start worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string) error {
	if path == "" {
		return errors.New("Case start worker configuration is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&cfg); err != nil {
		return err
	}
	if cfg.PostgresDSN == "" || cfg.EngineAddress == "" || cfg.EngineServerName == "" || cfg.AuthorizationURL == "" || cfg.AuthorizationServerName == "" || cfg.AuthorizationAudience == "" || cfg.WorkloadID != "case-service" || cfg.WorkloadKeyID == "" || cfg.PollIntervalMS < 50 || cfg.PollIntervalMS > 5000 {
		return errors.New("Case start worker configuration is invalid")
	}
	hasOwnerGRPC := cfg.AuthoringAddress != "" || cfg.ConfigurationAddress != ""
	hasOwnerHTTP := cfg.MetadataURL != ""
	hasFixture := cfg.MetadataPath != ""
	modes := 0
	if hasOwnerGRPC {
		modes++
	}
	if hasOwnerHTTP {
		modes++
	}
	if hasFixture {
		modes++
	}
	if modes != 1 {
		return errors.New("Case start worker requires exactly one workflow metadata source (authoring+configuration gRPC, metadata_url HTTPS, or local metadata_path fixture)")
	}
	privateKeyBytes, err := os.ReadFile(cfg.WorkloadPrivateKey)
	if err != nil || len(privateKeyBytes) != ed25519.SeedSize {
		return errors.New("Case workload signing key is invalid")
	}
	privateKey := ed25519.NewKeyFromSeed(privateKeyBytes)
	certificate, err := tls.LoadX509KeyPair(cfg.TLSCertificate, cfg.TLSPrivateKey)
	if err != nil {
		return err
	}
	caBytes, err := os.ReadFile(cfg.TLSCA)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return errors.New("Case worker TLS CA is invalid")
	}
	var metadata engineadapter.WorkflowMetadata
	switch {
	case hasOwnerGRPC:
		if cfg.AuthoringAddress == "" || cfg.AuthoringServerName == "" || cfg.AuthoringAudience == "" ||
			cfg.ConfigurationAddress == "" || cfg.ConfigurationServerName == "" ||
			cfg.PlatformReference == "" || cfg.EnvironmentReference == "" {
			return errors.New("Case owner gRPC metadata configuration is incomplete")
		}
		authoringConn, dialErr := grpc.NewClient(cfg.AuthoringAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: cfg.AuthoringServerName, RootCAs: roots, Certificates: []tls.Certificate{certificate}})))
		if dialErr != nil {
			return dialErr
		}
		defer authoringConn.Close()
		configConn, dialErr := grpc.NewClient(cfg.ConfigurationAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: cfg.ConfigurationServerName, RootCAs: roots, Certificates: []tls.Certificate{certificate}})))
		if dialErr != nil {
			return dialErr
		}
		defer configConn.Close()
		proofForJob := func(_ context.Context, job application.StartJob) (*authv1.ActorProof, error) {
			now := time.Now().UTC()
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
				"sub":          cfg.WorkloadID,
				"tenant_id":    job.TenantID.String(),
				"capabilities": []string{"authoring.deployment.read", "authoring.model.read"},
				"aud":          []string{cfg.AuthoringAudience},
				"iat":          now.Unix(),
				"exp":          now.Add(time.Minute).Unix(),
			})
			token.Header["kid"] = cfg.WorkloadKeyID
			signed, signErr := token.SignedString(privateKey)
			if signErr != nil {
				return nil, signErr
			}
			return &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(signed)}, nil
		}
		metadata, err = engineadapter.NewOwnerAPIMetadataProvider(
			authoringv1.NewAuthoringServiceClient(authoringConn),
			configv1.NewConfigurationResolverServiceClient(configConn),
			cfg.PlatformReference, cfg.EnvironmentReference, proofForJob,
		)
		if err != nil {
			return err
		}
	case hasOwnerHTTP:
		if cfg.MetadataServerName == "" || cfg.MetadataAudience == "" {
			return errors.New("Case owner HTTPS metadata configuration is incomplete")
		}
		metaClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: cfg.MetadataServerName, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}, Timeout: 5 * time.Second}
		metadata, err = engineadapter.NewHTTPMetadataProvider(metaClient, cfg.MetadataURL, cfg.MetadataAudience, cfg.WorkloadID, cfg.WorkloadKeyID, privateKey, time.Now)
		if err != nil {
			return err
		}
	default:
		metadataBytes, readErr := os.ReadFile(cfg.MetadataPath)
		if readErr != nil {
			return readErr
		}
		var document metadataDocument
		if err = json.Unmarshal(metadataBytes, &document); err != nil || len(document.Workflows) == 0 {
			return errors.New("Case fixture workflow metadata is invalid")
		}
		fixture := make(metadataProvider, len(document.Workflows))
		for _, item := range document.Workflows {
			if item.TenantID == "" || item.WorkflowType == "" || item.WorkflowVersion == "" || item.StartNodeID == "" || item.EncryptionKeyScope == "" {
				return errors.New("Case fixture workflow metadata entry is incomplete")
			}
			key := item.TenantID + "\x00" + item.WorkflowType + "\x00" + item.WorkflowVersion
			if _, exists := fixture[key]; exists {
				return errors.New("duplicate Case fixture workflow metadata")
			}
			fixture[key] = item
		}
		metadata = fixture
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return err
	}
	engineConn, err := grpc.NewClient(cfg.EngineAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: cfg.EngineServerName, RootCAs: roots, Certificates: []tls.Certificate{certificate}})))
	if err != nil {
		return err
	}
	defer engineConn.Close()
	authzClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: cfg.AuthorizationServerName, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}, Timeout: 5 * time.Second}
	provider, err := engineadapter.NewAuthzCredentialsProvider(authzClient, cfg.AuthorizationURL, cfg.AuthorizationAudience, cfg.WorkloadID, cfg.WorkloadKeyID, privateKey, metadata, time.Now)
	if err != nil {
		return err
	}
	adapter, err := engineadapter.New(enginev1.NewEngineCommandServiceClient(engineConn), provider, cfg.WorkloadID, time.Now)
	if err != nil {
		return err
	}
	store, err := postgres.New(pool)
	if err != nil {
		return err
	}
	runner, err := application.NewStartRunner(store, adapter, time.Now, uuidNew)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(time.Duration(cfg.PollIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		worked, runErr := runner.RunOnce(ctx)
		if runErr != nil {
			slog.Error("Case workflow-start attempt failed", "error", runErr)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func uuidNew() uuid.UUID { return uuid.New() }
