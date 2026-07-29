package tenantconsumer

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	tenancyv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/tenancy/v1"
)

func TestDecodeTenantLifecycleContract(t *testing.T) {
	event := &tenancyv1.TenantLifecycleEvent{
		SchemaVersion:     1,
		EventId:           "event-1",
		EventSequence:     7,
		TenantId:          "11111111-1111-1111-1111-111111111111",
		TenantCode:        "tenant-a",
		TenantVersion:     3,
		Kind:              tenancyv1.TenantLifecycleKind_TENANT_LIFECYCLE_KIND_UPDATED,
		ActorRef:          "operator",
		CorrelationId:     "correlation-1",
		OccurredAtEpochMs: 1000,
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decode(&kgo.Record{Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.GetEventSequence() != event.GetEventSequence() ||
		decoded.GetTenantVersion() != event.GetTenantVersion() {
		t.Fatalf("tenant lifecycle metadata changed: %+v", decoded)
	}
}

func TestDecodeRejectsUnboundedTenantLifecycle(t *testing.T) {
	event := &tenancyv1.TenantLifecycleEvent{
		SchemaVersion: 1,
		EventId: "event-1",
		EventSequence: ^uint64(0),
		TenantId: "tenant-a",
		TenantCode: "tenant-a",
		TenantVersion: 1,
		Kind: tenancyv1.TenantLifecycleKind_TENANT_LIFECYCLE_KIND_UPDATED,
		ActorRef: "operator",
		CorrelationId: "correlation-1",
		OccurredAtEpochMs: 1000,
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decode(&kgo.Record{Value: payload}); err == nil {
		t.Fatal("overflowing event sequence was accepted")
	}
}
