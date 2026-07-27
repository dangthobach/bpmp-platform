package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

func TestPostgresLifecycleAuditOutboxAndIdempotency(t *testing.T) {
	dsn := os.Getenv("BPMP_CONFIGURATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BPMP_CONFIGURATION_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "configuration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migration, err := os.ReadFile("../../../../../../db/configuration-service/migrations/001_configuration.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	store, _ := New(pool)
	service, _ := application.New(store, application.Config{
		ReadCapability: "configuration.read", ManageCapability: "configuration.manage",
		DefaultPageSize: 10, MaxPageSize: 100,
	})
	actor := domain.Actor{
		TenantID: "tenant-a", ActorID: "operator-a", CorrelationID: "correlation-1",
		CommandID: "command-1", IdempotencyKey: "create-1",
		Capabilities: map[string]struct{}{"configuration.read": {}, "configuration.manage": {}},
	}
	input := application.CreateInput{
		Name: "Engine policy", Owner: domain.OwnerEngine,
		Scope:         domain.Scope{Type: domain.ScopeTenant, Reference: "tenant-a"},
		SchemaVersion: 1, PolicyVersion: "policy-1", Reason: "initial policy", Values: integrationPolicy(),
	}
	created, err := service.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.Create(ctx, actor, input)
	if err != nil || duplicate.ID != created.ID {
		t.Fatalf("idempotent create failed: profile=%s err=%v", duplicate.ID, err)
	}
	input.Reason = "different request"
	if _, err = service.Create(ctx, actor, input); err != domain.ErrConflict {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	actor.CommandID, actor.IdempotencyKey = "command-2", "publish-1"
	published, err := service.Publish(
		ctx, actor, created.ID, created.Latest.ID, created.AggregateVersion, "approved",
	)
	if err != nil || published.AggregateVersion != 2 {
		t.Fatalf("publish failed: %+v %v", published, err)
	}
	resolved, err := service.Resolve(ctx, domain.ResolutionLookup{
		TenantID: "tenant-a", Owner: domain.OwnerEngine,
		WorkflowType: "approval", WorkflowVersion: "1",
		PlatformReference: "bpmp", EnvironmentReference: "test",
	})
	if err != nil || resolved.Version.ID != created.Latest.ID ||
		resolved.Profile.Scope.Type != domain.ScopeTenant {
		t.Fatalf("published configuration was not resolved: %+v %v", resolved, err)
	}
	var auditCount, outboxCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM configuration_audit WHERE tenant_id='tenant-a'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM configuration_outbox WHERE tenant_id='tenant-a'`).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 || outboxCount != 1 {
		t.Fatalf("unexpected durable side effects: audit=%d outbox=%d", auditCount, outboxCount)
	}
	if _, err = pool.Exec(ctx, `UPDATE configuration_audit SET reason='mutated'`); err == nil {
		t.Fatal("append-only audit accepted an update")
	}
}

func integrationPolicy() []byte {
	return []byte(fmt.Sprintf(`{
		"snapshotIntervalEvents":%d,"maxEventsPerDecision":64,"commandTimeoutMs":"15000",
		"optimisticConflictRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000},
		"localWasm":{"maxModuleBytes":"1048576","maxInputBytes":"262144","maxOutputBytes":"262144","maxMemoryBytes":"16777216","maxWasmStackBytes":"1048576","maxTableElements":1000,"maxInstances":4,"maxTables":4,"maxMemories":1,"fuel":"1000000"},
		"eventPayloadKeyScope":"tenant/operational","authorizationAuditKeyScope":"tenant/audit","maxMultiInstanceCardinality":1000,"defaultMultiInstanceParallelism":8,
		"boundaryRuntime":{"projectionBatchSize":100,"dispatchBatchSize":50,"maxDispatchAttempts":5,"retryDelayMs":"1000","leaseDurationMs":"30000","maxTimerHorizonMs":"31536000000","maxExpressionBytes":65536,"workerId":"worker-a","maxSignalIdBytes":256,"maxReferenceBytes":512,"maxSubscriptionsPerInstance":100}
	}`, 100))
}
