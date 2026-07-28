package kafkaconfig

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ProtocolPlaintext = "PLAINTEXT"
	ProtocolTLS       = "TLS"

	maxBootstrapTimeoutMS = 300_000
	maxBatchSize          = 1_000
	maxInflight           = 5
	maxMessageBytes       = 100 * 1024 * 1024
)

var (
	ErrInvalidConfig = errors.New("kafka configuration is invalid")
	namePattern      = regexp.MustCompile(`^bpmp\.[a-z0-9][a-z0-9-]*\.[a-z0-9][a-z0-9-]*\.v[1-9][0-9]*\.[a-z0-9][a-z0-9-]*$`)
	clientIDPattern  = regexp.MustCompile(`^bpmp-[a-z0-9][a-z0-9-]*-[a-z0-9][a-z0-9-]*$`)
)

// Bootstrap contains transport settings that must exist before dynamic
// configuration can be consumed. Secret values remain external references.
type Bootstrap struct {
	Brokers          []string `json:"brokers"`
	ClientID         string   `json:"client_id"`
	SecurityProtocol string   `json:"security_protocol"`
	CAFile           string   `json:"ca_file,omitempty"`
	CertificateFile  string   `json:"certificate_file,omitempty"`
	PrivateKeyFile   string   `json:"private_key_file,omitempty"`
	DialTimeoutMS    int64    `json:"dial_timeout_ms"`
	RequestTimeoutMS int64    `json:"request_timeout_ms"`
}

type Producer struct {
	Bootstrap
	Topic             string `json:"topic"`
	MessageTimeoutMS  int64  `json:"message_timeout_ms"`
	MaxInflight       int    `json:"max_inflight"`
	MaxMessageBytes   int    `json:"max_message_bytes"`
	RequiredAcks      string `json:"required_acks"`
	EnableIdempotence bool   `json:"enable_idempotence"`
}

type Consumer struct {
	Bootstrap
	Topic            string `json:"topic"`
	ConsumerGroup    string `json:"consumer_group"`
	BatchSize        int    `json:"batch_size"`
	MaxMessageBytes  int    `json:"max_message_bytes"`
	PollTimeoutMS    int64  `json:"poll_timeout_ms"`
	SessionTimeoutMS int64  `json:"session_timeout_ms"`
}

func (c Bootstrap) Validate() error {
	if len(c.Brokers) == 0 || len(c.ClientID) > 255 ||
		!clientIDPattern.MatchString(c.ClientID) ||
		c.DialTimeoutMS <= 0 || c.DialTimeoutMS > maxBootstrapTimeoutMS ||
		c.RequestTimeoutMS <= 0 || c.RequestTimeoutMS > maxBootstrapTimeoutMS {
		return ErrInvalidConfig
	}
	seenBrokers := make(map[string]struct{}, len(c.Brokers))
	for _, broker := range c.Brokers {
		if strings.Contains(broker, "://") {
			return ErrInvalidConfig
		}
		host, port, err := net.SplitHostPort(broker)
		if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return ErrInvalidConfig
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65_535 {
			return ErrInvalidConfig
		}
		if _, duplicate := seenBrokers[broker]; duplicate {
			return ErrInvalidConfig
		}
		seenBrokers[broker] = struct{}{}
	}
	switch c.SecurityProtocol {
	case ProtocolPlaintext:
		if c.CAFile != "" || c.CertificateFile != "" || c.PrivateKeyFile != "" {
			return ErrInvalidConfig
		}
	case ProtocolTLS:
		if c.CAFile == "" || c.CertificateFile == "" || c.PrivateKeyFile == "" {
			return ErrInvalidConfig
		}
	default:
		return ErrInvalidConfig
	}
	return nil
}

func (c Producer) Validate() error {
	if err := c.Bootstrap.Validate(); err != nil {
		return err
	}
	if ValidateTopic(c.Topic) != nil ||
		c.MessageTimeoutMS <= 0 ||
		c.MessageTimeoutMS > maxBootstrapTimeoutMS ||
		c.MaxInflight <= 0 ||
		c.MaxInflight > maxInflight ||
		c.MaxMessageBytes <= 0 ||
		c.MaxMessageBytes > maxMessageBytes ||
		c.RequiredAcks != "ALL" ||
		!c.EnableIdempotence {
		return ErrInvalidConfig
	}
	return nil
}

func (c Consumer) Validate() error {
	if err := c.Bootstrap.Validate(); err != nil {
		return err
	}
	if ValidateTopic(c.Topic) != nil ||
		ValidateConsumerGroup(c.ConsumerGroup) != nil ||
		c.BatchSize <= 0 ||
		c.BatchSize > maxBatchSize ||
		c.MaxMessageBytes <= 0 ||
		c.MaxMessageBytes > maxMessageBytes ||
		c.PollTimeoutMS <= 0 ||
		c.PollTimeoutMS > maxBootstrapTimeoutMS ||
		c.SessionTimeoutMS <= c.PollTimeoutMS ||
		c.SessionTimeoutMS > maxBootstrapTimeoutMS {
		return ErrInvalidConfig
	}
	return nil
}

func ValidateTopic(value string) error {
	if len(value) > 249 || !namePattern.MatchString(value) {
		return ErrInvalidConfig
	}
	return nil
}

func ValidateConsumerGroup(value string) error {
	if len(value) > 255 || !namePattern.MatchString(value) {
		return ErrInvalidConfig
	}
	return nil
}

func (c Bootstrap) DialTimeout() time.Duration {
	return time.Duration(c.DialTimeoutMS) * time.Millisecond
}

func (c Bootstrap) RequestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutMS) * time.Millisecond
}

func (c Producer) MessageTimeout() time.Duration {
	return time.Duration(c.MessageTimeoutMS) * time.Millisecond
}

func (c Consumer) PollTimeout() time.Duration {
	return time.Duration(c.PollTimeoutMS) * time.Millisecond
}
