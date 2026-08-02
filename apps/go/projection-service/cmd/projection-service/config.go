package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
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
		c.Kafka.Validate() != nil {
		return errors.New("projection service configuration is invalid")
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
