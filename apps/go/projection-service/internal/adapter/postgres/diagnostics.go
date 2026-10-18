package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func (s *Store) ExplainQueryPlan(ctx context.Context, query application.ProcessQuery, analyze bool) (string, error) {
	statement, args, err := compileCompletedQuery(query)
	if err != nil {
		return "", err
	}
	prefix := "EXPLAIN (FORMAT JSON) "
	if analyze {
		prefix = "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "
	}
	var raw []byte
	if err = s.pool.QueryRow(ctx, prefix+statement, args...).Scan(&raw); err != nil {
		return "", err
	}
	var document []map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		return "", err
	}
	if len(document) != 1 {
		return "", errors.New("unexpected PostgreSQL plan document")
	}
	safe := sanitizePlanDocument(document[0])
	encoded, err := json.Marshal(safe)
	return string(encoded), err
}
func sanitizePlanDocument(document map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"Planning Time", "Execution Time"} {
		if value, ok := document[key]; ok {
			result[key] = value
		}
	}
	if plan, ok := document["Plan"].(map[string]any); ok {
		result["Plan"] = sanitizePlanNode(plan)
	}
	return result
}
func sanitizePlanNode(node map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"Node Type", "Join Type", "Startup Cost", "Total Cost", "Plan Rows", "Plan Width", "Actual Startup Time", "Actual Total Time", "Actual Rows", "Actual Loops", "Shared Hit Blocks", "Shared Read Blocks", "Temp Read Blocks", "Temp Written Blocks"} {
		if value, ok := node[key]; ok {
			result[key] = value
		}
	}
	if children, ok := node["Plans"].([]any); ok {
		safe := make([]map[string]any, 0, len(children))
		for _, child := range children {
			if typed, valid := child.(map[string]any); valid {
				safe = append(safe, sanitizePlanNode(typed))
			}
		}
		result["Plans"] = safe
	}
	return result
}

var _ application.QueryPlanStore = (*Store)(nil)
