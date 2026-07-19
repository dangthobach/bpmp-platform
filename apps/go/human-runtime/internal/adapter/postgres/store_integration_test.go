package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

func TestPostgresProjectionAuditLockingAndLeaseRecovery(t *testing.T) {
	dsn := os.Getenv("HUMAN_RUNTIME_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("HUMAN_RUNTIME_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "human_test_" + time.Now().UTC().Format("20060102150405000000")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
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
	migration, err := os.ReadFile(findMigration(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0).UTC()
	_, err = pool.Exec(ctx, `INSERT INTO assignment_policies(tenant_id,policy_ref,workflow_type,workflow_version,node_id,assignee_id,sla_duration_ms,escalation_policy_ref,config_version,created_at,created_by,updated_at,updated_by) VALUES('tenant-a','reviewers','approval','1','review','alice',1000,'manager','cfg-1',$1,'admin',$1,'admin')`, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(pool)
	activation := domain.Activation{TenantID: "tenant-a", EventID: "event-1", Sequence: 3, InstanceID: "instance-1", WorkflowType: "approval", WorkflowVersion: "1", NodeID: "review", TaskType: "review", AssignmentPolicyRef: "reviewers", OccurredAt: now}
	item, duplicate, err := store.ProjectActivation(ctx, activation)
	if err != nil || duplicate {
		t.Fatalf("activation failed: duplicate=%v err=%v", duplicate, err)
	}
	_, duplicate, err = store.ProjectActivation(ctx, activation)
	if err != nil || !duplicate {
		t.Fatalf("dedup failed: duplicate=%v err=%v", duplicate, err)
	}
	delegated, err := domain.Delegate(item, "alice", domain.Assignment{AssigneeID: "bob"}, now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Delegate(ctx, delegated, "delegate-1", "correlation-1", "alice"); err != nil {
		t.Fatal(err)
	}
	if err = store.Delegate(ctx, delegated, "delegate-2", "correlation-2", "alice"); !errors.Is(err, application.ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE human_audit_log SET actor_id='tampered' WHERE tenant_id='tenant-a'`); err == nil {
		t.Fatal("immutable audit update succeeded")
	}
	claims, err := store.ClaimDueEscalations(ctx, now.Add(2*time.Second), now.Add(time.Minute), "worker-1", 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim failed: %d %v", len(claims), err)
	}
	claims2, err := store.ClaimDueEscalations(ctx, now.Add(3*time.Second), now.Add(time.Minute), "worker-2", 10)
	if err != nil || len(claims2) != 0 {
		t.Fatalf("lease isolation failed: %d %v", len(claims2), err)
	}
	var escalationAudits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM human_audit_log WHERE tenant_id='tenant-a' AND action='SLA_ESCALATION_DUE'`).Scan(&escalationAudits); err != nil || escalationAudits != 1 {
		t.Fatalf("escalation audit missing: count=%d err=%v", escalationAudits, err)
	}
	current, err := store.GetWorkItem(ctx, "tenant-a", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := domain.RequestCompletion(current, "bob", "approved", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	pending.CompletionCommandID = "complete-command-1"
	if err = store.RequestCompletion(ctx, pending, "complete-command-1", "correlation-2", "bob"); err != nil {
		t.Fatal(err)
	}
	completion := application.CommittedCompletion{TenantID: "tenant-a", EventID: "event-2", Sequence: 4, InstanceID: "instance-1", NodeID: "review", Decision: "approved", OccurredAt: now.Add(5 * time.Second)}
	if err = store.CommitCompletion(ctx, completion); err != nil {
		t.Fatal(err)
	}
	if err = store.CommitCompletion(ctx, completion); err != nil {
		t.Fatalf("duplicate committed event was not idempotent: %v", err)
	}
	completed, err := store.GetWorkItem(ctx, "tenant-a", item.ID)
	if err != nil || completed.Status != domain.WorkItemCompleted || completed.Decision != "approved" {
		t.Fatalf("committed completion was not projected: %#v err=%v", completed, err)
	}
	conflictingSequence := completion
	conflictingSequence.EventID = "event-3"
	if err = store.CommitCompletion(ctx, conflictingSequence); err == nil {
		t.Fatal("different event reused a committed stream sequence")
	}
	missing := application.CommittedCompletion{TenantID: "tenant-a", EventID: "event-missing", Sequence: 10, InstanceID: "missing", NodeID: "review", OccurredAt: now}
	if err = store.CommitCompletion(ctx, missing); !errors.Is(err, application.ErrProjectionDependency) {
		t.Fatalf("expected missing projection dependency, got %v", err)
	}
	var missingCheckpoint int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM human_event_inbox WHERE tenant_id='tenant-a' AND event_id='event-missing'`).Scan(&missingCheckpoint); err != nil || missingCheckpoint != 0 {
		t.Fatalf("failed projection checkpoint was committed: count=%d err=%v", missingCheckpoint, err)
	}
	assertNormalLoadP95(t, store)
}

func assertNormalLoadP95(t *testing.T, store *Store) {
	t.Helper()
	const workers, operationsPerWorker = 8, 25
	latencies := make(chan time.Duration, workers*operationsPerWorker)
	errorsSeen := make(chan error, workers*operationsPerWorker)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range operationsPerWorker {
				started := time.Now()
				_, err := store.GetWorkItem(context.Background(), "tenant-a", "event-1")
				latencies <- time.Since(started)
				if err != nil {
					errorsSeen <- err
				}
			}
		}()
	}
	wait.Wait()
	close(latencies)
	close(errorsSeen)
	if err, ok := <-errorsSeen; ok {
		t.Fatal(err)
	}
	values := make([]time.Duration, 0, workers*operationsPerWorker)
	for latency := range latencies {
		values = append(values, latency)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	p95 := values[(len(values)*95+99)/100-1]
	t.Logf("normal-load operations=%d concurrency=%d p95=%s", len(values), workers, p95)
	if p95 > 500*time.Millisecond {
		t.Fatalf("normal-load P95 %s exceeds 500ms", p95)
	}
}

func findMigration(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "db", "human-runtime", "migrations", "001_human_runtime.sql")
		if _, err = os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
