package runtimeconfig

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type Resolver interface {
	ResolveConfiguration(
		context.Context,
		*configurationv1.ResolveConfigurationRequest,
		...grpc.CallOption,
	) (*configurationv1.ResolveConfigurationResponse, error)
}

type Consumer interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type Config struct {
	Owner                configurationv1.ConfigurationOwner
	PlatformReference    string
	EnvironmentReference string
	InitialTenantIDs     []string
	ResolveTimeout       time.Duration
	Kafka                kafkaconfig.Consumer
}

type Reloader struct {
	config   Config
	resolver Resolver
	consumer Consumer
	cache    *Cache
}

func New(config Config, resolver Resolver, consumer Consumer, cache *Cache) (*Reloader, error) {
	if config.Owner == configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED ||
		config.PlatformReference == "" ||
		config.EnvironmentReference == "" ||
		len(config.InitialTenantIDs) == 0 ||
		config.ResolveTimeout <= 0 ||
		resolver == nil ||
		consumer == nil ||
		cache == nil ||
		config.Kafka.Validate() != nil {
		return nil, errors.New("runtime configuration reloader is invalid")
	}
	seen := make(map[string]struct{}, len(config.InitialTenantIDs))
	for _, tenantID := range config.InitialTenantIDs {
		if tenantID == "" {
			return nil, errors.New("runtime configuration tenant is invalid")
		}
		if _, duplicate := seen[tenantID]; duplicate {
			return nil, errors.New("runtime configuration tenant is duplicated")
		}
		seen[tenantID] = struct{}{}
	}
	return &Reloader{config: config, resolver: resolver, consumer: consumer, cache: cache}, nil
}

func (r *Reloader) Bootstrap(ctx context.Context) error {
	for _, tenantID := range r.config.InitialTenantIDs {
		if err := r.resolveAndInstall(ctx, tenantID, nil); err != nil {
			return fmt.Errorf("bootstrap runtime configuration for tenant %s: %w", tenantID, err)
		}
	}
	return nil
}

func (r *Reloader) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		fetches := r.consumer.PollRecords(ctx, r.config.Kafka.BatchSize)
		if errs := fetches.Errors(); len(errs) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			return errs[0].Err
		}
		for _, record := range fetches.Records() {
			if err := r.HandleRecord(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Reloader) HandleRecord(ctx context.Context, record *kgo.Record) error {
	if record == nil {
		return errors.New("configuration publication record is required")
	}
	ctx, span := requestmeta.StartKafkaConsumerSpan(ctx, record)
	defer span.End()
	event := &configurationv1.ConfigurationPublicationEvent{}
	if err := proto.Unmarshal(record.Value, event); err != nil {
		requestmeta.RecordSpanError(span, err)
		return fmt.Errorf("decode configuration publication: %w", err)
	}
	if err := validateEvent(event); err != nil {
		return err
	}
	if event.GetOwner() == r.config.Owner {
		key, err := cacheKeyForEvent(event.GetTenantId(), event)
		if err != nil {
			return err
		}
		if event.GetKind() == configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_RETIRED {
			err = r.cache.RetireScoped(key, event.GetOrdinal())
		} else {
			err = r.resolveAndInstall(ctx, event.GetTenantId(), event)
		}
		if err != nil {
			return err
		}
	}
	if err := r.consumer.CommitRecords(ctx, record); err != nil {
		requestmeta.RecordSpanError(span, err)
		return fmt.Errorf("commit configuration publication: %w", err)
	}
	return nil
}

func (r *Reloader) resolveAndInstall(
	ctx context.Context,
	tenantID string,
	event *configurationv1.ConfigurationPublicationEvent,
) error {
	key, err := cacheKeyForEvent(tenantID, event)
	if err != nil {
		return err
	}
	resolveCtx, cancel := context.WithTimeout(ctx, r.config.ResolveTimeout)
	defer cancel()
	response, err := r.resolver.ResolveConfiguration(resolveCtx, &configurationv1.ResolveConfigurationRequest{
		TenantId:             tenantID,
		WorkflowType:         key.WorkflowType,
		WorkflowVersion:      key.WorkflowVersion,
		PlatformReference:    r.config.PlatformReference,
		EnvironmentReference: r.config.EnvironmentReference,
		InstanceId:           key.InstanceID,
		Owner:                r.config.Owner,
	}, grpc.WaitForReady(true))
	if err != nil {
		return fmt.Errorf(
			"resolve authoritative runtime configuration for tenant %s within %s: %w",
			tenantID,
			r.config.ResolveTimeout,
			err,
		)
	}
	snapshot := response.GetSnapshot()
	if event != nil && snapshot != nil && snapshot.GetConfigId() == event.GetProfileId() {
		if snapshot.GetOrdinal() < event.GetOrdinal() {
			return ErrStaleSnapshot
		}
		if snapshot.GetOrdinal() == event.GetOrdinal() &&
			!bytes.Equal(snapshot.GetContentHash(), event.GetContentHash()) {
			return errors.New("resolved configuration hash does not match publication")
		}
	}
	err = r.cache.InstallScoped(key, snapshot)
	if err != nil {
		return fmt.Errorf("install runtime configuration: %w", err)
	}
	return nil
}

func cacheKeyForEvent(
	tenantID string,
	event *configurationv1.ConfigurationPublicationEvent,
) (CacheKey, error) {
	key := CacheKey{TenantID: tenantID}
	if event == nil {
		return key, nil
	}
	switch event.GetScope().GetType() {
	case configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_TYPE:
		key.WorkflowType = event.GetWorkflowType()
		if key.WorkflowType == "" {
			key.WorkflowType = event.GetScope().GetReference()
		}
	case configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_VERSION:
		key.WorkflowType = event.GetWorkflowType()
		key.WorkflowVersion = event.GetWorkflowVersion()
		if key.WorkflowType == "" || key.WorkflowVersion == "" {
			workflowType, workflowVersion, found := strings.Cut(event.GetScope().GetReference(), ":")
			if !found {
				return CacheKey{}, errors.New("workflow version publication scope is invalid")
			}
			key.WorkflowType, key.WorkflowVersion = workflowType, workflowVersion
		}
	case configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE:
		key.InstanceID = event.GetInstanceId()
		if key.InstanceID == "" {
			key.InstanceID = event.GetScope().GetReference()
		}
	}
	if !key.valid() {
		return CacheKey{}, errors.New("configuration publication cache key is invalid")
	}
	return key, nil
}

func validateEvent(event *configurationv1.ConfigurationPublicationEvent) error {
	if event.GetSchemaVersion() != 1 ||
		event.GetEventId() == "" ||
		event.GetEventSequence() == 0 ||
		event.GetTenantId() == "" ||
		event.GetProfileId() == "" ||
		event.GetVersionId() == "" ||
		event.GetConfigVersion() == "" ||
		event.GetPolicyVersion() == "" ||
		event.GetOrdinal() == 0 ||
		event.GetOwner() == configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED ||
		event.GetScope() == nil ||
		event.GetScope().GetType() == configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_UNSPECIFIED ||
		event.GetScope().GetReference() == "" ||
		len(event.GetContentHash()) != 32 ||
		event.GetKind() == configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_UNSPECIFIED ||
		event.GetOccurredAtEpochMs() == 0 {
		return errors.New("configuration publication metadata is invalid")
	}
	return nil
}

func NewKafkaClient(config kafkaconfig.Consumer) (*kgo.Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	options := []kgo.Opt{
		kgo.SeedBrokers(config.Brokers...),
		kgo.ClientID(config.ClientID),
		kgo.ConsumerGroup(config.ConsumerGroup),
		kgo.ConsumeTopics(config.Topic),
		kgo.DisableAutoCommit(),
		kgo.DialTimeout(config.DialTimeout()),
		kgo.RequestTimeoutOverhead(config.RequestTimeout()),
		kgo.FetchMaxBytes(int32(config.MaxMessageBytes)),
		kgo.SessionTimeout(time.Duration(config.SessionTimeoutMS) * time.Millisecond),
	}
	if config.SecurityProtocol == kafkaconfig.ProtocolTLS {
		tlsConfig, err := loadTLS(config.Bootstrap)
		if err != nil {
			return nil, err
		}
		options = append(options, kgo.DialTLSConfig(tlsConfig))
	}
	return kgo.NewClient(options...)
}

func loadTLS(config kafkaconfig.Bootstrap) (*tls.Config, error) {
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
