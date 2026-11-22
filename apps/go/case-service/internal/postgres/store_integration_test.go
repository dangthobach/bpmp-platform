package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateCaseIdempotencyAndTransactionalOutbox(t *testing.T) {
	dsn := os.Getenv("CASE_SERVICE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CASE_SERVICE_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "case_it_" + uuid.NewString()[:8]
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
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration path")
	}
	migration := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../db/case-service/migrations/001_case_foundation.sql"))
	contents, err := os.ReadFile(migration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(contents)); err != nil {
		t.Fatal(err)
	}
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	request := application.CreateRequest{TenantID: tenant, ActorID: "alice", IdempotencyKey: "create-1", CorrelationID: "corr-1", CustomerRef: "customer-1", WorkflowType: "approval", WorkflowVersion: "1"}
	const callers = 8
	results := make([]application.CreateResult, callers)
	errorsByCaller := make([]error, callers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errorsByCaller[i] = service.Create(ctx, request) }(i)
	}
	wg.Wait()
	var firstID uuid.UUID
	fresh := 0
	for i, result := range results {
		if errorsByCaller[i] != nil {
			t.Fatalf("caller %d: %v", i, errorsByCaller[i])
		}
		if firstID == uuid.Nil {
			firstID = result.Case.ID
		}
		if result.Case.ID != firstID || result.Case.OrchestrationStatus != "START_PENDING" || result.Case.Version != 1 {
			t.Fatalf("different Case or invalid initial state: %+v", result)
		}
		if !result.Duplicate {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("expected one fresh Case and seven replays; got %d", fresh)
	}
	var caseCount, receiptCount, eventCount, jobCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM customer_cases WHERE tenant_id=$1`, tenant).Scan(&caseCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_create_receipts WHERE tenant_id=$1`, tenant).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1 AND event_type='CaseCreated' AND published_at IS NULL`, tenant).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM workflow_start_jobs WHERE tenant_id=$1 AND case_id=$2 AND command_id=$2 AND status='PENDING'`, tenant, firstID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if caseCount != 1 || receiptCount != 1 || eventCount != 1 || jobCount != 1 {
		t.Fatalf("transactional effect counts: cases=%d receipts=%d events=%d jobs=%d", caseCount, receiptCount, eventCount, jobCount)
	}
	changed := request
	changed.CustomerRef = "customer-2"
	if _, err = service.Create(ctx, changed); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("changed intent reused key: %v", err)
	}
	if _, err = service.Get(ctx, uuid.New(), firstID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-tenant read succeeded: %v", err)
	}
	caseView, err := service.Get(ctx, tenant, firstID)
	if err != nil || caseView.CustomerRef != request.CustomerRef {
		t.Fatalf("tenant read failed: case=%+v err=%v", caseView, err)
	}
	var persistedDigestBytes int
	if err = pool.QueryRow(ctx, `SELECT octet_length(request_digest) FROM case_create_receipts WHERE tenant_id=$1`, tenant).Scan(&persistedDigestBytes); err != nil {
		t.Fatal(err)
	}
	if persistedDigestBytes != 32 {
		t.Fatal(fmt.Errorf("wrong receipt digest length: %d", persistedDigestBytes))
	}
	claimAt := time.Now().UTC().Add(time.Second)
	firstLease, found, err := store.ClaimStart(ctx, claimAt, 30*time.Second, uuid.New())
	if err != nil || !found || firstLease.CommandID != firstID || firstLease.Attempts != 1 {
		t.Fatalf("first workflow start lease: job=%+v found=%v err=%v", firstLease, found, err)
	}
	if _, found, err = store.ClaimStart(ctx, claimAt, 30*time.Second, uuid.New()); err != nil || found {
		t.Fatalf("leased job was claimed twice: found=%v err=%v", found, err)
	}
	retryAt := claimAt.Add(2 * time.Second)
	if err = store.RetryStart(ctx, firstLease, claimAt.Add(time.Second), retryAt, "ENGINE_OUTCOME_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	caseView, err = service.Get(ctx, tenant, firstID)
	if err != nil || caseView.OrchestrationStatus != "RECONCILING" || caseView.Version != 2 {
		t.Fatalf("ambiguous Engine outcome was not visible: case=%+v err=%v", caseView, err)
	}
	secondLease, found, err := store.ClaimStart(ctx, retryAt.Add(time.Second), 30*time.Second, uuid.New())
	if err != nil || !found || secondLease.CommandID != firstLease.CommandID || secondLease.CaseID != firstLease.CaseID || secondLease.Attempts != 2 {
		t.Fatalf("retry changed Engine command identity: job=%+v found=%v err=%v", secondLease, found, err)
	}
	if err = store.CompleteStart(ctx, firstLease, retryAt.Add(2*time.Second), uuid.New()); !errors.Is(err, application.ErrLeaseLost) {
		t.Fatalf("stale worker completed workflow: %v", err)
	}
	if err = store.CompleteStart(ctx, secondLease, retryAt.Add(2*time.Second), uuid.New()); err != nil {
		t.Fatal(err)
	}
	caseView, err = service.Get(ctx, tenant, firstID)
	if err != nil || caseView.OrchestrationStatus != "ACTIVE" || caseView.WorkflowInstanceID != firstID.String() || caseView.Version != 3 {
		t.Fatalf("workflow start did not reconcile: case=%+v err=%v", caseView, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1`, tenant).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 3 {
		t.Fatalf("expected CaseCreated, CaseWorkflowReconciling and CaseWorkflowStarted; got %d", eventCount)
	}
}
