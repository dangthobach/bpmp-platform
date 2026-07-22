package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

type runtimeConfig struct {
	ListenAddress   string           `json:"listen_address"`
	PostgresDSN     string           `json:"postgres_dsn"`
	ApplyMigrations bool             `json:"apply_migrations"`
	MigrationPath   string           `json:"migration_path"`
	EngineAddress   string           `json:"engine_address"`
	TLS             tlsConfig        `json:"tls"`
	Kafka           kafkaConfig      `json:"kafka"`
	Identity        identityConfig   `json:"identity"`
	Workload        workloadConfig   `json:"workload"`
	GRPC            grpcConfig       `json:"grpc"`
	Escalation      escalationConfig `json:"escalation"`
}

type tlsConfig struct {
	ServerCertificate string `json:"server_certificate"`
	ServerPrivateKey  string `json:"server_private_key"`
	ClientCertificate string `json:"client_certificate"`
	ClientPrivateKey  string `json:"client_private_key"`
	ClientCA          string `json:"client_ca"`
	EngineCA          string `json:"engine_ca"`
	EngineServerName  string `json:"engine_server_name"`
}

type kafkaConfig struct {
	Brokers             []string `json:"brokers"`
	CommittedEventTopic string   `json:"committed_event_topic"`
	EscalationTopic     string   `json:"escalation_topic"`
	ConsumerGroup       string   `json:"consumer_group"`
	BatchSize           int      `json:"batch_size"`
}

type identityConfig struct {
	JWKSPath          string            `json:"jwks_path"`
	InternalKeys      map[string]string `json:"internal_keys"`
	Issuers           []string          `json:"issuers"`
	Audiences         []string          `json:"audiences"`
	AllowedJWTMethods []string          `json:"allowed_jwt_methods"`
	WorkloadID        string            `json:"workload_id"`
	MaxProofBytes     int               `json:"max_proof_bytes"`
	MaxJWKSKeys       int               `json:"max_jwks_keys"`
	MaxRoles          int               `json:"max_roles"`
	MaxCapabilities   int               `json:"max_capabilities"`
	ClockSkewMS       int64             `json:"clock_skew_ms"`
}

type workloadConfig struct {
	ID             string `json:"id"`
	SigningKeyID   string `json:"signing_key_id"`
	PrivateKeyPath string `json:"private_key_path"`
	ProofTTLMS     int64  `json:"proof_ttl_ms"`
}

type grpcConfig struct {
	MaxReceiveBytes int `json:"max_receive_bytes"`
	MaxSendBytes    int `json:"max_send_bytes"`
}

type escalationConfig struct {
	WorkerID  string `json:"worker_id"`
	BatchSize int    `json:"batch_size"`
	LeaseMS   int64  `json:"lease_ms"`
	RetryMS   int64  `json:"retry_ms"`
	PollMS    int64  `json:"poll_ms"`
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
	if c.ListenAddress == "" || c.PostgresDSN == "" || c.EngineAddress == "" || len(c.Kafka.Brokers) == 0 || c.Kafka.CommittedEventTopic == "" || c.Kafka.EscalationTopic == "" || c.Kafka.ConsumerGroup == "" || c.Identity.JWKSPath == "" || len(c.Identity.InternalKeys) == 0 || c.Workload.ID == "" || c.Workload.SigningKeyID == "" || c.Workload.PrivateKeyPath == "" || c.Escalation.WorkerID == "" {
		return errors.New("human-runtime configuration is incomplete")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return err
	}
	if c.GRPC.MaxReceiveBytes <= 0 || c.GRPC.MaxSendBytes <= 0 || c.Kafka.BatchSize <= 0 || c.Escalation.BatchSize <= 0 || c.Workload.ProofTTLMS <= 0 || c.Escalation.LeaseMS <= 0 || c.Escalation.RetryMS <= 0 || c.Escalation.PollMS <= 0 {
		return errors.New("human-runtime bounds and durations must be positive")
	}
	return nil
}

func (c escalationConfig) lease() time.Duration { return time.Duration(c.LeaseMS) * time.Millisecond }
func (c escalationConfig) retry() time.Duration { return time.Duration(c.RetryMS) * time.Millisecond }
func (c escalationConfig) poll() time.Duration  { return time.Duration(c.PollMS) * time.Millisecond }
