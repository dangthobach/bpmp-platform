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
	Requests    uint32 `json:"requests"`
	WindowMS    int64  `json:"window_ms"`
	MaxSubjects int    `json:"max_subjects"`
}
type HTTP struct {
	ReadHeaderTimeoutMS int64 `json:"read_header_timeout_ms"`
	ReadTimeoutMS       int64 `json:"read_timeout_ms"`
	WriteTimeoutMS      int64 `json:"write_timeout_ms"`
	IdleTimeoutMS       int64 `json:"idle_timeout_ms"`
	MaxBodyBytes        int64 `json:"max_body_bytes"`
}
type GRPC struct {
	MaxReceiveBytes int `json:"max_receive_bytes"`
	MaxSendBytes    int `json:"max_send_bytes"`
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
	if c.Identity.MaxTokenBytes <= 0 || c.Identity.MaxJWKSKeys <= 0 || c.Workload.ProofTTLMS <= 0 || c.RateLimit.Requests == 0 || c.RateLimit.WindowMS <= 0 || c.RateLimit.MaxSubjects <= 0 || c.HTTP.MaxBodyBytes <= 0 || c.GRPC.MaxReceiveBytes <= 0 || c.GRPC.MaxSendBytes <= 0 {
		return errors.New("api-gateway bounds must be positive")
	}
	return nil
}
func (c HTTP) ReadHeaderTimeout() time.Duration {
	return time.Duration(c.ReadHeaderTimeoutMS) * time.Millisecond
}
func (c HTTP) ReadTimeout() time.Duration  { return time.Duration(c.ReadTimeoutMS) * time.Millisecond }
func (c HTTP) WriteTimeout() time.Duration { return time.Duration(c.WriteTimeoutMS) * time.Millisecond }
func (c HTTP) IdleTimeout() time.Duration  { return time.Duration(c.IdleTimeoutMS) * time.Millisecond }
