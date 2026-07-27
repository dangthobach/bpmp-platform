package runtimeconfig

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
)

type fakeResolver struct {
	snapshot *configurationv1.ResolvedConfigurationSnapshot
	calls    int
}

func (f *fakeResolver) ResolveConfiguration(
	context.Context,
	*configurationv1.ResolveConfigurationRequest,
	...grpc.CallOption,
) (*configurationv1.ResolveConfigurationResponse, error) {
	f.calls++
	return &configurationv1.ResolveConfigurationResponse{Snapshot: f.snapshot}, nil
}

type fakeConsumer struct{ commits int }

func (*fakeConsumer) PollRecords(context.Context, int) kgo.Fetches { return kgo.Fetches{} }
func (f *fakeConsumer) CommitRecords(context.Context, ...*kgo.Record) error {
	f.commits++
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
