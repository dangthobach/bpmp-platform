package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

const durationExpression = `COALESCE(GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (p.completed_at-p.started_at))*1000)),0)::bigint`
const eventCountExpression = `(SELECT COUNT(*) FROM workflow_event_read_models ec WHERE ec.tenant_id=p.tenant_id AND ec.instance_id=p.instance_id AND NOT ec.is_deleted)::bigint`

type sqlBuilder struct {
	args          []any
	relationIndex uint32
}

func (s *Store) QueryCompleted(
	ctx context.Context,
	query application.ProcessQuery,
) ([]application.CompletedProcess, error) {
	query.Scope = application.QueryScopeCompleted
	return s.queryProcesses(ctx, query)
}

func (s *Store) QueryInstances(ctx context.Context, query application.ProcessQuery) ([]application.CompletedProcess, error) {
	return s.queryProcesses(ctx, query)
}

func (s *Store) queryProcesses(ctx context.Context, query application.ProcessQuery) ([]application.CompletedProcess, error) {
	statement, arguments, err := compileCompletedQuery(query)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	processes := make([]application.CompletedProcess, 0, query.Limit)
	for rows.Next() {
		var process application.CompletedProcess
		var duration, eventCount int64
		err = rows.Scan(
			&process.Instance.TenantID, &process.Instance.InstanceID,
			&process.Instance.WorkflowType, &process.Instance.WorkflowVersion,
			&process.Instance.Status, &process.Instance.ActiveNodeID,
			&process.Instance.LastEventSequence, &process.Instance.StartedAt,
			&process.Instance.UpdatedAt, &process.Instance.CompletedAt,
			&process.Instance.ConfigVersion, &process.Instance.PolicyVersion,
			&process.Instance.InitiatorActorID, &process.Instance.IsDeleted,
			&process.Instance.Version,
			&duration, &eventCount,
		)
		if err != nil {
			return nil, err
		}
		if duration < 0 || eventCount < 0 {
			return nil, errors.New("completed process aggregate is invalid")
		}
		process.DurationMS = uint64(duration)
		process.EventCount = uint64(eventCount)
		processes = append(processes, process)
	}
	return processes, rows.Err()
}

func compileCompletedQuery(query application.ProcessQuery) (string, []any, error) {
	builder := &sqlBuilder{}
	tenant := builder.bind(query.TenantID)
	conditions := []string{
		"p.tenant_id=" + tenant,
	}
	switch query.Scope {
	case application.QueryScopeCompleted:
		conditions = append(conditions, "p.status='COMPLETED'", "p.completed_at IS NOT NULL")
	case application.QueryScopeActive:
		conditions = append(conditions, "p.completed_at IS NULL")
	case application.QueryScopeDeleted:
		conditions = append(conditions, "p.is_deleted")
	}
	if query.Scope != application.QueryScopeDeleted && !query.IncludeDeleted {
		conditions = append(conditions, "NOT p.is_deleted")
	}
	if query.Filter != nil {
		filter, err := builder.processExpression(*query.Filter)
		if err != nil {
			return "", nil, err
		}
		conditions = append(conditions, filter)
	}
	if query.After != nil {
		cursor, err := builder.cursorCondition(query.Sort, query.After.Values)
		if err != nil {
			return "", nil, err
		}
		conditions = append(conditions, cursor)
	}
	order := make([]string, 0, len(query.Sort))
	for _, sort := range query.Sort {
		field, err := builder.processSortSQL(sort)
		if err != nil {
			return "", nil, err
		}
		direction := "ASC"
		if sort.Direction == application.SortDescending {
			direction = "DESC"
		}
		order = append(order, field+" "+direction)
	}
	limit := builder.bind(query.Limit)
	statement := `SELECT p.tenant_id,p.instance_id,p.workflow_type,p.workflow_version,p.status,
		p.active_node_id,p.last_event_sequence,p.started_at,p.updated_at,p.completed_at,
		p.config_version,p.policy_version,p.initiator_actor_id,p.is_deleted,p.version,` + durationExpression + `,
		` + eventCountExpression + `
		FROM workflow_instance_read_models p
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY ` + strings.Join(order, ",") + `
		LIMIT ` + limit
	return statement, builder.args, nil
}

func (b *sqlBuilder) processExpression(expression application.ProcessExpression) (string, error) {
	switch {
	case expression.Predicate != nil:
		field, err := processSQL(expression.Predicate.Field)
		if err != nil {
			return "", err
		}
		field = b.fieldExpression(field, expression.Predicate.TextTransform, expression.Predicate.NumericTransform, expression.Predicate.DateTransform, expression.Predicate.Coalesce)
		return b.comparison(field, expression.Predicate.Operator, expression.Predicate.Values)
	case expression.Negated != nil:
		child, err := b.processExpression(*expression.Negated)
		return "NOT (" + child + ")", err
	case expression.EventRelation != nil:
		b.relationIndex++
		alias := fmt.Sprintf("e%d", b.relationIndex)
		child, err := b.eventExpression(alias, expression.EventRelation.Expression)
		if err != nil {
			return "", err
		}
		exists := "EXISTS"
		if expression.EventRelation.Quantifier == application.RelationNotExists {
			exists = "NOT EXISTS"
		}
		return exists + " (SELECT 1 FROM workflow_event_read_models " + alias +
			" WHERE " + alias + ".tenant_id=p.tenant_id AND " + alias +
			".instance_id=p.instance_id AND NOT " + alias + ".is_deleted AND " + child + ")", nil
	default:
		children := make([]string, 0, len(expression.Children))
		for _, value := range expression.Children {
			child, err := b.processExpression(value)
			if err != nil {
				return "", err
			}
			children = append(children, child)
		}
		operator := " AND "
		if expression.Logical == application.LogicalOr {
			operator = " OR "
		}
		return "(" + strings.Join(children, operator) + ")", nil
	}
}

func (b *sqlBuilder) eventExpression(alias string, expression application.EventExpression) (string, error) {
	switch {
	case expression.Predicate != nil:
		field, err := eventSQL(alias, expression.Predicate.Field)
		if err != nil {
			return "", err
		}
		field = b.fieldExpression(field, expression.Predicate.TextTransform, expression.Predicate.NumericTransform, expression.Predicate.DateTransform, expression.Predicate.Coalesce)
		return b.comparison(field, expression.Predicate.Operator, expression.Predicate.Values)
	case expression.Negated != nil:
		child, err := b.eventExpression(alias, *expression.Negated)
		return "NOT (" + child + ")", err
	default:
		children := make([]string, 0, len(expression.Children))
		for _, value := range expression.Children {
			child, err := b.eventExpression(alias, value)
			if err != nil {
				return "", err
			}
			children = append(children, child)
		}
		operator := " AND "
		if expression.Logical == application.LogicalOr {
			operator = " OR "
		}
		return "(" + strings.Join(children, operator) + ")", nil
	}
}

func (b *sqlBuilder) comparison(
	field string,
	operator application.Comparison,
	values []application.QueryValue,
) (string, error) {
	switch operator {
	case application.CompareIsNull:
		return field + " IS NULL", nil
	case application.CompareIsNotNull:
		return field + " IS NOT NULL", nil
	case application.CompareIn, application.CompareNotIn:
		value, err := queryArray(values)
		if err != nil {
			return "", err
		}
		keyword := "= ANY"
		if operator == application.CompareNotIn {
			keyword = "<> ALL"
		}
		return field + " " + keyword + "(" + b.bind(value) + ")", nil
	case application.CompareBetween, application.CompareNotBetween:
		keyword := "BETWEEN"
		if operator == application.CompareNotBetween {
			keyword = "NOT BETWEEN"
		}
		return field + " " + keyword + " " + b.bind(queryScalar(values[0])) + " AND " + b.bind(queryScalar(values[1])), nil
	case application.CompareContains, application.CompareStartsWith, application.CompareEndsWith:
		value := escapeLike(values[0].String)
		if operator == application.CompareContains {
			value = "%" + value + "%"
		} else if operator == application.CompareStartsWith {
			value += "%"
		} else {
			value = "%" + value
		}
		return field + " LIKE " + b.bind(value) + " ESCAPE '\\'", nil
	default:
		operators := map[application.Comparison]string{
			application.CompareEqual:              "=",
			application.CompareNotEqual:           "<>",
			application.CompareGreaterThan:        ">",
			application.CompareGreaterThanOrEqual: ">=",
			application.CompareLessThan:           "<",
			application.CompareLessThanOrEqual:    "<=",
		}
		sqlOperator, ok := operators[operator]
		if !ok {
			return "", application.ErrInvalidQuery
		}
		return field + sqlOperator + b.bind(queryScalar(values[0])), nil
	}
}

func (b *sqlBuilder) cursorCondition(
	sorts []application.ProcessSort,
	values []application.QueryValue,
) (string, error) {
	branches := make([]string, 0, len(sorts))
	for current, sort := range sorts {
		parts := make([]string, 0, current+1)
		for previous := 0; previous < current; previous++ {
			field, err := b.processSortSQL(sorts[previous])
			if err != nil {
				return "", err
			}
			parts = append(parts, field+"="+b.bind(queryScalar(values[previous])))
		}
		field, err := b.processSortSQL(sort)
		if err != nil {
			return "", err
		}
		operator := ">"
		if sort.Direction == application.SortDescending {
			operator = "<"
		}
		parts = append(parts, field+operator+b.bind(queryScalar(values[current])))
		branches = append(branches, "("+strings.Join(parts, " AND ")+")")
	}
	return "(" + strings.Join(branches, " OR ") + ")", nil
}

func (b *sqlBuilder) processSortSQL(sort application.ProcessSort) (string, error) {
	field, err := processSQL(sort.Field)
	if err != nil {
		return "", err
	}
	return b.fieldExpression(field, sort.TextTransform, sort.NumericTransform, sort.DateTransform, sort.Coalesce), nil
}

func (b *sqlBuilder) fieldExpression(field string, textTransform application.TextTransform, numericTransform application.NumericTransform, dateTransform application.DateTransform, coalesce *application.QueryValue) string {
	if coalesce != nil {
		field = "COALESCE(" + field + "," + b.bind(queryScalar(*coalesce)) + ")"
	}
	if numericTransform == application.NumericTransformAbs {
		field = "ABS(" + field + ")"
	}
	switch dateTransform {
	case application.DateTransformUTCDay:
		field = "DATE_TRUNC('day'," + field + " AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'"
	case application.DateTransformUTCWeek:
		field = "DATE_TRUNC('week'," + field + " AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'"
	case application.DateTransformUTCMonth:
		field = "DATE_TRUNC('month'," + field + " AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'"
	case application.DateTransformUTCYear:
		field = "DATE_TRUNC('year'," + field + " AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'"
	}
	switch textTransform {
	case application.TextTransformTrim:
		return "BTRIM(" + field + ")"
	case application.TextTransformLower:
		return "LOWER(" + field + ")"
	case application.TextTransformUpper:
		return "UPPER(" + field + ")"
	case application.TextTransformTrimLower:
		return "LOWER(BTRIM(" + field + "))"
	case application.TextTransformTrimUpper:
		return "UPPER(BTRIM(" + field + "))"
	default:
		return field
	}
}

func (b *sqlBuilder) bind(value any) string {
	b.args = append(b.args, value)
	return fmt.Sprintf("$%d", len(b.args))
}

func processSQL(field application.ProcessField) (string, error) {
	values := map[application.ProcessField]string{
		application.ProcessInstanceID:        "p.instance_id",
		application.ProcessWorkflowType:      "p.workflow_type",
		application.ProcessWorkflowVersion:   "p.workflow_version",
		application.ProcessStatus:            "p.status",
		application.ProcessActiveNodeID:      "p.active_node_id",
		application.ProcessLastEventSequence: "p.last_event_sequence",
		application.ProcessStartedAt:         "p.started_at",
		application.ProcessUpdatedAt:         "p.updated_at",
		application.ProcessCompletedAt:       "p.completed_at",
		application.ProcessDurationMS:        durationExpression,
		application.ProcessConfigVersion:     "p.config_version",
		application.ProcessPolicyVersion:     "p.policy_version",
		application.ProcessVersion:           "p.version",
		application.ProcessInitiatorActorID:  "p.initiator_actor_id",
		application.ProcessEventCount:       eventCountExpression,
		application.ProcessIsDeleted:        "p.is_deleted",
	}
	value, ok := values[field]
	if !ok {
		return "", application.ErrInvalidQuery
	}
	return value, nil
}

func eventSQL(alias string, field application.EventField) (string, error) {
	values := map[application.EventField]string{
		application.EventType:          "event_type",
		application.EventNodeID:        "node_id",
		application.EventActorID:       "actor_id",
		application.EventSequence:      "event_sequence",
		application.EventOccurredAt:    "occurred_at",
		application.EventConfigVersion: "config_version",
		application.EventPolicyVersion: "policy_version",
	}
	value, ok := values[field]
	if !ok {
		return "", application.ErrInvalidQuery
	}
	return alias + "." + value, nil
}

func queryScalar(value application.QueryValue) any {
	switch value.Kind {
	case application.ValueInteger:
		return value.Integer
	case application.ValueBoolean:
		return value.Boolean
	case application.ValueTimestamp:
		return value.Time
	default:
		return value.String
	}
}

func queryArray(values []application.QueryValue) (any, error) {
	switch values[0].Kind {
	case application.ValueString:
		result := make([]string, 0, len(values))
		for _, value := range values {
			result = append(result, value.String)
		}
		return result, nil
	case application.ValueInteger:
		result := make([]int64, 0, len(values))
		for _, value := range values {
			result = append(result, value.Integer)
		}
		return result, nil
	case application.ValueTimestamp:
		result := make([]time.Time, 0, len(values))
		for _, value := range values {
			result = append(result, value.Time)
		}
		return result, nil
	case application.ValueBoolean:
		result := make([]bool, 0, len(values))
		for _, value := range values {
			result = append(result, value.Boolean)
		}
		return result, nil
	default:
		return nil, application.ErrInvalidQuery
	}
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

var _ application.Store = (*Store)(nil)
