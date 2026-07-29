package runtimeconfig

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
)

type fakeResolver struct {
	snapshot *configurationv1.ResolvedConfigurationSnapshot
	calls    int
	request  *configurationv1.ResolveConfigurationRequest
}

func (f *fakeResolver) ResolveConfiguration(
	_ context.Context,
	request *configurationv1.ResolveConfigurationRequest,
	_ ...grpc.CallOption,
) (*configurationv1.ResolveConfigurationResponse, error) {
	f.calls++
	f.request = proto.Clone(request).(*configurationv1.ResolveConfigurationRequest)
	return &configurationv1.ResolveConfigurationResponse{Snapshot: f.snapshot}, nil
}

func TestInstancePublicationInstallsOnlyInstanceSnapshot(t *testing.T) {
	cache, _ := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	if err := cache.Install("tenant-a", gatewaySnapshot(1)); err != nil {
		t.Fatal(err)
	}
	override := gatewaySnapshot(2)
	override.ConfigId = "config-a"
	override.ResolvedScopes = append(override.ResolvedScopes, &configurationv1.ConfigurationScope{
		Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE,
		Reference: "instance-1",
	})
	resolver := &fakeResolver{snapshot: override}
	consumer := &fakeConsumer{}
	reloader, err := New(testReloaderConfig(), resolver, consumer, cache)
	if err != nil {
		t.Fatal(err)
	}
	event := publication(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	event.Scope = &configurationv1.ConfigurationScope{
		Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE,
		Reference: "instance-1",
	}
	payload, _ := proto.Marshal(event)
	if err = reloader.HandleRecord(context.Background(), &kgo.Record{Value: payload}); err != nil {
		t.Fatal(err)
	}
	if resolver.request.GetInstanceId() != "instance-1" {
		t.Fatalf("instance scope was not resolved: %+v", resolver.request)
	}
	tenant, _ := cache.Get("tenant-a")
	instance, _ := cache.GetForInstance("tenant-a", "instance-1")
	if tenant.GetOrdinal() != 1 || instance.GetOrdinal() != 2 {
		t.Fatalf("instance publication contaminated tenant snapshot: tenant=%d instance=%d", tenant.GetOrdinal(), instance.GetOrdinal())
	}
}

func testReloaderConfig() Config {
	return Config{
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
		PlatformReference:    "bpmp",
		EnvironmentReference: "test",
		InitialTenantIDs:     []string{"tenant-a"},
		ResolveTimeout:       time.Second,
		Kafka: kafkaconfig.Consumer{
			Bootstrap: kafkaconfig.Bootstrap{
				Brokers: []string{"kafka:9092"}, ClientID: "bpmp-gateway-configuration",
				SecurityProtocol: kafkaconfig.ProtocolPlaintext,
				DialTimeoutMS:    1000, RequestTimeoutMS: 1000,
			},
			Topic:         "bpmp.configuration.publications.v1.test",
			ConsumerGroup: "bpmp.gateway.configuration-reloader.v1.test",
			BatchSize:     10, MaxMessageBytes: 1 << 20, PollTimeoutMS: 100,
			SessionTimeoutMS: 1000,
		},
	}
}

type fakeConsumer struct {
	commits        int
	commitFailures int
}

func (*fakeConsumer) PollRecords(context.Context, int) kgo.Fetches { return kgo.Fetches{} }
func (f *fakeConsumer) CommitRecords(context.Context, ...*kgo.Record) error {
	f.commits++
	if f.commitFailures > 0 {
		f.commitFailures--
		return errors.New("simulated offset commit crash")
	}
	return nil
}

func TestHandleRecordResolvesBeforeCommitAndIgnoresOtherOwners(t *testing.T) {
	cache, _ := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	resolver := &fakeResolver{snapshot: gatewaySnapshot(2)}
	consumer := &fakeConsumer{}
	reloader, err := New(Config{
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
		PlatformReference:    "bpmp",
		EnvironmentReference: "test",
		InitialTenantIDs:     []string{"tenant-a"},
		ResolveTimeout:       time.Second,
		Kafka: kafkaconfig.Consumer{
			Bootstrap: kafkaconfig.Bootstrap{
				Brokers: []string{"kafka:9092"}, ClientID: "bpmp-gateway-configuration",
				SecurityProtocol: kafkaconfig.ProtocolPlaintext,
				DialTimeoutMS:    1000, RequestTimeoutMS: 1000,
			},
			Topic:         "bpmp.configuration.publications.v1.test",
			ConsumerGroup: "bpmp.gateway.configuration-reloader.v1.test",
			BatchSize:     10, MaxMessageBytes: 1 << 20, PollTimeoutMS: 100,
			SessionTimeoutMS: 1000,
		},
	}, resolver, consumer, cache)
	if err != nil {
		t.Fatal(err)
	}
	event := publication(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	payload, _ := proto.Marshal(event)
	if err = reloader.HandleRecord(context.Background(), &kgo.Record{Value: payload}); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || consumer.commits != 1 {
		t.Fatalf("resolve/commit order was not completed: calls=%d commits=%d", resolver.calls, consumer.commits)
	}
	event.Owner = configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE
	payload, _ = proto.Marshal(event)
	if err = reloader.HandleRecord(context.Background(), &kgo.Record{Value: payload}); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || consumer.commits != 2 {
		t.Fatal("unrelated owner was not acknowledged without resolving")
	}
}

func TestPublicationReplayAfterOffsetCommitFailureIsIdempotent(t *testing.T) {
	cache := mustGatewayCache(t)
	resolver := &fakeResolver{snapshot: gatewaySnapshot(2)}
	consumer := &fakeConsumer{commitFailures: 1}
	reloader, err := New(testReloaderConfig(), resolver, consumer, cache)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := proto.Marshal(publication(
		configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
	))
	record := &kgo.Record{Value: payload}
	if err = reloader.HandleRecord(context.Background(), record); err == nil {
		t.Fatal("expected simulated offset commit failure")
	}
	if _, err = cache.Get("tenant-a"); err != nil {
		t.Fatalf("snapshot must be installed before offset commit: %v", err)
	}
	if err = reloader.HandleRecord(context.Background(), record); err != nil {
		t.Fatalf("replayed publication must be idempotent: %v", err)
	}
	if resolver.calls != 2 || consumer.commits != 2 {
		t.Fatalf("unexpected replay calls=%d commits=%d", resolver.calls, consumer.commits)
	}
}

func TestRetireReplayKeepsCacheFailClosed(t *testing.T) {
	cache := mustGatewayCache(t)
	if err := cache.Install("tenant-a", gatewaySnapshot(1)); err != nil {
		t.Fatal(err)
	}
	consumer := &fakeConsumer{commitFailures: 1}
	reloader, err := New(testReloaderConfig(), &fakeResolver{}, consumer, cache)
	if err != nil {
		t.Fatal(err)
	}
	event := publication(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	event.Kind = configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_RETIRED
	payload, _ := proto.Marshal(event)
	record := &kgo.Record{Value: payload}
	if err = reloader.HandleRecord(context.Background(), record); err == nil {
		t.Fatal("expected simulated offset commit failure")
	}
	if _, err = cache.Get("tenant-a"); !errors.Is(err, ErrMissingSnapshot) {
		t.Fatalf("retired cache must fail closed before offset commit: %v", err)
	}
	if err = reloader.HandleRecord(context.Background(), record); err != nil {
		t.Fatalf("retire replay must be idempotent: %v", err)
	}
	if _, err = cache.Get("tenant-a"); !errors.Is(err, ErrMissingSnapshot) {
		t.Fatalf("retired cache was resurrected: %v", err)
	}
}

type blockingResolver struct{}

func (blockingResolver) ResolveConfiguration(
	ctx context.Context,
	_ *configurationv1.ResolveConfigurationRequest,
	_ ...grpc.CallOption,
) (*configurationv1.ResolveConfigurationResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBootstrapBoundsUnavailableResolver(t *testing.T) {
	config := testReloaderConfig()
	config.ResolveTimeout = 25 * time.Millisecond
	reloader, err := New(config, blockingResolver{}, &fakeConsumer{}, mustGatewayCache(t))
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	err = reloader.Bootstrap(context.Background())
	elapsed := time.Since(startedAt)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected resolver deadline, got %v", err)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("resolver deadline was not bounded: elapsed=%s", elapsed)
	}
}

func mustGatewayCache(t *testing.T) *Cache {
	t.Helper()
	cache, err := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func publication(owner configurationv1.ConfigurationOwner) *configurationv1.ConfigurationPublicationEvent {
	return &configurationv1.ConfigurationPublicationEvent{
		SchemaVersion: 1, EventId: "event-1", EventSequence: 1, TenantId: "tenant-a",
		ProfileId: "config-a", VersionId: "version-a", ConfigVersion: "config-v1",
		PolicyVersion: "policy-v1", Ordinal: 2, Owner: owner,
		Scope: &configurationv1.ConfigurationScope{
			Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT,
			Reference: "tenant-a",
		},
		ContentHash:       make([]byte, 32),
		Kind:              configurationv1.ConfigurationPublicationKind_CONFIGURATION_PUBLICATION_KIND_PUBLISHED,
		OccurredAtEpochMs: 1,
	}
}
