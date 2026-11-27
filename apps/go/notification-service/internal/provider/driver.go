package provider

import (
	"context"
	"errors"
	"time"
)

type OutcomeClass string

const (
	OutcomeOK         OutcomeClass = "OK"
	OutcomeRetryable  OutcomeClass = "RETRYABLE"
	OutcomeThrottled  OutcomeClass = "THROTTLED"
	OutcomePermanent  OutcomeClass = "PERMANENT"
	OutcomeAuthFailed OutcomeClass = "AUTH_FAILED"
	OutcomeAmbiguous  OutcomeClass = "AMBIGUOUS"
)

type AmbiguousPolicy string

const (
	AmbiguousResendOnFailover     AmbiguousPolicy = "RESEND_ON_FAILOVER"
	AmbiguousReconcileBeforeRetry AmbiguousPolicy = "RECONCILE_BEFORE_RETRY"
	AmbiguousNoRetry              AmbiguousPolicy = "NO_RETRY"
)

type SecretResolver interface {
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

type StaticSecretResolver map[string]string

func (s StaticSecretResolver) ResolveSecret(_ context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	val, ok := s[ref]
	if !ok {
		return "", errors.New("secret reference not found")
	}
	return val, nil
}

type ProviderRevision struct {
	TenantID       string
	ProviderID     string
	ProviderKey    string
	RevisionID     string
	Revision       uint64
	Channel        string
	DriverKind     string
	PresetKey      string
	ConfigJSON     []byte
	CredentialsRef string
	SenderIdentity map[string]string
}

type RenderedMessage struct {
	TenantID            string
	DeliveryID          string
	Channel             string
	CategoryKey         string
	RecipientSubjectRef string
	RecipientAddress    string
	FromAddress         string
	FromName            string
	ReplyTo             string
	Subject             string
	HTMLBody            string
	TextBody            string
	ActionURL           string
	UnsubscribeURL      string
	CorrelationID       string
	ProviderTemplateID  string
	TemplateParams      map[string]any
	Metadata            map[string]string
}

type SendResult struct {
	Outcome            OutcomeClass
	ProviderMessageID  string
	ProviderStatusCode string
	ErrorCode          string
	RetryAfter         time.Duration
	Latency            time.Duration
}

type Driver interface {
	Kind() string
	ConfigSchema() []byte
	Open(ctx context.Context, rev ProviderRevision, secrets SecretResolver) (Sender, error)
}

type Sender interface {
	Send(ctx context.Context, msg RenderedMessage) (SendResult, error)
	Close() error
}
