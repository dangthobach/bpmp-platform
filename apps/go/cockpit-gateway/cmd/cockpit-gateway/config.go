package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
)

type config struct {
	ListenAddress string               `json:"listen_address"`
	HealthAddress string               `json:"health_address"`
	TLS           tlsConfig            `json:"tls"`
	Identity      identityConfig       `json:"identity"`
	HTTP          httpConfig           `json:"http"`
	Realtime      realtimeConfig       `json:"realtime"`
	Health        healthConfig         `json:"health"`
	Telemetry     telemetryConfig      `json:"telemetry"`
	Kafka         kafkaconfig.Consumer `json:"kafka"`
}

type tlsConfig struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

type identityConfig struct {
	JWKSPath         string   `json:"jwks_path"`
	Issuers          []string `json:"issuers"`
	Audiences        []string `json:"audiences"`
	Algorithms       []string `json:"algorithms"`
	MaxTokenBytes    int      `json:"max_token_bytes"`
	MaxJWKSKeys      int      `json:"max_jwks_keys"`
	ClockSkewSeconds int64    `json:"clock_skew_seconds"`
}

type httpConfig struct {
	ReadHeaderTimeoutMS int64    `json:"read_header_timeout_ms"`
	RequestTimeoutMS    int64    `json:"request_timeout_ms"`
	IdleTimeoutMS       int64    `json:"idle_timeout_ms"`
	ShutdownTimeoutMS   int64    `json:"shutdown_timeout_ms"`
	MaxHeaderBytes      int      `json:"max_header_bytes"`
	AllowedOrigins      []string `json:"allowed_origins"`
	AdmissionRateRPS    uint32   `json:"admission_rate_rps"`
	AdmissionBurst      uint32   `json:"admission_burst"`
}

type realtimeConfig struct {
	Path                  string   `json:"path"`
	AllowedSignalNames    []string `json:"allowed_signal_names"`
	MaxNamesPerConnection uint32   `json:"max_names_per_connection"`
	MaxSignalNamesBytes   uint32   `json:"max_signal_names_bytes"`
	MaxConnections        uint32   `json:"max_connections"`
	MaxSubscriptions      uint32   `json:"max_subscriptions"`
	OutboundBufferSize    uint32   `json:"outbound_buffer_size"`
	ReplaySizePerStream   uint32   `json:"replay_size_per_stream"`
	MaxReplayStreams      uint32   `json:"max_replay_streams"`
	HeartbeatIntervalMS   int64    `json:"heartbeat_interval_ms"`
}

type healthConfig struct {
	ReadinessTimeoutMS int64 `json:"readiness_timeout_ms"`
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
	if c.TLS.Certificate == "" || c.TLS.PrivateKey == "" ||
		c.Identity.JWKSPath == "" || len(c.Identity.Issuers) == 0 ||
		len(c.Identity.Audiences) == 0 || len(c.Identity.Algorithms) == 0 ||
		c.Identity.MaxTokenBytes <= 0 || c.Identity.MaxJWKSKeys <= 0 ||
		c.Identity.ClockSkewSeconds < 0 ||
		c.HTTP.ReadHeaderTimeoutMS <= 0 || c.HTTP.RequestTimeoutMS <= 0 ||
		c.HTTP.IdleTimeoutMS <= 0 ||
		c.HTTP.ShutdownTimeoutMS <= 0 || c.HTTP.MaxHeaderBytes <= 0 ||
		c.HTTP.AdmissionRateRPS == 0 || c.HTTP.AdmissionBurst == 0 ||
		c.Realtime.Path == "" || c.Realtime.Path[0] != '/' ||
		len(c.Realtime.AllowedSignalNames) == 0 ||
		c.Realtime.MaxNamesPerConnection == 0 ||
		c.Realtime.MaxSignalNamesBytes == 0 ||
		c.Realtime.MaxConnections == 0 ||
		c.Realtime.MaxSubscriptions == 0 ||
		c.Realtime.OutboundBufferSize == 0 ||
		c.Realtime.ReplaySizePerStream == 0 ||
		c.Realtime.MaxReplayStreams < c.Realtime.MaxSubscriptions ||
		c.Realtime.HeartbeatIntervalMS <= 0 ||
		c.Health.ReadinessTimeoutMS <= 0 || c.Health.MaxHeaderBytes <= 0 ||
		c.Telemetry.ServiceName == "" || c.Telemetry.ServiceVersion == "" ||
		c.Telemetry.Endpoint == "" || c.Telemetry.SampleRatio < 0 ||
		c.Telemetry.SampleRatio > 1 || c.Telemetry.ExportTimeoutMS <= 0 ||
		c.Kafka.Validate() != nil {
		return errors.New("cockpit gateway configuration is invalid")
	}
	if c.Realtime.MaxNamesPerConnection >
		uint32(len(c.Realtime.AllowedSignalNames)) ||
		c.Realtime.MaxSubscriptions <
			c.Realtime.MaxNamesPerConnection {
		return errors.New("cockpit realtime bounds are inconsistent")
	}
	if err := uniqueNonEmpty(c.Realtime.AllowedSignalNames); err != nil {
		return err
	}
	if err := validOrigins(c.HTTP.AllowedOrigins); err != nil {
		return err
	}
	return nil
}

func uniqueNonEmpty(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return errors.New("configuration values cannot be empty")
		}
		if _, exists := seen[value]; exists {
			return errors.New("configuration values cannot be duplicated")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validOrigins(values []string) error {
	if len(values) == 0 {
		return errors.New("at least one browser origin is required")
	}
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("browser origin is invalid")
		}
	}
	return uniqueNonEmpty(values)
}

func milliseconds(value int64) time.Duration {
	return time.Duration(value) * time.Millisecond
}
