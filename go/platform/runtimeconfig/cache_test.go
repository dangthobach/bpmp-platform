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

func TestInstanceOverrideDoesNotReplaceTenantSnapshot(t *testing.T) {
	cache, err := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	if err != nil {
		t.Fatal(err)
	}
	tenant := gatewaySnapshot(2)
	if err = cache.Install("tenant-a", tenant); err != nil {
		t.Fatal(err)
	}
	instance := gatewaySnapshot(3)
	instance.ConfigId = "instance-config"
	instance.ApiGateway.RateLimitRequests = 1
	instance.ResolvedScopes = append(instance.ResolvedScopes, &configurationv1.ConfigurationScope{
		Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE,
		Reference: "instance-1",
	})
	if err = cache.InstallInstance("tenant-a", "instance-1", instance); err != nil {
		t.Fatal(err)
	}
	tenantLoaded, _ := cache.Get("tenant-a")
	instanceLoaded, _ := cache.GetForInstance("tenant-a", "instance-1")
	if tenantLoaded.GetConfigId() != tenant.GetConfigId() ||
		instanceLoaded.GetConfigId() != "instance-config" {
		t.Fatalf("instance override contaminated tenant cache: tenant=%s instance=%s", tenantLoaded.GetConfigId(), instanceLoaded.GetConfigId())
	}
}

func TestRetireTombstoneRejectsOlderPublication(t *testing.T) {
	cache, err := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY)
	if err != nil {
		t.Fatal(err)
	}
	key := CacheKey{TenantID: "tenant-a"}
	if err = cache.InstallScoped(key, gatewaySnapshot(2)); err != nil {
		t.Fatal(err)
	}
	if err = cache.RetireScoped(key, 3); err != nil {
		t.Fatal(err)
	}
	if err = cache.InstallScoped(key, gatewaySnapshot(2)); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("retired policy was resurrected: %v", err)
	}
	restored := gatewaySnapshot(4)
	if err = cache.InstallScoped(key, restored); err != nil {
		t.Fatalf("newer restore was rejected: %v", err)
	}
}

func TestNotificationOwnerInstallScopedLookupStaleAndNilPolicyRejection(t *testing.T) {
	cache, err := NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION)
	if err != nil {
		t.Fatal(err)
	}

	nilPolicy := notificationSnapshot(1)
	nilPolicy.Notification = nil
	if err = cache.Install("tenant-a", nilPolicy); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("expected nil NotificationPolicy rejection, got %v", err)
	}

	wrongOwner := gatewaySnapshot(1)
	if err = cache.Install("tenant-a", wrongOwner); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("expected wrong owner rejection, got %v", err)
	}

	tenantSnap := notificationSnapshot(2)
	if err = cache.Install("tenant-a", tenantSnap); err != nil {
		t.Fatal(err)
	}

	if err = cache.Install("tenant-a", notificationSnapshot(1)); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("expected stale notification snapshot rejection, got %v", err)
	}

	workflowKey := CacheKey{
		TenantID:     "tenant-a",
		WorkflowType: "loan-approval",
	}
	fallbackLoaded, err := cache.GetScoped(workflowKey)
	if err != nil {
		t.Fatal(err)
	}
	if fallbackLoaded.GetConfigId() != "notification-config-a" ||
		fallbackLoaded.GetNotification().GetDefaultProviderBulkheadConcurrency() != 32 {
		t.Fatalf("unexpected fallback notification snapshot: %+v", fallbackLoaded)
	}

	scopedSnap := notificationSnapshot(3)
	scopedSnap.ConfigId = "notification-workflow-override"
	scopedSnap.Notification.DefaultProviderBulkheadConcurrency = 64
	scopedSnap.ResolvedScopes = []*configurationv1.ConfigurationScope{{
		Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_TYPE,
		Reference: "loan-approval",
	}}
	if err = cache.InstallScoped(workflowKey, scopedSnap); err != nil {
		t.Fatal(err)
	}

	scopedLoaded, err := cache.GetScoped(workflowKey)
	if err != nil {
		t.Fatal(err)
	}
	tenantLoaded, err := cache.Get("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if scopedLoaded.GetConfigId() != "notification-workflow-override" ||
		scopedLoaded.GetNotification().GetDefaultProviderBulkheadConcurrency() != 64 ||
		tenantLoaded.GetConfigId() != "notification-config-a" {
		t.Fatalf("scoped notification lookup mismatch: scoped=%s tenant=%s", scopedLoaded.GetConfigId(), tenantLoaded.GetConfigId())
	}
}

func notificationSnapshot(ordinal uint64) *configurationv1.ResolvedConfigurationSnapshot {
	lane := &configurationv1.NotificationLanePolicy{
		WorkerConcurrency: 8,
		ClaimBatchSize:    32,
		LeaseDurationMs:   30000,
		Retry: &configurationv1.RetryPolicy{
			MaxAttempts:      5,
			InitialBackoffMs: 500,
			MaxBackoffMs:     30000,
			MultiplierMillis: 2000,
		},
		MaxQueueLagAlertMs: 5000,
	}
	return &configurationv1.ResolvedConfigurationSnapshot{
		ConfigId:      "notification-config-a",
		ConfigVersion: "config-v1",
		PolicyVersion: "policy-v1",
		SchemaVersion: 1,
		Ordinal:       ordinal,
		Owner:         configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION,
		ContentHash:   make([]byte, 32),
		ResolvedScopes: []*configurationv1.ConfigurationScope{{
			Type:      configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT,
			Reference: "tenant-a",
		}},
		Notification: &configurationv1.NotificationPolicy{
			TransactionalLane: lane,
			NormalLane:        lane,
			BulkLane:          lane,
			DefaultBreaker: &configurationv1.NotificationBreakerPolicy{
				RollingWindowMs:         60000,
				MinRequests:             20,
				FailureRateThresholdBps: 5000,
				OpenStateDurationMs:     30000,
				HalfOpenMaxProbes:       3,
				MaxManualOverrideTtlMs:  3600000,
			},
			RenderLimits: &configurationv1.NotificationRenderLimits{
				MaxPayloadBytes:            65536,
				MaxEmailHtmlBytes:          102400,
				MaxSmsSegments:             6,
				MaxPushBodyBytes:           4096,
				MaxArrayItems:              100,
				PreviewTimeoutMs:           2000,
				PreviewBulkheadConcurrency: 16,
				PreviewRateLimitPerMinute:  120,
			},
			Retention: &configurationv1.NotificationRetentionPolicy{
				TransactionalPayloadDays:      30,
				BulkPayloadDays:               7,
				DeliveryMetadataMonths:        13,
				AttemptCallbackDays:           90,
				MetricsHourlyMonths:           25,
				IdempotencyInboxDays:          14,
				InappMessageDays:              90,
				InstanceContextRetentionHours: 24,
				CleanupBatchSize:              500,
				CleanupIntervalMs:             60000,
			},
			DefaultProviderBulkheadConcurrency: 32,
			ProviderCallTimeoutMs:              5000,
			AllowedProviderHosts:               []string{"email.us-east-1.amazonaws.com"},
			OtpPerRecipientLimit:               5,
			OtpRateWindowMs:                    600000,
		},
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
