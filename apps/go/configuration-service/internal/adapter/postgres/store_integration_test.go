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
	for _, path := range []string{
		"../../../../../../db/configuration-service/migrations/001_configuration.sql",
		"../../../../../../db/configuration-service/migrations/002_kafka_hot_reload.sql",
		"../../../../../../db/configuration-service/migrations/003_owner_and_tenant_readiness.sql",
		"../../../../../../db/configuration-service/migrations/004_audit_effective_versions.sql",
		"../../../../../../db/configuration-service/migrations/005_outbox_observability_context.sql",
	} {
		migration, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := New(pool)
	service, _ := application.New(store, application.Config{
		ReadCapability: "configuration.read", ManageCapability: "configuration.manage",
		DefaultPageSize: 10, MaxPageSize: 100,
	})
	actor := domain.Actor{
		TenantID: "tenant-a", ActorID: "operator-a", CorrelationID: "correlation-1",
		RequestID: "request-1", CommandID: "command-1", IdempotencyKey: "create-1",
		TraceParent:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceState:   "bpmp=integration-test",
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
	actor.CommandID, actor.IdempotencyKey = "command-3", "retire-1"
	retired, err := service.Retire(
		ctx, actor, created.ID, published.AggregateVersion, "superseded",
	)
	if err != nil || retired.AggregateVersion != 3 {
		t.Fatalf("retire failed: %+v %v", retired, err)
	}
	if _, err = service.Resolve(ctx, domain.ResolutionLookup{
		TenantID: "tenant-a", Owner: domain.OwnerEngine,
		WorkflowType: "approval", WorkflowVersion: "1",
		PlatformReference: "bpmp", EnvironmentReference: "test",
	}); err != domain.ErrNotFound {
		t.Fatalf("retired configuration remained effective: %v", err)
	}
	actor.CommandID, actor.IdempotencyKey = "command-4", "restore-1"
	restored, err := service.Restore(
		ctx, actor, created.ID, created.Latest.ID, retired.AggregateVersion,
		"policy-2", "restore known good policy",
	)
	if err != nil || restored.AggregateVersion != 4 {
		t.Fatalf("restore failed: %+v %v", restored, err)
	}
	var auditCount, outboxCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM configuration_audit WHERE tenant_id='tenant-a'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM configuration_outbox WHERE tenant_id='tenant-a'`).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 || outboxCount != 3 {
		t.Fatalf("unexpected durable side effects: audit=%d outbox=%d", auditCount, outboxCount)
	}
	var requestID, correlationID, commandID, traceParent, traceState string
	if err = pool.QueryRow(ctx, `SELECT request_id,correlation_id,command_id,trace_parent,trace_state
		FROM configuration_outbox WHERE tenant_id='tenant-a'
		ORDER BY created_at LIMIT 1`).Scan(&requestID, &correlationID, &commandID, &traceParent, &traceState); err != nil {
		t.Fatal(err)
	}
	if requestID != actor.RequestID || correlationID != actor.CorrelationID ||
		commandID != "command-2" || traceParent != actor.TraceParent || traceState != actor.TraceState {
		t.Fatalf("outbox lost observability context: request=%q correlation=%q command=%q traceparent=%q tracestate=%q",
			requestID, correlationID, commandID, traceParent, traceState)
	}
	var auditedConfigVersion, auditedPolicyVersion string
	if err = pool.QueryRow(ctx, `SELECT config_version,policy_version
		FROM configuration_audit WHERE tenant_id='tenant-a'
		ORDER BY occurred_at DESC LIMIT 1`).Scan(&auditedConfigVersion, &auditedPolicyVersion); err != nil {
		t.Fatal(err)
	}
	if auditedConfigVersion == "" || auditedPolicyVersion == "" {
		t.Fatal("effective configuration versions were not recorded in audit")
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
		"boundaryRuntime":{"projectionBatchSize":100,"dispatchBatchSize":50,"maxDispatchAttempts":5,"retryDelayMs":"1000","leaseDurationMs":"30000","maxTimerHorizonMs":"31536000000","maxExpressionBytes":65536,"workerId":"worker-a","maxSignalIdBytes":256,"maxReferenceBytes":512,"maxSubscriptionsPerInstance":100},
		"workers":{"pollIntervalMs":"100","outboxBatchSize":100,"outboxRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000},"localTaskBatchSize":50,"localTaskRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000}}
	}`, 100))
}
