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
