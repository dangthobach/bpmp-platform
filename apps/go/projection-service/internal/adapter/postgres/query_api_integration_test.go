package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresExtendedQueryAPIs(t *testing.T) {
	dsn := os.Getenv("BPMP_PROJECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("BPMP_PROJECTION_TEST_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("projection_api_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelCleanup()
		if _, dropErr := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
			t.Error(dropErr)
		}
	}()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrations := []string{"001_projection.sql", "002_completed_process_query.sql", "003_query_revoke_kafka.sql", "004_query_text_transforms.sql", "005_query_api.sql", "006_query_storage_hardening.sql"}
	for pass := 1; pass <= 2; pass++ {
		for _, file := range migrations {
			data, readErr := os.ReadFile(filepath.Join("../../../../../../db/projection-service/migrations", file))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, err = pool.Exec(ctx, string(data)); err != nil {
				t.Fatalf("apply %s pass %d: %v", file, pass, err)
			}
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workflow_instance_read_models(tenant_id,instance_id,workflow_type,workflow_version,status,last_event_sequence,started_at,updated_at,completed_at,config_version,policy_version,initiator_actor_id,is_deleted,version) VALUES
	('tenant-a','i1',' Approval ','1','COMPLETED',2,now()-interval '2 seconds',now()-interval '1 second',now()-interval '1 second','c1','p1','alice',false,1),
	('tenant-a','i2','Approval','1','COMPLETED',2,now()-interval '3 seconds',now()-interval '1 second',now()-interval '1 second','c1','p1','bob',false,1),
		('tenant-a','i4','Approval','1','ACTIVE',1,now()-interval '4 seconds',now(),NULL,'c1','p1','alice',false,1),
		('tenant-a','i5','Approval','1','COMPLETED',1,now()-interval '5 seconds',now()-interval '4 seconds',now()-interval '4 seconds','c1','p1','alice',true,1),
	('tenant-b','i3','Approval','1','COMPLETED',1,now()-interval '1 second',now(),now(),'c1','p1','mallory',false,1);
	INSERT INTO workflow_event_read_models(tenant_id,instance_id,event_id,event_sequence,event_type,node_id,actor_id,occurred_at,config_version,policy_version,is_deleted,version) VALUES
	('tenant-a','i1','e11',1,'started','n1','alice',now()-interval '2 seconds','c1','p1',false,1),
	('tenant-a','i1','e12',2,'completed','n2','alice',now()-interval '1 second','c1','p1',false,1),
	('tenant-a','i2','e21',1,'started','n1','bob',now()-interval '3 seconds','c1','p1',false,1),
	('tenant-a','i2','e22',2,'completed','n2','bob',now()-interval '1 second','c1','p1',false,1),
		('tenant-a','i4','e41',1,'started','n1','alice',now()-interval '4 seconds','c1','p1',false,1),
		('tenant-a','i5','e51',1,'completed','n2','alice',now()-interval '4 seconds','c1','p1',false,1),
	('tenant-b','i3','e31',1,'completed','n2','mallory',now(),'c1','p1',false,1)`); err != nil {
		t.Fatal(err)
	}
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("aggregate tenant isolation and decimal", func(t *testing.T) {
		rows, queryErr := store.AggregateProcesses(ctx, application.AggregateQuery{TenantID: "tenant-a", Scope: application.QueryScopeCompleted, GroupBy: []application.ProcessField{application.ProcessWorkflowVersion}, Metrics: []application.AggregateMetric{{Alias: "total", Operation: application.AggregateCount}, {Alias: "average_events", Operation: application.AggregateAverage, Field: application.ProcessEventCount}}, Limit: 10})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(rows) != 1 || rows[0].Metrics[0].Value.Integer != 2 || rows[0].Metrics[1].Value.Decimal != "2.0000000000000000" {
			t.Fatalf("unexpected aggregate: %#v", rows)
		}
	})
	t.Run("event query process filter", func(t *testing.T) {
		events, queryErr := store.QueryEvents(ctx, application.EventQuery{TenantID: "tenant-a", Filter: &application.EventExpression{Predicate: &application.EventPredicate{Field: application.EventType, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: "completed"}}}}, ProcessFilter: &application.ProcessExpression{Predicate: &application.ProcessPredicate{Field: application.ProcessInitiatorActorID, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: "alice"}}}}, Sort: []application.EventSort{{Field: application.EventSequence, Direction: application.SortDescending}}, Limit: 10})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(events) != 1 || events[0].InstanceID != "i1" {
			t.Fatalf("unexpected events: %#v", events)
		}
	})
	t.Run("dsl transforms range collapse coalesce and expansion", func(t *testing.T) {
		now := time.Now().UTC()
		from := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
		to := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		rows, queryErr := store.QueryInstances(ctx, application.ProcessQuery{TenantID: "tenant-a", Scope: application.QueryScopeCompleted, Filter: &application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{
			{Predicate: &application.ProcessPredicate{Field: application.ProcessWorkflowType, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: "APPROVAL"}}, TextTransform: application.TextTransformTrimUpper}},
			{Predicate: &application.ProcessPredicate{Field: application.ProcessStartedAt, Operator: application.CompareBetween, Values: []application.QueryValue{{Kind: application.ValueTimestamp, Time: from}, {Kind: application.ValueTimestamp, Time: to}}, DateTransform: application.DateTransformUTCDay}},
		}}, Sort: []application.ProcessSort{{Field: application.ProcessWorkflowType, Direction: application.SortAscending, TextTransform: application.TextTransformTrimLower}, {Field: application.ProcessInstanceID, Direction: application.SortAscending}}, Collapse: application.ProcessWorkflowType, ExpandEvents: &application.EventExpansion{Limit: 1}, Limit: 10})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(rows) != 1 || len(rows[0].Events) != 1 {
			t.Fatalf("collapse or expansion failed: %#v", rows)
		}
		fallback := application.QueryValue{Kind: application.ValueTimestamp, Time: time.Unix(0, 0).UTC()}
		active, queryErr := store.QueryInstances(ctx, application.ProcessQuery{TenantID: "tenant-a", Scope: application.QueryScopeAll, Sort: []application.ProcessSort{{Field: application.ProcessCompletedAt, Direction: application.SortAscending, Coalesce: &fallback}, {Field: application.ProcessInstanceID, Direction: application.SortAscending}}, Limit: 10})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(active) != 3 || active[0].Instance.InstanceID != "i4" {
			t.Fatalf("coalesce/order/scope failed: %#v", active)
		}
	})
	t.Run("event keyset pagination is stable", func(t *testing.T) {
		query := application.EventQuery{TenantID: "tenant-a", Sort: []application.EventSort{{Field: application.EventSequence, Direction: application.SortDescending}}, Limit: 2}
		first, queryErr := store.QueryEvents(ctx, query)
		if queryErr != nil || len(first) != 2 {
			t.Fatalf("first page: %#v %v", first, queryErr)
		}
		cursor, cursorErr := application.EventCursorFor(query, first[1])
		if cursorErr != nil {
			t.Fatal(cursorErr)
		}
		query.After = cursor
		second, queryErr := store.QueryEvents(ctx, query)
		if queryErr != nil || len(second) != 2 {
			t.Fatalf("second page: %#v %v", second, queryErr)
		}
		seen := map[string]bool{}
		for _, event := range first {
			seen[fmt.Sprintf("%s/%d", event.InstanceID, event.Sequence)] = true
		}
		for _, event := range second {
			if seen[fmt.Sprintf("%s/%d", event.InstanceID, event.Sequence)] {
				t.Fatalf("event repeated across pages: %#v", event)
			}
		}
	})
	t.Run("saved query optimistic version", func(t *testing.T) {
		item := application.SavedQuery{TenantID: "tenant-a", ID: "mine", Name: "Mine", OwnerSubject: "alice", Query: application.ProcessQuery{TenantID: "tenant-a", Scope: application.QueryScopeAll}}
		created, saveErr := store.SaveQuery(ctx, item, 0)
		if saveErr != nil || created.Version != 1 {
			t.Fatalf("create: %#v %v", created, saveErr)
		}
		item.Name = "Updated"
		updated, saveErr := store.SaveQuery(ctx, item, 1)
		if saveErr != nil || updated.Version != 2 {
			t.Fatalf("update: %#v %v", updated, saveErr)
		}
		if _, saveErr = store.SaveQuery(ctx, item, 1); saveErr != application.ErrConflict {
			t.Fatalf("stale update accepted: %v", saveErr)
		}
		listed, listErr := store.ListSavedQueries(ctx, "tenant-a", "alice", time.Time{}, "", 10)
		if listErr != nil || len(listed) != 1 {
			t.Fatalf("list: %#v %v", listed, listErr)
		}
	})
	t.Run("durable export lifecycle", func(t *testing.T) {
		item, createErr := store.CreateQueryExport(ctx, application.QueryExport{TenantID: "tenant-a", ID: "export-1", OwnerSubject: "alice", Status: application.QueryExportPending, Query: application.ProcessQuery{TenantID: "tenant-a", Scope: application.QueryScopeAll}, MaxRows: 10, ExpiresAt: time.Now().Add(time.Hour)})
		if createErr != nil {
			t.Fatal(createErr)
		}
		claimed, claimErr := store.ClaimQueryExport(ctx)
		if claimErr != nil || claimed.ID != item.ID {
			t.Fatalf("claim: %#v %v", claimed, claimErr)
		}
		if completeErr := store.CompleteQueryExport(ctx, "tenant-a", item.ID, []byte("a,b\n1,2\n"), 1, ""); completeErr != nil {
			t.Fatal(completeErr)
		}
		got, getErr := store.GetQueryExport(ctx, "tenant-a", item.ID, "alice")
		if getErr != nil || got.Status != application.QueryExportCompleted || got.RowCount != 1 {
			t.Fatalf("get: %#v %v", got, getErr)
		}
	})
	t.Run("redacted explain", func(t *testing.T) {
		plan, planErr := store.ExplainQueryPlan(ctx, application.ProcessQuery{TenantID: "tenant-a", Scope: application.QueryScopeCompleted, Sort: []application.ProcessSort{{Field: application.ProcessCompletedAt, Direction: application.SortDescending}, {Field: application.ProcessInstanceID, Direction: application.SortAscending}}, Limit: 10}, true)
		if planErr != nil {
			t.Fatal(planErr)
		}
		for _, secret := range []string{"workflow_instance_read_models", "tenant-a", "idx_"} {
			if strings.Contains(plan, secret) {
				t.Fatalf("plan leaked %q: %s", secret, plan)
			}
		}
		if !strings.Contains(plan, "Execution Time") {
			t.Fatalf("analyze metrics absent: %s", plan)
		}
	})
}
