package postgres

import (
	"context"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func (s *Store) QueryEvents(ctx context.Context, query application.EventQuery) ([]application.WorkflowEvent, error) {
	b := &sqlBuilder{}
	conditions := []string{"e.tenant_id=" + b.bind(query.TenantID), "NOT e.is_deleted"}
	if query.Filter != nil {
		sql, err := b.eventExpression("e", *query.Filter)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, sql)
	}
	if query.ProcessFilter != nil {
		sql, err := b.processExpression(*query.ProcessFilter)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, "EXISTS (SELECT 1 FROM workflow_instance_read_models p WHERE p.tenant_id=e.tenant_id AND p.instance_id=e.instance_id AND NOT p.is_deleted AND "+sql+")")
	}
	orders := make([]string, 0, len(query.Sort)+1)
	for _, sort := range query.Sort {
		field, err := eventSQL("e", sort.Field)
		if err != nil {
			return nil, err
		}
		direction := "ASC"
		if sort.Direction == application.SortDescending {
			direction = "DESC"
		}
		orders = append(orders, field+" "+direction)
	}
	orders = append(orders, "e.instance_id ASC")
	if query.After != nil {
		branches := make([]string, 0, len(query.Sort)+1)
		fields := make([]string, 0, len(query.Sort)+1)
		dirs := make([]application.SortDirection, 0, len(query.Sort)+1)
		for _, sort := range query.Sort {
			field, _ := eventSQL("e", sort.Field)
			fields = append(fields, field)
			dirs = append(dirs, sort.Direction)
		}
		fields = append(fields, "e.instance_id")
		dirs = append(dirs, application.SortAscending)
		for i := range fields {
			parts := make([]string, 0, i+1)
			for j := 0; j < i; j++ {
				parts = append(parts, fields[j]+"="+b.bind(queryScalar(query.After.Values[j])))
			}
			op := ">"
			if dirs[i] == application.SortDescending {
				op = "<"
			}
			parts = append(parts, fields[i]+op+b.bind(queryScalar(query.After.Values[i])))
			branches = append(branches, "("+strings.Join(parts, " AND ")+")")
		}
		conditions = append(conditions, "("+strings.Join(branches, " OR ")+")")
	}
	statement := `SELECT e.instance_id,e.event_sequence,e.event_type,e.node_id,e.actor_id,e.occurred_at,e.config_version,e.policy_version,e.is_deleted FROM workflow_event_read_models e WHERE ` + strings.Join(conditions, " AND ") + " ORDER BY " + strings.Join(orders, ",") + " LIMIT " + b.bind(query.Limit)
	rows, err := s.pool.Query(ctx, statement, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]application.WorkflowEvent, 0, query.Limit)
	for rows.Next() {
		var event application.WorkflowEvent
		if err = rows.Scan(&event.InstanceID, &event.Sequence, &event.EventType, &event.NodeID, &event.ActorID, &event.OccurredAt, &event.ConfigVersion, &event.PolicyVersion, &event.IsDeleted); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

var _ application.EventQueryStore = (*Store)(nil)
