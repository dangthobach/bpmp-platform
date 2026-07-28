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
			QueryDefaultPageSize: 50, QueryMaxPageSize: 200,
			EngineRetry: &configurationv1.RetryPolicy{
				MaxAttempts: 3, InitialBackoffMs: 25, MaxBackoffMs: 250, MultiplierMillis: 2000,
			},
			EngineCircuitBreakerFailureThreshold: 5,
			EngineCircuitBreakerOpenMs:           1000,
			EngineRetryableCodes:                 []string{"UNAVAILABLE", "DEADLINE_EXCEEDED"},
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
		policy.MaxAssignmentCandidates != 100 ||
		policy.MaxDelegationDepth != 8 ||
		policy.QueryDefaultPageSize != 50 ||
		policy.QueryMaxPageSize != 200 ||
		policy.EngineRetry.MaxAttempts != 3 ||
		policy.EngineRetry.InitialBackoff != 25*time.Millisecond ||
		policy.EngineCircuitThreshold != 5 ||
		policy.EngineCircuitOpen != time.Second ||
		len(policy.EngineRetryableCodes) != 2 {
		t.Fatalf("unexpected mapped policy: %+v", policy)
	}
}
