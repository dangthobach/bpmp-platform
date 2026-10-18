package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func (s *Store) AggregateProcesses(ctx context.Context, query application.AggregateQuery) ([]application.AggregateRow, error) {
	builder := &sqlBuilder{}
	conditions := []string{"p.tenant_id=" + builder.bind(query.TenantID)}
	switch query.Scope {
	case application.QueryScopeActive:
		conditions = append(conditions, "p.completed_at IS NULL", "NOT p.is_deleted")
	case application.QueryScopeCompleted:
		conditions = append(conditions, "p.status='COMPLETED'", "p.completed_at IS NOT NULL", "NOT p.is_deleted")
	case application.QueryScopeDeleted:
		conditions = append(conditions, "p.is_deleted")
	default:
		conditions = append(conditions, "NOT p.is_deleted")
	}
	if query.Filter != nil {
		filter, err := builder.processExpression(*query.Filter)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, filter)
	}
	selects := make([]string, 0, len(query.GroupBy)+len(query.Metrics))
	groups := make([]string, 0, len(query.GroupBy))
	for index, field := range query.GroupBy {
		expression, err := processSQL(field)
		if err != nil {
			return nil, err
		}
		selects = append(selects, expression+fmt.Sprintf(" AS d%d", index))
		groups = append(groups, expression)
	}
	for index, metric := range query.Metrics {
		expression := "*"
		if metric.Field != 0 {
			var err error
			expression, err = processSQL(metric.Field)
			if err != nil {
				return nil, err
			}
		}
		var aggregate string
		switch metric.Operation {
		case application.AggregateCount:
			aggregate = "COUNT(" + expression + ")::bigint"
		case application.AggregateSum:
			aggregate = "COALESCE(SUM(" + expression + "),0)::text"
		case application.AggregateAverage:
			aggregate = "COALESCE(AVG(" + expression + "),0)::text"
		case application.AggregateMin:
			aggregate = "MIN(" + expression + ")"
		case application.AggregateMax:
			aggregate = "MAX(" + expression + ")"
		default:
			return nil, application.ErrInvalidQuery
		}
		selects = append(selects, aggregate+fmt.Sprintf(" AS m%d", index))
	}
	limit := builder.bind(query.Limit)
	statement := "SELECT " + strings.Join(selects, ",") + " FROM workflow_instance_read_models p WHERE " + strings.Join(conditions, " AND ") +
		" GROUP BY " + strings.Join(groups, ",") + " ORDER BY " + strings.Join(groups, ",") + " LIMIT " + limit
	rows, err := s.pool.Query(ctx, statement, builder.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]application.AggregateRow, 0, query.Limit)
	for rows.Next() {
		raw := make([]any, len(query.GroupBy)+len(query.Metrics))
		targets := make([]any, len(raw))
		for index := range raw {
			targets[index] = &raw[index]
		}
		if err = rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := application.AggregateRow{}
		for index, field := range query.GroupBy {
			row.Dimensions = append(row.Dimensions, application.AggregateCell{Name: application.ProcessFieldName(field), Value: aggregateValue(raw[index], processFieldKindForAggregate(field))})
		}
		for index, metric := range query.Metrics {
			value := raw[len(query.GroupBy)+index]
			kind := processFieldKindForAggregate(metric.Field)
			if metric.Operation == application.AggregateCount {
				kind = application.ValueInteger
			} else if metric.Operation == application.AggregateSum || metric.Operation == application.AggregateAverage {
				kind = application.ValueDecimal
			}
			row.Metrics = append(row.Metrics, application.AggregateCell{Name: metric.Alias, Value: aggregateValue(value, kind)})
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func processFieldKindForAggregate(field application.ProcessField) application.ValueKind {
	switch field {
	case application.ProcessLastEventSequence, application.ProcessDurationMS, application.ProcessVersion, application.ProcessEventCount:
		return application.ValueInteger
	case application.ProcessStartedAt, application.ProcessUpdatedAt, application.ProcessCompletedAt:
		return application.ValueTimestamp
	case application.ProcessIsDeleted:
		return application.ValueBoolean
	default:
		return application.ValueString
	}
}

func aggregateValue(raw any, kind application.ValueKind) application.QueryValue {
	value := application.QueryValue{Kind: kind}
	if raw == nil {
		return application.QueryValue{}
	}
	switch kind {
	case application.ValueInteger:
		switch typed := raw.(type) {
		case int64:
			value.Integer = typed
		case int32:
			value.Integer = int64(typed)
		case string:
			value.Integer, _ = strconv.ParseInt(typed, 10, 64)
		}
	case application.ValueBoolean:
		value.Boolean, _ = raw.(bool)
	case application.ValueTimestamp:
		value.Time, _ = raw.(time.Time)
	case application.ValueDecimal:
		value.Decimal = fmt.Sprint(raw)
	default:
		value.String = fmt.Sprint(raw)
	}
	return value
}

var _ application.AggregateStore = (*Store)(nil)
