package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
)

type Config struct {
	ListenAddress    string        `json:"listen_address"`
	EngineAddress    string        `json:"engine_address"`
	HumanAddress     string        `json:"human_address"`
	ConfigurationURL string        `json:"configuration_url"`
	PublicTLS        PublicTLS     `json:"public_tls"`
	UpstreamTLS      UpstreamTLS   `json:"upstream_tls"`
	Identity         Identity      `json:"identity"`
	Workload         Workload      `json:"workload"`
	RateLimit        RateLimit     `json:"rate_limit"`
	HTTP             HTTP          `json:"http"`
	GRPC             GRPC          `json:"grpc"`
	Health           Health        `json:"health"`
	Telemetry        Telemetry     `json:"telemetry"`
	APIDocs          APIDocs       `json:"api_docs"`
	RuntimeConfig    RuntimeConfig `json:"runtime_configuration"`
}

type PublicTLS struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}
type UpstreamTLS struct {
	Certificate             string `json:"certificate"`
	PrivateKey              string `json:"private_key"`
	CA                      string `json:"ca"`
	EngineServerName        string `json:"engine_server_name"`
	HumanServerName         string `json:"human_server_name"`
	ConfigurationServerName string `json:"configuration_server_name"`
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
	RedisAddress       string `json:"redis_address"`
	RedisUsername      string `json:"redis_username"`
	RedisPasswordFile  string `json:"redis_password_file"`
	RedisDatabase      int    `json:"redis_database"`
	RedisKeyPrefix     string `json:"redis_key_prefix"`
	OperationTimeoutMS int64  `json:"operation_timeout_ms"`
}
type HTTP struct {
	ReadHeaderTimeoutMS int64  `json:"read_header_timeout_ms"`
	RequestTimeoutMS    int64  `json:"request_timeout_ms"`
	ReadTimeoutMS       int64  `json:"read_timeout_ms"`
	WriteTimeoutMS      int64  `json:"write_timeout_ms"`
	IdleTimeoutMS       int64  `json:"idle_timeout_ms"`
	ShutdownTimeoutMS   int64  `json:"shutdown_timeout_ms"`
	MaxHeaderBytes      int    `json:"max_header_bytes"`
	AdmissionRateRPS    uint32 `json:"admission_rate_rps"`
	AdmissionBurst      uint32 `json:"admission_burst"`
}
type GRPC struct {
	MaxReceiveBytes int `json:"max_receive_bytes"`
	MaxSendBytes    int `json:"max_send_bytes"`
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
type APIDocs struct {
	Enabled         bool   `json:"enabled"`
	OpenAPIPath     string `json:"openapi_path"`
	ReferencePath   string `json:"reference_path"`
	ScalarScriptURL string `json:"scalar_script_url"`
}
type RuntimeConfig struct {
	ResolverAddress      string               `json:"resolver_address"`
	PlatformReference    string               `json:"platform_reference"`
	EnvironmentReference string               `json:"environment_reference"`
	InitialTenantIDs     []string             `json:"initial_tenant_ids"`
	ResolveTimeoutMS     int64                `json:"resolve_timeout_ms"`
	Kafka                kafkaconfig.Consumer `json:"kafka"`
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
	if c.ListenAddress == "" || c.EngineAddress == "" || c.HumanAddress == "" ||
		c.ConfigurationURL == "" || c.UpstreamTLS.ConfigurationServerName == "" ||
		c.Identity.JWKSPath == "" || len(c.Identity.Issuers) == 0 ||
		len(c.Identity.Audiences) == 0 || len(c.Identity.Algorithms) == 0 ||
		c.Workload.ID == "" || c.Workload.SigningKeyID == "" ||
		c.Workload.PrivateKeyPath == "" {
		return errors.New("api-gateway configuration is incomplete")
	}
	if c.RuntimeConfig.ResolverAddress == "" ||
		c.RuntimeConfig.PlatformReference == "" ||
		c.RuntimeConfig.EnvironmentReference == "" ||
		len(c.RuntimeConfig.InitialTenantIDs) == 0 ||
		c.RuntimeConfig.ResolveTimeoutMS <= 0 ||
		c.RuntimeConfig.Kafka.Validate() != nil {
		return errors.New("api-gateway runtime configuration is invalid")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return err
	}
	if c.Identity.MaxTokenBytes <= 0 || c.Identity.MaxJWKSKeys <= 0 ||
		c.Workload.ProofTTLMS <= 0 || c.RateLimit.RedisAddress == "" ||
		c.RateLimit.RedisKeyPrefix == "" || c.RateLimit.OperationTimeoutMS <= 0 ||
		c.HTTP.ReadHeaderTimeoutMS <= 0 || c.HTTP.RequestTimeoutMS <= 0 ||
		c.HTTP.ReadTimeoutMS <= 0 ||
		c.HTTP.WriteTimeoutMS <= 0 || c.HTTP.IdleTimeoutMS <= 0 ||
		c.HTTP.ShutdownTimeoutMS <= 0 || c.HTTP.MaxHeaderBytes <= 0 ||
		c.HTTP.AdmissionRateRPS == 0 || c.HTTP.AdmissionBurst == 0 ||
		c.GRPC.MaxReceiveBytes <= 0 || c.GRPC.MaxSendBytes <= 0 {
		return errors.New("api-gateway bounds must be positive")
	}
	if c.Health.ReadinessTimeoutMS <= 0 ||
		c.Telemetry.ServiceName == "" ||
		c.Telemetry.ServiceVersion == "" ||
		c.Telemetry.Endpoint == "" ||
		c.Telemetry.SampleRatio < 0 ||
		c.Telemetry.SampleRatio > 1 ||
		c.Telemetry.ExportTimeoutMS <= 0 {
		return errors.New("api-gateway health and telemetry configuration is invalid")
	}
	if err := c.APIDocs.Validate(); err != nil {
		return err
	}
	return nil
}
func (c APIDocs) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !validDocumentationPath(c.OpenAPIPath) ||
		!validDocumentationPath(c.ReferencePath) ||
		c.OpenAPIPath == c.ReferencePath {
		return errors.New("api-gateway documentation paths are invalid")
	}
	scriptURL, err := url.Parse(c.ScalarScriptURL)
	if err != nil || scriptURL.Scheme != "https" || scriptURL.Host == "" ||
		scriptURL.User != nil || scriptURL.RawQuery != "" || scriptURL.Fragment != "" {
		return errors.New("api-gateway Scalar script URL must be an HTTPS resource")
	}
	return nil
}
func validDocumentationPath(value string) bool {
	if value == "" || value[0] != '/' || value == "/" ||
		strings.ContainsAny(value, "{}*") || path.Clean(value) != value {
		return false
	}
	return value != "/livez" && value != "/readyz" &&
		value != "/v1" && !strings.HasPrefix(value, "/v1/")
}
func (c HTTP) ReadHeaderTimeout() time.Duration {
	return time.Duration(c.ReadHeaderTimeoutMS) * time.Millisecond
}
func (c HTTP) RequestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutMS) * time.Millisecond
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
func (c RuntimeConfig) ResolveTimeout() time.Duration {
	return time.Duration(c.ResolveTimeoutMS) * time.Millisecond
}
