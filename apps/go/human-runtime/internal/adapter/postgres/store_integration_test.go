package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	activation := domain.Activation{TenantID: "tenant-a", EventID: "event-1", InstanceID: "instance-1", WorkflowType: "approval", WorkflowVersion: "1", NodeID: "review", TaskType: "review", AssignmentPolicyRef: "reviewers", OccurredAt: now}
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
