package kafkapublisher

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
)

type Publisher struct {
	client          *kgo.Client
	topic           string
	maxMessageBytes int
}

func New(config kafkaconfig.Producer) (*Publisher, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	options := []kgo.Opt{
		kgo.SeedBrokers(config.Brokers...),
		kgo.ClientID(config.ClientID),
		kgo.DialTimeout(config.DialTimeout()),
		kgo.RequestTimeoutOverhead(config.RequestTimeout()),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.MaxProduceRequestsInflightPerBroker(config.MaxInflight),
		kgo.ProducerBatchMaxBytes(int32(config.MaxMessageBytes)),
		kgo.ProduceRequestTimeout(config.MessageTimeout()),
		kgo.RecordDeliveryTimeout(config.MessageTimeout()),
	}
	if config.SecurityProtocol == kafkaconfig.ProtocolTLS {
		tlsConfig, err := loadTLS(config)
		if err != nil {
			return nil, err
		}
		options = append(options, kgo.DialTLSConfig(tlsConfig))
	}
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, err
	}
	return &Publisher{client: client, topic: config.Topic, maxMessageBytes: config.MaxMessageBytes}, nil
}

func (p *Publisher) Close() {
	p.client.Close()
}

func (p *Publisher) Ping(ctx context.Context) error {
	return p.client.Ping(ctx)
}

func (p *Publisher) Publish(ctx context.Context, publication domain.Publication) error {
	event, err := publicationEvent(publication)
	if err != nil {
		return err
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return err
	}
	if len(payload) > p.maxMessageBytes {
		return errors.New("configuration publication exceeds Kafka message bound")
	}
	result := p.client.ProduceSync(ctx, &kgo.Record{
		Topic: p.topic,
		Key:   []byte(publication.TenantID),
		Value: payload,
		Headers: []kgo.RecordHeader{
			{Key: "bpmp-event-id", Value: []byte(publication.EventID)},
			{Key: "bpmp-tenant-id", Value: []byte(publication.TenantID)},
			{Key: "bpmp-schema-version", Value: []byte("1")},
		},
	})
	if err = result.FirstErr(); err != nil {
		return fmt.Errorf("publish configuration event: %w", err)
	}
	return nil
}

func publicationEvent(publication domain.Publication) (*configurationv1.ConfigurationPublicationEvent, error) {
	owner := ownerToProto(publication.Owner)
	scope := scopeToProto(publication.Scope.Type)
	kind := configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_UNSPECIFIED
	switch publication.Kind {
	case "configuration.published":
		kind = configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_PUBLISHED
	case "configuration.rolled_back":
		kind = configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_ROLLED_BACK
	default:
		return nil, errors.New("configuration publication kind is invalid")
	}
	if owner == configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED ||
		scope == configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_UNSPECIFIED {
		return nil, errors.New("configuration publication scope is invalid")
	}
	return &configurationv1.ConfigurationPublicationEvent{
		SchemaVersion:     1,
		EventId:           publication.EventID,
		EventSequence:     publication.EventSequence,
		TenantId:          publication.TenantID,
		ProfileId:         publication.ProfileID,
		VersionId:         publication.VersionID,
		ConfigVersion:     publication.ConfigVersion,
		PolicyVersion:     publication.PolicyVersion,
		Ordinal:           publication.Ordinal,
		Owner:             owner,
		Scope:             &configurationv1.ConfigurationScope{Type: scope, Reference: publication.Scope.Reference},
		ContentHash:       publication.ContentHash[:],
		Kind:              kind,
		OccurredAtEpochMs: uint64(publication.OccurredAt.UnixMilli()),
	}, nil
}

func ownerToProto(owner domain.Owner) configurationv1.ConfigurationOwner {
	switch owner {
	case domain.OwnerEngine:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE
	case domain.OwnerAPIGateway:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY
	case domain.OwnerHumanRuntime:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME
	case domain.OwnerProjection:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_PROJECTION
	case domain.OwnerGovernance:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_GOVERNANCE
	default:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED
	}
}

func scopeToProto(scope domain.ScopeType) configurationv1.ConfigurationScopeType {
	switch scope {
	case domain.ScopePlatform:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_PLATFORM
	case domain.ScopeEnvironment:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_ENVIRONMENT
	case domain.ScopeTenant:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT
	case domain.ScopeWorkflowType:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_TYPE
	case domain.ScopeWorkflowVersion:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_VERSION
	case domain.ScopeApprovedInstanceOverride:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE
	default:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_UNSPECIFIED
	}
}

func loadTLS(config kafkaconfig.Producer) (*tls.Config, error) {
	caPEM, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("kafka CA file contains no certificates")
	}
	certificate, err := tls.LoadX509KeyPair(config.CertificateFile, config.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
	}, nil
}
