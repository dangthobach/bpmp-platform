package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/jackc/pgx/v5/pgxpool"
)

type explainNode struct {
	NodeType     string        `json:"Node Type"`
	RelationName string        `json:"Relation Name"`
	IndexName    string        `json:"Index Name"`
	ActualRows   float64       `json:"Actual Rows"`
	ActualLoops  float64       `json:"Actual Loops"`
	SharedHit    float64       `json:"Shared Hit Blocks"`
	SharedRead   float64       `json:"Shared Read Blocks"`
	Plans        []explainNode `json:"Plans"`
}

type explainDocument struct {
	Plan          explainNode `json:"Plan"`
	PlanningTime  float64     `json:"Planning Time"`
	ExecutionTime float64     `json:"Execution Time"`
}

func TestRepresentativeCompletedQueryPlan(t *testing.T) {
	dsn := os.Getenv("BPMP_PROJECTION_PLAN_DSN")
	if dsn == "" {
		t.Skip("BPMP_PROJECTION_PLAN_DSN is required for representative EXPLAIN ANALYZE")
	}
	processCount := planSize(t, "BPMP_PROJECTION_PLAN_PROCESSES", 100_000)
	eventsPerProcess := planSize(t, "BPMP_PROJECTION_PLAN_EVENTS_PER_PROCESS", 8)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("projection_plan_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, dropErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
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
	for _, file := range []string{"001_projection.sql", "002_completed_process_query.sql", "004_query_text_transforms.sql"} {
		data, readErr := os.ReadFile(filepath.Join("../../../../../../db/projection-service/migrations", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workflow_instance_read_models (
		tenant_id,instance_id,workflow_type,workflow_version,status,active_node_id,
		last_event_sequence,started_at,updated_at,completed_at,config_version,
		policy_version,initiator_actor_id,is_deleted,version)
		SELECT 'tenant-plan','instance-' || LPAD(n::text,8,'0'),
			CASE WHEN n % 4 = 0 THEN ' Approval ' ELSE 'Fulfilment' END,
			'7','COMPLETED','', $2,
			TIMESTAMPTZ '2026-01-01 00:00:00+00' + n * INTERVAL '1 second',
			TIMESTAMPTZ '2026-01-01 00:00:01+00' + n * INTERVAL '1 second',
			TIMESTAMPTZ '2026-01-01 00:00:01+00' + n * INTERVAL '1 second',
			'config-v1','policy-v1','actor-' || (n % 100)::text,FALSE,1
		FROM generate_series(1,$1) AS n`, processCount, eventsPerProcess); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workflow_event_read_models (
		tenant_id,instance_id,event_id,event_sequence,event_type,node_id,actor_id,
		occurred_at,config_version,policy_version,is_deleted,version)
		SELECT 'tenant-plan','instance-' || LPAD(n::text,8,'0'),
			'event-' || n::text || '-' || seq::text,seq,
			CASE WHEN seq = $3 THEN ' Workflow_Completed ' ELSE 'Node_Activated' END,
			'node-' || seq::text,'actor-' || (n % 100)::text,
			TIMESTAMPTZ '2026-01-01 00:00:00+00' + n * INTERVAL '1 second' + seq * INTERVAL '10 milliseconds',
			'config-v1','policy-v1',FALSE,1
		FROM generate_series(1,$1) AS n
		CROSS JOIN generate_series(1,$2) AS seq`, processCount, eventsPerProcess, eventsPerProcess); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "ANALYZE workflow_instance_read_models; ANALYZE workflow_event_read_models"); err != nil {
		t.Fatal(err)
	}

	filter := application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{
		{Predicate: &application.ProcessPredicate{
			Field: application.ProcessWorkflowType, Operator: application.CompareEqual,
			Values:        []application.QueryValue{{Kind: application.ValueString, String: "approval"}},
			TextTransform: application.TextTransformTrimLower,
		}},
		{EventRelation: &application.EventRelation{Quantifier: application.RelationExists, Expression: application.EventExpression{
			Predicate: &application.EventPredicate{
				Field: application.EventType, Operator: application.CompareEqual,
				Values:        []application.QueryValue{{Kind: application.ValueString, String: "workflow_completed"}},
				TextTransform: application.TextTransformTrimLower,
			},
		}}},
	}}
	statement, arguments, err := compileCompletedQuery(application.ProcessQuery{
		TenantID: "tenant-plan", Filter: &filter, Limit: 51,
		Sort: []application.ProcessSort{
			{Field: application.ProcessCompletedAt, Direction: application.SortDescending},
			{Field: application.ProcessWorkflowType, Direction: application.SortAscending, TextTransform: application.TextTransformTrimLower},
			{Field: application.ProcessInstanceID, Direction: application.SortAscending},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement, arguments...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var documents []explainDocument
	if err = json.Unmarshal(raw, &documents); err != nil || len(documents) != 1 {
		t.Fatalf("decode explain plan: %v %s", err, raw)
	}
	indexes := make([]string, 0, 4)
	seqScans := make([]string, 0, 2)
	collectPlanEvidence(documents[0].Plan, &indexes, &seqScans)
	t.Logf("representative plan: processes=%d events=%d planning_ms=%.3f execution_ms=%.3f indexes=%s",
		processCount, processCount*eventsPerProcess, documents[0].PlanningTime,
		documents[0].ExecutionTime, strings.Join(indexes, ","))
	if documents[0].Plan.ActualRows > 51 {
		t.Fatalf("query returned more than its bounded lookahead: %.0f", documents[0].Plan.ActualRows)
	}
	if len(indexes) == 0 {
		t.Fatalf("representative query used no index: %s", raw)
	}
	for _, relation := range seqScans {
		if relation == "workflow_event_read_models" {
			t.Fatalf("event relation regressed to a sequential scan: %s", raw)
		}
	}
}

func planSize(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 10_000_000 {
		t.Fatalf("%s must be between 1 and 10000000", name)
	}
	return parsed
}

func collectPlanEvidence(node explainNode, indexes, seqScans *[]string) {
	if node.IndexName != "" {
		*indexes = append(*indexes, node.IndexName)
	}
	if node.NodeType == "Seq Scan" && node.RelationName != "" {
		*seqScans = append(*seqScans, node.RelationName)
	}
	for _, child := range node.Plans {
		collectPlanEvidence(child, indexes, seqScans)
	}
}
