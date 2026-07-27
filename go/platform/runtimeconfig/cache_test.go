package runtimeconfig

import (
	"errors"
	"testing"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
)

func TestCacheRejectsStaleAndReturnsClones(t *testing.T) {
	cache, err := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := gatewaySnapshot(2)
	if err = cache.Install("tenant-a", snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.Get("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	loaded.ApiGateway.RateLimitRequests = 999
	again, _ := cache.Get("tenant-a")
	if again.GetApiGateway().GetRateLimitRequests() == 999 {
		t.Fatal("cache exposed mutable internal snapshot")
	}
	if err = cache.Install("tenant-a", gatewaySnapshot(1)); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("expected stale snapshot rejection, got %v", err)
	}
}

func gatewaySnapshot(ordinal uint64) *configurationv1.ResolvedConfigurationSnapshot {
	return &configurationv1.ResolvedConfigurationSnapshot{
		ConfigId: "config-a", ConfigVersion: "config-v1", PolicyVersion: "policy-v1",
		SchemaVersion: 1, Ordinal: ordinal,
		Owner:       configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
		ContentHash: make([]byte, 32),
		ResolvedScopes: []*configurationv1.ConfigurationScope{{
			Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT,
			Reference: "tenant-a",
		}},
		ApiGateway: &configurationv1.ApiGatewayPolicy{
			RateLimitRequests: 10, RateLimitWindowMs: 1000, UpstreamTimeoutMs: 1000,
			CircuitBreakerFailureThreshold: 5, CircuitBreakerOpenMs: 1000,
			BulkheadMaxConcurrency: 10, MaxRequestBodyBytes: 4096,
			MaxUpstreamResponseBytes: 8192, BatchChunkSize: 10, BatchConcurrency: 2,
		},
	}
}
