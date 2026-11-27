package kafkapublisher

import (
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
)

func TestPublicationEventPreservesOrderingAndScope(t *testing.T) {
	t.Parallel()
	publication := domain.Publication{
		EventID: "event-1", EventSequence: 42, TenantID: "tenant-a",
		ProfileID: "profile-1", VersionID: "version-1", ConfigVersion: "config-1",
		PolicyVersion: "policy-1", Ordinal: 7, Owner: domain.OwnerEngine,
		Scope:       domain.Scope{Type: domain.ScopeWorkflowVersion, Reference: "orders:3"},
		ContentHash: [32]byte{1, 2, 3}, Kind: "configuration.published",
		OccurredAt: time.UnixMilli(1234).UTC(),
	}
	event, err := publicationEvent(publication)
	if err != nil {
		t.Fatal(err)
	}
	if event.GetEventSequence() != 42 || event.GetOrdinal() != 7 ||
		event.GetOwner() != configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE ||
		event.GetScope().GetReference() != "orders:3" ||
		event.GetOccurredAtEpochMs() != 1234 {
		t.Fatalf("publication event lost metadata: %+v", event)
	}
}

func TestPublicationEventSupportsNotificationOwner(t *testing.T) {
	t.Parallel()
	publication := domain.Publication{
		EventID: "event-2", EventSequence: 43, TenantID: "tenant-a",
		ProfileID: "profile-notif", VersionID: "version-notif", ConfigVersion: "config-notif-1",
		PolicyVersion: "policy-notif-1", Ordinal: 1, Owner: domain.OwnerNotification,
		Scope:       domain.Scope{Type: domain.ScopeTenant, Reference: "tenant-a"},
		ContentHash: [32]byte{9, 9, 9}, Kind: "configuration.published",
		OccurredAt: time.UnixMilli(5678).UTC(),
	}
	event, err := publicationEvent(publication)
	if err != nil {
		t.Fatal(err)
	}
	if event.GetOwner() != configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION {
		t.Fatalf("expected CONFIGURATION_OWNER_NOTIFICATION, got %v", event.GetOwner())
	}
}
