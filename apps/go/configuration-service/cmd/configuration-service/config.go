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

type runtimeConfig struct {
	ListenAddress   string               `json:"listen_address"`
	PostgresDSN     string               `json:"postgres_dsn"`
	ApplyMigrations bool                 `json:"apply_migrations"`
	MigrationPath   string               `json:"migration_path"`
	TLS             tlsConfig            `json:"tls"`
	GRPC            grpcConfig           `json:"grpc"`
	Kafka           kafkaconfig.Producer `json:"kafka"`
	Outbox          outboxConfig         `json:"outbox"`
	Identity        identityConfig       `json:"identity"`
	API             apiConfig            `json:"api"`
	Telemetry       telemetryConfig      `json:"telemetry"`
}

type tlsConfig struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
	ClientCA    string `json:"client_ca"`
}

type identityConfig struct {
	JWKSPath         string   `json:"jwks_path"`
	Issuers          []string `json:"issuers"`
	Audiences        []string `json:"audiences"`
	Algorithms       []string `json:"algorithms"`
	MaxTokenBytes    int      `json:"max_token_bytes"`
	MaxJWKSKeys      int      `json:"max_jwks_keys"`
	ClockSkewMS      int64    `json:"clock_skew_ms"`
	ReadCapability   string   `json:"read_capability"`
	ManageCapability string   `json:"manage_capability"`
}

type grpcConfig struct {
	ListenAddress   string `json:"listen_address"`
	MaxReceiveBytes int    `json:"max_receive_bytes"`
	MaxSendBytes    int    `json:"max_send_bytes"`
}

type outboxConfig struct {
	WorkerID            string `json:"worker_id"`
	BatchSize           int    `json:"batch_size"`
	LeaseDurationMS     int64  `json:"lease_duration_ms"`
	PollIntervalMS      int64  `json:"poll_interval_ms"`
	InitialRetryDelayMS int64  `json:"initial_retry_delay_ms"`
	MaxRetryDelayMS     int64  `json:"max_retry_delay_ms"`
	RetryMultiplier     uint32 `json:"retry_multiplier_millis"`
}

type apiConfig struct {
	DefaultPageSize     int   `json:"default_page_size"`
	MaxPageSize         int   `json:"max_page_size"`
	MaxBodyBytes        int64 `json:"max_body_bytes"`
	ReadHeaderTimeoutMS int64 `json:"read_header_timeout_ms"`
	ReadTimeoutMS       int64 `json:"read_timeout_ms"`
	WriteTimeoutMS      int64 `json:"write_timeout_ms"`
	IdleTimeoutMS       int64 `json:"idle_timeout_ms"`
	ShutdownTimeoutMS   int64 `json:"shutdown_timeout_ms"`
	ReadinessTimeoutMS  int64 `json:"readiness_timeout_ms"`
}

type telemetryConfig struct {
	ServiceName     string  `json:"service_name"`
	ServiceVersion  string  `json:"service_version"`
	Endpoint        string  `json:"endpoint"`
	Insecure        bool    `json:"insecure"`
	SampleRatio     float64 `json:"sample_ratio"`
	ExportTimeoutMS int64   `json:"export_timeout_ms"`
}

func loadConfig(path string) (runtimeConfig, error) {
	var config runtimeConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return config, err
	}
	if err = config.validate(); err != nil {
		return config, err
	}
	return config, nil
}

func (c runtimeConfig) validate() error {
	if c.ListenAddress == "" || c.GRPC.ListenAddress == "" || c.PostgresDSN == "" ||
		c.TLS.Certificate == "" || c.TLS.PrivateKey == "" || c.TLS.ClientCA == "" ||
		c.Identity.JWKSPath == "" || len(c.Identity.Issuers) == 0 ||
		len(c.Identity.Audiences) == 0 || len(c.Identity.Algorithms) == 0 ||
		c.Identity.ReadCapability == "" || c.Identity.ManageCapability == "" ||
		c.Telemetry.ServiceName == "" || c.Telemetry.ServiceVersion == "" ||
		c.Telemetry.Endpoint == "" || c.Outbox.WorkerID == "" {
		return errors.New("configuration-service configuration is incomplete")
	}
	if err := c.Kafka.Validate(); err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(c.GRPC.ListenAddress); err != nil {
		return err
	}
	if c.ApplyMigrations && c.MigrationPath == "" {
		return errors.New("migration path is required")
	}
	if c.Identity.MaxTokenBytes <= 0 || c.Identity.MaxJWKSKeys <= 0 ||
		c.GRPC.MaxReceiveBytes <= 0 || c.GRPC.MaxSendBytes <= 0 ||
		c.API.DefaultPageSize <= 0 || c.API.MaxPageSize < c.API.DefaultPageSize ||
		c.API.MaxBodyBytes <= 0 || c.API.ReadHeaderTimeoutMS <= 0 ||
		c.API.ReadTimeoutMS <= 0 || c.API.WriteTimeoutMS <= 0 ||
		c.API.IdleTimeoutMS <= 0 || c.API.ShutdownTimeoutMS <= 0 ||
		c.API.ReadinessTimeoutMS <= 0 || c.Telemetry.ExportTimeoutMS <= 0 ||
		c.Telemetry.SampleRatio < 0 || c.Telemetry.SampleRatio > 1 ||
		c.Outbox.BatchSize <= 0 || c.Outbox.LeaseDurationMS <= 0 ||
		c.Outbox.PollIntervalMS <= 0 || c.Outbox.InitialRetryDelayMS <= 0 ||
		c.Outbox.MaxRetryDelayMS < c.Outbox.InitialRetryDelayMS ||
		c.Outbox.RetryMultiplier < 1000 {
		return errors.New("configuration-service bounds are invalid")
	}
	return nil
}

func milliseconds(value int64) time.Duration {
	return time.Duration(value) * time.Millisecond
}
