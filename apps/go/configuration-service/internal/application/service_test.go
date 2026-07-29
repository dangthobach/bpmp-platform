package application

import (
	"context"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type repositoryStub struct {
	created  domain.Profile
	versions []domain.Version
}

func (r *repositoryStub) Create(_ context.Context, _ domain.Actor, profile domain.Profile, version domain.Version) (domain.Profile, error) {
	profile.Latest = &version
	r.created = profile
	return profile, nil
}
func (*repositoryStub) List(context.Context, string, string, string, int) ([]domain.Profile, error) {
	return nil, nil
}
func (r *repositoryStub) Get(context.Context, string, string) (domain.Profile, []domain.Version, error) {
	return domain.Profile{}, r.versions, nil
}

func TestDiffReturnsStableJSONPointerPaths(t *testing.T) {
	repository := &repositoryStub{versions: []domain.Version{
		{
			ID:            "11111111-1111-1111-1111-111111111111",
			SchemaVersion: 1,
			ValuesJSON:    []byte(`{"batch":{"size":10},"enabled":true}`),
			ContentHash:   [32]byte{1},
		},
		{
			ID:            "22222222-2222-2222-2222-222222222222",
			SchemaVersion: 2,
			ValuesJSON:    []byte(`{"batch":{"size":20},"enabled":true,"mode":"safe"}`),
			ContentHash:   [32]byte{2},
		},
	}}
	service, err := New(repository, Config{
		ReadCapability: "configuration.read", ManageCapability: "configuration.manage",
		DefaultPageSize: 50, MaxPageSize: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := service.Diff(context.Background(), domain.Actor{
		TenantID: "tenant-a", ActorID: "operator", CorrelationID: "correlation-1",
		Capabilities: map[string]struct{}{"configuration.read": {}},
	}, "profile-a", repository.versions[0].ID, repository.versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.SchemaChange || len(diff.Differences) != 2 ||
		diff.Differences[0].Path != "/batch/size" ||
		diff.Differences[1].Path != "/mode" {
		t.Fatalf("unexpected deterministic diff: %+v", diff)
	}
}
func (*repositoryStub) ProfileOwner(context.Context, string, string) (domain.Owner, error) {
	return domain.OwnerEngine, nil
}
func (*repositoryStub) AddDraft(context.Context, domain.Actor, string, int64, domain.Version) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*repositoryStub) Publish(context.Context, domain.Actor, string, string, int64, string, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*repositoryStub) Rollback(context.Context, domain.Actor, string, string, int64, domain.Version, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*repositoryStub) Restore(context.Context, domain.Actor, string, string, int64, domain.Version, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*repositoryStub) Retire(context.Context, domain.Actor, string, int64, string, time.Time) (domain.Profile, error) {
	return domain.Profile{}, nil
}
func (*repositoryStub) Resolve(context.Context, domain.ResolutionLookup) (domain.ResolvedConfiguration, error) {
	return domain.ResolvedConfiguration{}, nil
}

func TestCreateRequiresConfiguredCapabilityAndCanonicalizesPolicy(t *testing.T) {
	repository := &repositoryStub{}
	service, err := New(repository, Config{
		ReadCapability: "configuration.read", ManageCapability: "configuration.manage",
		DefaultPageSize: 50, MaxPageSize: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateInput{
		Name: "Engine defaults", Owner: domain.OwnerEngine,
		Scope:         domain.Scope{Type: domain.ScopeTenant, Reference: "tenant-a"},
		SchemaVersion: 1, PolicyVersion: "policy-1", Reason: "initial",
		Values: validPolicy(),
	}
	actor := domain.Actor{
		TenantID: "tenant-a", ActorID: "operator", CorrelationID: "correlation-1",
		Capabilities: map[string]struct{}{"configuration.manage": {}},
	}
	created, err := service.Create(context.Background(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.TenantID != actor.TenantID || created.Latest == nil ||
		created.Latest.Status != domain.StatusDraft || len(created.Latest.ValuesJSON) == 0 {
		t.Fatalf("unexpected profile: %+v", created)
	}
	delete(actor.Capabilities, "configuration.manage")
	if _, err = service.Create(context.Background(), actor, input); err != domain.ErrUnauthorized {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

func validPolicy() []byte {
	return []byte(`{
		"snapshotIntervalEvents":100,"maxEventsPerDecision":64,"commandTimeoutMs":"15000",
		"optimisticConflictRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000},
		"localWasm":{"maxModuleBytes":"1048576","maxInputBytes":"262144","maxOutputBytes":"262144","maxMemoryBytes":"16777216","maxWasmStackBytes":"1048576","maxTableElements":1000,"maxInstances":4,"maxTables":4,"maxMemories":1,"fuel":"1000000"},
		"eventPayloadKeyScope":"tenant/operational","authorizationAuditKeyScope":"tenant/audit","maxMultiInstanceCardinality":1000,"defaultMultiInstanceParallelism":8,
		"boundaryRuntime":{"projectionBatchSize":100,"dispatchBatchSize":50,"maxDispatchAttempts":5,"retryDelayMs":"1000","leaseDurationMs":"30000","maxTimerHorizonMs":"31536000000","maxExpressionBytes":65536,"workerId":"boundary-worker","maxSignalIdBytes":256,"maxReferenceBytes":512,"maxSubscriptionsPerInstance":100},
		"workers":{"pollIntervalMs":"100","outboxBatchSize":100,"outboxRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000},"localTaskBatchSize":50,"localTaskRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000}}
	}`)
}
