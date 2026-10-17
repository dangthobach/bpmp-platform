package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func TestCompileCompletedQueryParameterizesNestedRelationAndSort(t *testing.T) {
	attack := `x%' OR TRUE; DROP TABLE workflow_instance_read_models; --`
	filter := application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{
		{Predicate: &application.ProcessPredicate{
			Field: application.ProcessWorkflowType, Operator: application.CompareContains,
			Values: []application.QueryValue{{Kind: application.ValueString, String: attack}},
		}},
		{EventRelation: &application.EventRelation{Quantifier: application.RelationExists, Expression: application.EventExpression{
			Predicate: &application.EventPredicate{
				Field: application.EventNodeID, Operator: application.CompareIn,
				Values: []application.QueryValue{{Kind: application.ValueString, String: "approve"}, {Kind: application.ValueString, String: "review"}},
			},
		}}},
	}}
	statement, arguments, err := compileCompletedQuery(application.ProcessQuery{
		TenantID: "tenant-a", Filter: &filter, Limit: 51,
		Sort: []application.ProcessSort{
			{Field: application.ProcessCompletedAt, Direction: application.SortDescending},
			{Field: application.ProcessInstanceID, Direction: application.SortAscending},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(statement, attack) || !strings.Contains(statement, "EXISTS (SELECT 1") ||
		!strings.Contains(statement, "p.tenant_id=$1") ||
		!strings.Contains(statement, "ORDER BY p.completed_at DESC,p.instance_id ASC") {
		t.Fatalf("unsafe or incomplete SQL: %s", statement)
	}
	if len(arguments) != 4 || arguments[0] != "tenant-a" {
		t.Fatalf("unexpected bound arguments: %#v", arguments)
	}
	if value, ok := arguments[1].(string); !ok || !strings.Contains(value, `\%`) {
		t.Fatalf("LIKE wildcard was not escaped: %#v", arguments[1])
	}
}

func TestCompileCompletedQueryBuildsMixedDirectionKeyset(t *testing.T) {
	completed := time.UnixMilli(2000).UTC()
	statement, _, err := compileCompletedQuery(application.ProcessQuery{
		TenantID: "tenant-a", Limit: 10,
		Sort: []application.ProcessSort{
			{Field: application.ProcessCompletedAt, Direction: application.SortDescending},
			{Field: application.ProcessInstanceID, Direction: application.SortAscending},
		},
		After: &application.QueryCursor{Values: []application.QueryValue{
			{Kind: application.ValueTimestamp, Time: completed},
			{Kind: application.ValueString, String: "instance-9"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, "p.completed_at<") ||
		!strings.Contains(statement, "p.completed_at=") ||
		!strings.Contains(statement, "p.instance_id>") {
		t.Fatalf("mixed-direction keyset is incomplete: %s", statement)
	}
}

func TestCompileCompletedQueryUsesIdenticalTransformsForFilterSortAndCursor(t *testing.T) {
	fallback := application.QueryValue{Kind: application.ValueString, String: "unknown"}
	filter := application.ProcessExpression{Predicate: &application.ProcessPredicate{
		Field: application.ProcessWorkflowType, Operator: application.CompareEqual,
		Values:        []application.QueryValue{{Kind: application.ValueString, String: "approval"}},
		TextTransform: application.TextTransformTrimLower,
		Coalesce:      &fallback,
	}}
	statement, arguments, err := compileCompletedQuery(application.ProcessQuery{
		TenantID: "tenant-a", Filter: &filter, Limit: 25,
		Sort: []application.ProcessSort{
			{Field: application.ProcessWorkflowType, Direction: application.SortAscending, TextTransform: application.TextTransformTrimLower, Coalesce: &fallback},
			{Field: application.ProcessCompletedAt, Direction: application.SortDescending},
			{Field: application.ProcessInstanceID, Direction: application.SortAscending},
		},
		After: &application.QueryCursor{Values: []application.QueryValue{
			{Kind: application.ValueString, String: "approval"},
			{Kind: application.ValueTimestamp, Time: time.UnixMilli(2_000).UTC()},
			{Kind: application.ValueString, String: "i9"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expression := "LOWER(BTRIM(COALESCE(p.workflow_type,"
	if strings.Count(statement, expression) < 4 ||
		!strings.Contains(statement, "ORDER BY LOWER(BTRIM(COALESCE(p.workflow_type,") ||
		!strings.Contains(statement, "p.completed_at DESC,p.instance_id ASC") {
		t.Fatalf("filter/order/cursor transforms diverged: %s", statement)
	}
	for _, argument := range arguments {
		if text, ok := argument.(string); ok && strings.Contains(text, "DROP TABLE") {
			t.Fatalf("unexpected SQL text in arguments: %#v", arguments)
		}
	}
}

func TestCompileCompletedQuerySupportsTypedRangesAndSuffixMatching(t *testing.T) {
	filter := application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{
		{Predicate: &application.ProcessPredicate{
			Field: application.ProcessDurationMS, Operator: application.CompareBetween,
			Values:           []application.QueryValue{{Kind: application.ValueInteger, Integer: 10}, {Kind: application.ValueInteger, Integer: 20}},
			NumericTransform: application.NumericTransformAbs,
		}},
		{Predicate: &application.ProcessPredicate{
			Field: application.ProcessCompletedAt, Operator: application.CompareNotBetween,
			Values:        []application.QueryValue{{Kind: application.ValueTimestamp, Time: time.UnixMilli(1_000)}, {Kind: application.ValueTimestamp, Time: time.UnixMilli(2_000)}},
			DateTransform: application.DateTransformUTCMonth,
		}},
		{Predicate: &application.ProcessPredicate{
			Field: application.ProcessWorkflowType, Operator: application.CompareEndsWith,
			Values: []application.QueryValue{{Kind: application.ValueString, String: `%_done`}},
		}},
	}}
	statement, arguments, err := compileCompletedQuery(application.ProcessQuery{
		TenantID: "tenant-a", Filter: &filter, Limit: 10,
		Sort: []application.ProcessSort{{Field: application.ProcessInstanceID, Direction: application.SortAscending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, "ABS(COALESCE(GREATEST(") || !strings.Contains(statement, " BETWEEN ") ||
		!strings.Contains(statement, " NOT BETWEEN ") || !strings.Contains(statement, "DATE_TRUNC('month',") ||
		!strings.Contains(statement, " LIKE ") {
		t.Fatalf("typed transform, range, or suffix SQL is absent: %s", statement)
	}
	last, ok := arguments[len(arguments)-2].(string)
	if !ok || last != `%\%\_done` {
		t.Fatalf("suffix wildcard was not escaped: %#v", arguments)
	}
}
