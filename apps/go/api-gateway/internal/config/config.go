package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

type Config struct {
	ListenAddress   string            `json:"listen_address"`
	EngineAddress   string            `json:"engine_address"`
	HumanAddress    string            `json:"human_address"`
	PublicTLS       PublicTLS         `json:"public_tls"`
	UpstreamTLS     UpstreamTLS       `json:"upstream_tls"`
	Identity        Identity          `json:"identity"`
	Workload        Workload          `json:"workload"`
	RateLimit       RateLimit         `json:"rate_limit"`
	HTTP            HTTP              `json:"http"`
	GRPC            GRPC              `json:"grpc"`
	Reliability     Reliability       `json:"reliability"`
	Health          Health            `json:"health"`
	Telemetry       Telemetry         `json:"telemetry"`
	TenantKeyScopes map[string]string `json:"tenant_key_scopes"`
}

type PublicTLS struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}
type UpstreamTLS struct {
	Certificate      string `json:"certificate"`
	PrivateKey       string `json:"private_key"`
	CA               string `json:"ca"`
	EngineServerName string `json:"engine_server_name"`
	HumanServerName  string `json:"human_server_name"`
}
type Identity struct {
	JWKSPath         string   `json:"jwks_path"`
	Issuers          []string `json:"issuers"`
	Audiences        []string `json:"audiences"`
	Algorithms       []string `json:"algorithms"`
	MaxTokenBytes    int      `json:"max_token_bytes"`
	MaxJWKSKeys      int      `json:"max_jwks_keys"`
	ClockSkewSeconds int64    `json:"clock_skew_seconds"`
}
type Workload struct {
	ID             string `json:"id"`
	SigningKeyID   string `json:"signing_key_id"`
	PrivateKeyPath string `json:"private_key_path"`
	ProofTTLMS     int64  `json:"proof_ttl_ms"`
}
type RateLimit struct {
	Requests           uint32 `json:"requests"`
	WindowMS           int64  `json:"window_ms"`
	RedisAddress       string `json:"redis_address"`
	RedisUsername      string `json:"redis_username"`
	RedisPasswordFile  string `json:"redis_password_file"`
	RedisDatabase      int    `json:"redis_database"`
	RedisKeyPrefix     string `json:"redis_key_prefix"`
	OperationTimeoutMS int64  `json:"operation_timeout_ms"`
}
type HTTP struct {
	ReadHeaderTimeoutMS int64 `json:"read_header_timeout_ms"`
	ReadTimeoutMS       int64 `json:"read_timeout_ms"`
	WriteTimeoutMS      int64 `json:"write_timeout_ms"`
	IdleTimeoutMS       int64 `json:"idle_timeout_ms"`
	ShutdownTimeoutMS   int64 `json:"shutdown_timeout_ms"`
	MaxBodyBytes        int64 `json:"max_body_bytes"`
}
type GRPC struct {
	MaxReceiveBytes int `json:"max_receive_bytes"`
	MaxSendBytes    int `json:"max_send_bytes"`
}
type Reliability struct {
	MaxAttempts      uint32   `json:"max_attempts"`
	InitialBackoffMS int64    `json:"initial_backoff_ms"`
	MaxBackoffMS     int64    `json:"max_backoff_ms"`
	AttemptTimeoutMS int64    `json:"attempt_timeout_ms"`
	FailureThreshold uint32   `json:"failure_threshold"`
	OpenDurationMS   int64    `json:"open_duration_ms"`
	RetryableCodes   []string `json:"retryable_codes"`
}
type Health struct {
	ReadinessTimeoutMS int64 `json:"readiness_timeout_ms"`
}
type Telemetry struct {
	ServiceName     string  `json:"service_name"`
	ServiceVersion  string  `json:"service_version"`
	Endpoint        string  `json:"endpoint"`
	Insecure        bool    `json:"insecure"`
	SampleRatio     float64 `json:"sample_ratio"`
	ExportTimeoutMS int64   `json:"export_timeout_ms"`
}

func Load(path string) (Config, error) {
	var value Config
	data, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return value, err
	}
	if err = value.Validate(); err != nil {
		return value, err
	}
	return value, nil
}

func (c Config) Validate() error {
	if c.ListenAddress == "" || c.EngineAddress == "" || c.HumanAddress == "" || c.Identity.JWKSPath == "" || len(c.Identity.Issuers) == 0 || len(c.Identity.Audiences) == 0 || len(c.Identity.Algorithms) == 0 || c.Workload.ID == "" || c.Workload.SigningKeyID == "" || c.Workload.PrivateKeyPath == "" || len(c.TenantKeyScopes) == 0 {
		return errors.New("api-gateway configuration is incomplete")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return err
	}
	if c.Identity.MaxTokenBytes <= 0 || c.Identity.MaxJWKSKeys <= 0 || c.Workload.ProofTTLMS <= 0 || c.RateLimit.Requests == 0 || c.RateLimit.WindowMS <= 0 || c.RateLimit.RedisAddress == "" || c.RateLimit.RedisKeyPrefix == "" || c.RateLimit.OperationTimeoutMS <= 0 || c.HTTP.ReadHeaderTimeoutMS <= 0 || c.HTTP.ReadTimeoutMS <= 0 || c.HTTP.WriteTimeoutMS <= 0 || c.HTTP.IdleTimeoutMS <= 0 || c.HTTP.ShutdownTimeoutMS <= 0 || c.HTTP.MaxBodyBytes <= 0 || c.GRPC.MaxReceiveBytes <= 0 || c.GRPC.MaxSendBytes <= 0 {
		return errors.New("api-gateway bounds must be positive")
	}
	if c.Reliability.MaxAttempts == 0 ||
		c.Reliability.InitialBackoffMS <= 0 ||
		c.Reliability.MaxBackoffMS < c.Reliability.InitialBackoffMS ||
		c.Reliability.AttemptTimeoutMS <= 0 ||
		c.Reliability.FailureThreshold == 0 ||
		c.Reliability.OpenDurationMS <= 0 ||
		len(c.Reliability.RetryableCodes) == 0 ||
		c.Health.ReadinessTimeoutMS <= 0 ||
		c.Telemetry.ServiceName == "" ||
		c.Telemetry.ServiceVersion == "" ||
		c.Telemetry.Endpoint == "" ||
		c.Telemetry.SampleRatio < 0 ||
		c.Telemetry.SampleRatio > 1 ||
		c.Telemetry.ExportTimeoutMS <= 0 {
		return errors.New("api-gateway reliability and health configuration is invalid")
	}
	return nil
}
func (c HTTP) ReadHeaderTimeout() time.Duration {
	return time.Duration(c.ReadHeaderTimeoutMS) * time.Millisecond
}
func (c HTTP) ReadTimeout() time.Duration  { return time.Duration(c.ReadTimeoutMS) * time.Millisecond }
func (c HTTP) WriteTimeout() time.Duration { return time.Duration(c.WriteTimeoutMS) * time.Millisecond }
func (c HTTP) IdleTimeout() time.Duration  { return time.Duration(c.IdleTimeoutMS) * time.Millisecond }
func (c HTTP) ShutdownTimeout() time.Duration {
	return time.Duration(c.ShutdownTimeoutMS) * time.Millisecond
}
func (c Health) ReadinessTimeout() time.Duration {
	return time.Duration(c.ReadinessTimeoutMS) * time.Millisecond
}
func (c Telemetry) ExportTimeout() time.Duration {
	return time.Duration(c.ExportTimeoutMS) * time.Millisecond
}
