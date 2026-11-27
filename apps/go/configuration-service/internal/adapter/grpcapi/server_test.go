package grpcapi

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

type resolveRepoStub struct {
	lastLookup domain.ResolutionLookup
	resolved   domain.ResolvedConfiguration
	err        error
}

func (*resolveRepoStub) Create(context.Context, domain.Actor, domain.Profile, domain.Version) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*resolveRepoStub) List(context.Context, string, string, string, int) ([]domain.Profile, error) {
	return nil, nil
}
func (*resolveRepoStub) Get(context.Context, string, string) (domain.Profile, []domain.Version, error) {
	return domain.Profile{}, nil, nil
}
func (*resolveRepoStub) ProfileOwner(context.Context, string, string) (domain.Owner, error) {
	return domain.OwnerNotification, nil
}
func (*resolveRepoStub) AddDraft(context.Context, domain.Actor, string, int64, domain.Version) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*resolveRepoStub) Publish(context.Context, domain.Actor, string, string, int64, string, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*resolveRepoStub) Rollback(context.Context, domain.Actor, string, string, int64, domain.Version, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*resolveRepoStub) Restore(context.Context, domain.Actor, string, string, int64, domain.Version, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*resolveRepoStub) Retire(context.Context, domain.Actor, string, int64, string, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (r *resolveRepoStub) Resolve(_ context.Context, lookup domain.ResolutionLookup) (domain.ResolvedConfiguration, error) {
	r.lastLookup = lookup
	if r.err != nil {
		return domain.ResolvedConfiguration{}, r.err
	}
	return r.resolved, nil
}

func TestResolveConfigurationNotificationSnapshotInstallsInRuntimeCache(t *testing.T) {
	t.Parallel()
	rawPolicy := []byte(`{
		"transactionalLane":{"workerConcurrency":16,"claimBatchSize":32,"leaseDurationMs":"30000","retry":{"maxAttempts":5,"initialBackoffMs":"250","maxBackoffMs":"10000","multiplierMillis":2000},"maxQueueLagAlertMs":"2000"},
		"normalLane":{"workerConcurrency":8,"claimBatchSize":64,"leaseDurationMs":"45000","retry":{"maxAttempts":6,"initialBackoffMs":"500","maxBackoffMs":"30000","multiplierMillis":2000},"maxQueueLagAlertMs":"15000"},
		"bulkLane":{"workerConcurrency":4,"claimBatchSize":128,"leaseDurationMs":"60000","retry":{"maxAttempts":4,"initialBackoffMs":"1000","maxBackoffMs":"60000","multiplierMillis":2000},"maxQueueLagAlertMs":"60000"},
		"defaultBreaker":{"rollingWindowMs":"60000","minRequests":20,"failureRateThresholdBps":5000,"openStateDurationMs":"30000","halfOpenMaxProbes":3,"maxManualOverrideTtlMs":"3600000"},
		"renderLimits":{"maxPayloadBytes":65536,"maxEmailHtmlBytes":102400,"maxSmsSegments":6,"maxPushBodyBytes":4096,"maxArrayItems":100,"previewTimeoutMs":"2000","previewBulkheadConcurrency":16,"previewRateLimitPerMinute":120},
		"retention":{"transactionalPayloadDays":30,"bulkPayloadDays":7,"deliveryMetadataMonths":13,"attemptCallbackDays":90,"metricsHourlyMonths":25,"idempotencyInboxDays":14,"inappMessageDays":90,"instanceContextRetentionHours":24,"cleanupBatchSize":500,"cleanupIntervalMs":"60000"},
		"defaultProviderBulkheadConcurrency":32,
		"providerCallTimeoutMs":"5000",
		"allowedProviderHosts":["email.us-east-1.amazonaws.com"],
		"otpPerRecipientLimit":5,
		"otpRateWindowMs":"600000"
	}`)
	_, canonical, hash, err := domain.ParsePolicy(domain.OwnerNotification, rawPolicy)
	if err != nil {
		t.Fatal(err)
	}

	repo := &resolveRepoStub{
		resolved: domain.ResolvedConfiguration{
			Profile: domain.Profile{
				ID:       "profile-notif-1",
				TenantID: "tenant-a",
				Owner:    domain.OwnerNotification,
				Name:     "Notification defaults",
				Scope:    domain.Scope{Type: domain.ScopeTenant, Reference: "tenant-a"},
			},
			Version: domain.Version{
				ID:            "version-notif-1",
				ProfileID:     "profile-notif-1",
				TenantID:      "tenant-a",
				Ordinal:       3,
				ConfigVersion: "config-notif-v3",
				PolicyVersion: "policy-notif-v3",
				SchemaVersion: 1,
				Status:        domain.StatusPublished,
				ValuesJSON:    canonical,
				ContentHash:   hash,
			},
		},
	}
	svc, err := application.New(repo, application.Config{
		ReadCapability:   "configuration.read",
		ManageCapability: "configuration.manage",
		DefaultPageSize:  50,
		MaxPageSize:      200,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(svc)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := server.ResolveConfiguration(context.Background(), &configurationv1.ResolveConfigurationRequest{
		TenantId:             "tenant-a",
		PlatformReference:    "bpmp",
		EnvironmentReference: "local",
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION,
	})
	if err != nil {
		t.Fatalf("ResolveConfiguration failed: %v", err)
	}
	if repo.lastLookup.Owner != domain.OwnerNotification {
		t.Fatalf("expected lookup owner %s, got %s", domain.OwnerNotification, repo.lastLookup.Owner)
	}
	snap := resp.GetSnapshot()
	if snap.GetOwner() != configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION ||
		snap.GetNotification() == nil ||
		snap.GetNotification().GetTransactionalLane().GetWorkerConcurrency() != 16 ||
		snap.GetNotification().GetDefaultBreaker().GetFailureRateThresholdBps() != 5000 {
		t.Fatalf("unexpected notification snapshot: %+v", snap)
	}

	cache, err := runtimeconfig.NewCache(configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION)
	if err != nil {
		t.Fatal(err)
	}
	if err = cache.Install("tenant-a", snap); err != nil {
		t.Fatalf("resolved snapshot rejected by runtimeconfig.Cache: %v", err)
	}

	// Verify corrupted stored JSON fails closed with DataLoss.
	repo.resolved.Version.ValuesJSON = []byte(`{"defaultProviderBulkheadConcurrency":0}`)
	_, err = server.ResolveConfiguration(context.Background(), &configurationv1.ResolveConfigurationRequest{
		TenantId:             "tenant-a",
		PlatformReference:    "bpmp",
		EnvironmentReference: "local",
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_NOTIFICATION,
	})
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("expected DataLoss on invalid stored NotificationPolicy, got %v", err)
	}
}
