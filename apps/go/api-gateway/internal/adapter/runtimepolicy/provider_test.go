package runtimepolicy

import (
	"testing"
	"time"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

func TestProviderMapsTenantSnapshot(t *testing.T) {
	cache, err := runtimeconfig.NewCache(
		configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &configurationv1.ResolvedConfigurationSnapshot{
		ConfigId: "gateway-config", ConfigVersion: "gateway-v2", PolicyVersion: "policy-v1",
		SchemaVersion: 1, Ordinal: 2,
		Owner:       configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
		ContentHash: make([]byte, 32),
		ResolvedScopes: []*configurationv1.ConfigurationScope{{
			Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT,
			Reference: "tenant-a",
		}},
		ApiGateway: &configurationv1.ApiGatewayPolicy{
			RateLimitRequests: 50, RateLimitWindowMs: 2500,
			UpstreamTimeoutMs: 3000, CircuitBreakerFailureThreshold: 5,
			CircuitBreakerOpenMs: 1000, BulkheadMaxConcurrency: 20,
			MaxRequestBodyBytes: 4096, MaxUpstreamResponseBytes: 8192,
			BatchChunkSize: 10, BatchConcurrency: 2,
			UpstreamRetry: &configurationv1.RetryPolicy{
				MaxAttempts: 3, InitialBackoffMs: 25, MaxBackoffMs: 250,
				MultiplierMillis: 2000,
			},
			EncryptionKeyScope: "tenant-a/workflows",
		},
	}
	if err = cache.Install("tenant-a", snapshot); err != nil {
		t.Fatal(err)
	}
	provider, err := New(cache)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := provider.Policy("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if policy.RateLimitRequests != 50 ||
		policy.RateLimitWindow != 2500*time.Millisecond ||
		policy.UpstreamTimeout != 3*time.Second ||
		policy.CircuitBreakerFailureThreshold != 5 ||
		policy.CircuitBreakerOpen != time.Second ||
		policy.BulkheadMaxConcurrency != 20 ||
		policy.MaxRequestBodyBytes != 4096 ||
		policy.MaxUpstreamResponseBytes != 8192 ||
		policy.BatchChunkSize != 10 ||
		policy.BatchConcurrency != 2 ||
		policy.UpstreamRetry.MaxAttempts != 3 ||
		policy.EncryptionKeyScope != "tenant-a/workflows" {
		t.Fatalf("unexpected mapped policy: %+v", policy)
	}
}
