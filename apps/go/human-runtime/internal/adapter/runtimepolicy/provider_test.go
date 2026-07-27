package runtimepolicy

import (
	"testing"
	"time"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

func TestProviderMapsTenantSnapshot(t *testing.T) {
	cache, err := runtimeconfig.NewCache(
		configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &configurationv1.ResolvedConfigurationSnapshot{
		ConfigId: "human-config", ConfigVersion: "human-v2", PolicyVersion: "policy-v1",
		SchemaVersion: 1, Ordinal: 2,
		Owner:       configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME,
		ContentHash: make([]byte, 32),
		ResolvedScopes: []*configurationv1.ConfigurationScope{{
			Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT,
			Reference: "tenant-a",
		}},
		HumanRuntime: &configurationv1.HumanRuntimePolicy{
			ProjectionBatchSize: 64, EscalationBatchSize: 16,
			EscalationLeaseMs: 5000, EscalationRetryMs: 1000,
			EscalationPollMs: 250, EngineCommandTimeoutMs: 3000,
			MaxAssignmentCandidates: 100, MaxDelegationDepth: 8,
		},
	}
	if err = cache.Install("tenant-a", snapshot); err != nil {
		t.Fatal(err)
	}
	provider, err := New(cache, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := provider.Policy()
	if err != nil {
		t.Fatal(err)
	}
	if policy.ProjectionBatchSize != 64 ||
		policy.EscalationBatchSize != 16 ||
		policy.EscalationLease != 5*time.Second ||
		policy.EscalationPoll != 250*time.Millisecond ||
		policy.EngineCommandTimeout != 3*time.Second ||
		policy.MaxDelegationDepth != 8 {
		t.Fatalf("unexpected mapped policy: %+v", policy)
	}
}
