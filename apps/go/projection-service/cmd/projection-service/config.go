package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
)

type config struct {
	ListenAddress   string                     `json:"listen_address"`
	HealthAddress   string                     `json:"health_address"`
	PostgresDSN     string                     `json:"postgres_dsn"`
	ApplyMigrations bool                       `json:"apply_migrations"`
	MigrationPath   string                     `json:"migration_path"`
	TLS             tlsConfig                  `json:"tls"`
	GRPC            grpcConfig                 `json:"grpc"`
	Health          healthConfig               `json:"health"`
	Telemetry       telemetryConfig            `json:"telemetry"`
	Kafka           kafkaconfig.Consumer       `json:"kafka"`
	RuntimeConfig   dynamicConfigurationConfig `json:"runtime_configuration"`
	Identity        identityConfig             `json:"identity"`
	CursorSigning   cursorSigningConfig        `json:"cursor_signing"`
	PolicyBundles   policyBundleConfig         `json:"authorization_policy_bundles"`
	BranchScope     branchScopeResolverConfig  `json:"authorization_branch_scope_resolver"`
	QueryRevoke     queryRevokeConfig          `json:"query_revoke"`
}
type policyBundleConfig struct {
	Consumer        kafkaconfig.Consumer `json:"consumer"`
	Acknowledgement kafkaconfig.Producer `json:"acknowledgement_producer"`
	ConsumerID      string               `json:"consumer_id"`
	ConsumerType    string               `json:"consumer_type"`
	TrustKeys       map[string]string    `json:"trust_keys"`
	OwnerID         string               `json:"owner_id"`
	MaxBundleBytes  int                  `json:"max_bundle_bytes"`
	InitialBundles  []string             `json:"initial_bundles"`
}
type queryRevokeConfig struct {
	Consumer  kafkaconfig.Consumer `json:"consumer"`
	DLQ       kafkaconfig.Producer `json:"dlq"`
	TrustKeys map[string]string    `json:"trust_keys"`
}
type branchScopeResolverConfig struct {
	BaseURL        string `json:"base_url"`
	CAPath         string `json:"ca_path"`
	TimeoutMS      int64  `json:"timeout_ms"`
	WorkloadID     string `json:"workload_id"`
	SigningKeyID   string `json:"signing_key_id"`
	SigningKeyPath string `json:"signing_key_path"`
	ProofTTLMS     int64  `json:"proof_ttl_ms"`
}

type tlsConfig struct {
	ServerCertificate       string `json:"server_certificate"`
	ServerPrivateKey        string `json:"server_private_key"`
	ClientCertificate       string `json:"client_certificate"`
	ClientPrivateKey        string `json:"client_private_key"`
	ClientCA                string `json:"client_ca"`
	ConfigurationCA         string `json:"configuration_ca"`
	ConfigurationServerName string `json:"configuration_server_name"`
}

type grpcConfig struct {
	MaxReceiveBytes                   int      `json:"max_receive_bytes"`
	MaxSendBytes                      int      `json:"max_send_bytes"`
	UnaryTimeoutMS                    int64    `json:"unary_timeout_ms"`
	AuthorizedClientCertificateSHA256 []string `json:"authorized_client_certificate_sha256"`
	AuthorizedMethods                 []string `json:"authorized_methods"`
	AdmissionRateRPS                  uint32   `json:"admission_rate_rps"`
	AdmissionBurst                    uint32   `json:"admission_burst"`
	ReflectionEnabled                 bool     `json:"reflection_enabled"`
}

type healthConfig struct {
	ReadinessTimeoutMS int64 `json:"readiness_timeout_ms"`
	ShutdownTimeoutMS  int64 `json:"shutdown_timeout_ms"`
	MaxHeaderBytes     int   `json:"max_header_bytes"`
}

type telemetryConfig struct {
	ServiceName     string  `json:"service_name"`
	ServiceVersion  string  `json:"service_version"`
	Endpoint        string  `json:"endpoint"`
	Insecure        bool    `json:"insecure"`
	SampleRatio     float64 `json:"sample_ratio"`
	ExportTimeoutMS int64   `json:"export_timeout_ms"`
}

type dynamicConfigurationConfig struct {
	ResolverAddress      string               `json:"resolver_address"`
	PlatformReference    string               `json:"platform_reference"`
	EnvironmentReference string               `json:"environment_reference"`
	InitialTenantIDs     []string             `json:"initial_tenant_ids"`
	ResolveTimeoutMS     int64                `json:"resolve_timeout_ms"`
	Kafka                kafkaconfig.Consumer `json:"kafka"`
}

type identityConfig struct {
	JWKSPath         string   `json:"jwks_path"`
	Issuers          []string `json:"issuers"`
	Audiences        []string `json:"audiences"`
	Algorithms       []string `json:"algorithms"`
	MaxTokenBytes    int      `json:"max_token_bytes"`
	MaxJWKSKeys      int      `json:"max_jwks_keys"`
	MaxCapabilities  int      `json:"max_capabilities"`
	ClockSkewSeconds int64    `json:"clock_skew_seconds"`
}

type cursorSigningConfig struct {
	Keys []cursorSigningKeyConfig `json:"keys"`
}

type cursorSigningKeyConfig struct {
	ID         string `json:"id"`
	SecretPath string `json:"secret_path"`
}

func loadConfig(path string) (config, error) {
	var value config
	data, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return value, err
	}
	return value, value.Validate()
}

func (c config) Validate() error {
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(c.HealthAddress); err != nil {
		return err
	}
	if c.PostgresDSN == "" || (c.ApplyMigrations && c.MigrationPath == "") ||
		c.TLS.ServerCertificate == "" || c.TLS.ServerPrivateKey == "" ||
		c.TLS.ClientCertificate == "" || c.TLS.ClientPrivateKey == "" ||
		c.TLS.ClientCA == "" || c.TLS.ConfigurationCA == "" ||
		c.TLS.ConfigurationServerName == "" ||
		c.GRPC.MaxReceiveBytes <= 0 || c.GRPC.MaxSendBytes <= 0 ||
		c.GRPC.UnaryTimeoutMS <= 0 || len(c.GRPC.AuthorizedClientCertificateSHA256) == 0 ||
		len(c.GRPC.AuthorizedMethods) == 0 || c.GRPC.AdmissionRateRPS == 0 ||
		c.GRPC.AdmissionBurst == 0 ||
		c.Health.ReadinessTimeoutMS <= 0 || c.Health.ShutdownTimeoutMS <= 0 ||
		c.Health.MaxHeaderBytes <= 0 ||
		c.Telemetry.ServiceName == "" || c.Telemetry.ServiceVersion == "" ||
		c.Telemetry.Endpoint == "" || c.Telemetry.SampleRatio < 0 ||
		c.Telemetry.SampleRatio > 1 || c.Telemetry.ExportTimeoutMS <= 0 ||
		c.Kafka.Validate() != nil || c.Identity.JWKSPath == "" ||
		len(c.Identity.Issuers) == 0 || len(c.Identity.Audiences) == 0 ||
		len(c.Identity.Algorithms) == 0 || c.Identity.MaxTokenBytes <= 0 ||
		c.Identity.MaxJWKSKeys <= 0 || c.Identity.MaxCapabilities <= 0 ||
		c.Identity.ClockSkewSeconds < 0 || len(c.CursorSigning.Keys) == 0 {
		return errors.New("projection service configuration is invalid")
	}
	if c.PolicyBundles.Consumer.Validate() != nil || c.PolicyBundles.Acknowledgement.Validate() != nil || c.PolicyBundles.Acknowledgement.Topic == c.PolicyBundles.Consumer.Topic || c.PolicyBundles.ConsumerID == "" || len(c.PolicyBundles.ConsumerID) > 40 || c.PolicyBundles.ConsumerType == "" || len(c.PolicyBundles.ConsumerType) > 100 || c.PolicyBundles.OwnerID == "" || c.PolicyBundles.MaxBundleBytes <= 0 || c.PolicyBundles.MaxBundleBytes > c.PolicyBundles.Consumer.MaxMessageBytes || len(c.PolicyBundles.TrustKeys) == 0 || len(c.PolicyBundles.TrustKeys) > c.Identity.MaxJWKSKeys {
		return errors.New("invalid local authorization bundle configuration")
	}
	if c.QueryRevoke.Consumer.Validate() != nil || c.QueryRevoke.DLQ.Validate() != nil || c.QueryRevoke.Consumer.Topic == c.QueryRevoke.DLQ.Topic || len(c.QueryRevoke.TrustKeys) == 0 || len(c.QueryRevoke.TrustKeys) > c.Identity.MaxJWKSKeys {
		return errors.New("invalid centralized query revoke Kafka configuration")
	}
	if !strings.HasPrefix(c.BranchScope.BaseURL, "https://") || c.BranchScope.CAPath == "" || c.BranchScope.TimeoutMS <= 0 || strings.TrimSpace(c.BranchScope.WorkloadID) == "" || strings.TrimSpace(c.BranchScope.SigningKeyID) == "" || c.BranchScope.SigningKeyPath == "" || c.BranchScope.ProofTTLMS <= 0 || c.BranchScope.ProofTTLMS > 300_000 {
		return errors.New("current organization branch scope resolver configuration is invalid")
	}
	for id, path := range c.QueryRevoke.TrustKeys {
		if id == "" || path == "" {
			return errors.New("invalid revoke signing trust reference")
		}
	}
	cursorKeyIDs := make(map[string]struct{}, len(c.CursorSigning.Keys))
	for _, key := range c.CursorSigning.Keys {
		if key.ID == "" || key.SecretPath == "" {
			return errors.New("projection cursor signing key is invalid")
		}
		if _, duplicate := cursorKeyIDs[key.ID]; duplicate {
			return errors.New("projection cursor signing key is duplicated")
		}
		cursorKeyIDs[key.ID] = struct{}{}
	}
	runtime := c.RuntimeConfig
	if runtime.ResolverAddress == "" || runtime.PlatformReference == "" ||
		runtime.EnvironmentReference == "" || len(runtime.InitialTenantIDs) == 0 ||
		runtime.ResolveTimeoutMS <= 0 || runtime.Kafka.Validate() != nil {
		return errors.New("projection runtime configuration bootstrap is invalid")
	}
	seen := make(map[string]struct{}, len(runtime.InitialTenantIDs))
	for _, tenantID := range runtime.InitialTenantIDs {
		if tenantID == "" {
			return errors.New("projection runtime tenant is invalid")
		}
		if _, duplicate := seen[tenantID]; duplicate {
			return errors.New("projection runtime tenant is duplicated")
		}
		seen[tenantID] = struct{}{}
	}
	return nil
}

func milliseconds(value int64) time.Duration {
	return time.Duration(value) * time.Millisecond
}
