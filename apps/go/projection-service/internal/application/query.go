package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrInvalidQuery = errors.New("projection query is invalid")

type ValueKind uint8

const (
	ValueString ValueKind = iota + 1
	ValueInteger
	ValueBoolean
	ValueTimestamp
)

type QueryValue struct {
	Kind    ValueKind `json:"kind"`
	String  string    `json:"string,omitempty"`
	Integer int64     `json:"integer,omitempty"`
	Boolean bool      `json:"boolean,omitempty"`
	Time    time.Time `json:"time,omitempty"`
}

type Comparison uint8

const (
	CompareEqual Comparison = iota + 1
	CompareNotEqual
	CompareGreaterThan
	CompareGreaterThanOrEqual
	CompareLessThan
	CompareLessThanOrEqual
	CompareIn
	CompareNotIn
	CompareContains
	CompareStartsWith
	CompareIsNull
	CompareIsNotNull
)

type ProcessField uint8

const (
	ProcessInstanceID ProcessField = iota + 1
	ProcessWorkflowType
	ProcessWorkflowVersion
	ProcessStatus
	ProcessActiveNodeID
	ProcessLastEventSequence
	ProcessStartedAt
	ProcessUpdatedAt
	ProcessCompletedAt
	ProcessDurationMS
	ProcessConfigVersion
	ProcessPolicyVersion
	ProcessVersion
)

type EventField uint8

const (
	EventType EventField = iota + 1
	EventNodeID
	EventActorID
	EventSequence
	EventOccurredAt
	EventConfigVersion
	EventPolicyVersion
)

type Logical uint8

const (
	LogicalAnd Logical = iota + 1
	LogicalOr
)

type ProcessPredicate struct {
	Field    ProcessField `json:"field"`
	Operator Comparison   `json:"operator"`
	Values   []QueryValue `json:"values,omitempty"`
}

type EventPredicate struct {
	Field    EventField   `json:"field"`
	Operator Comparison   `json:"operator"`
	Values   []QueryValue `json:"values,omitempty"`
}

type EventExpression struct {
	Predicate *EventPredicate   `json:"predicate,omitempty"`
	Logical   Logical           `json:"logical,omitempty"`
	Children  []EventExpression `json:"children,omitempty"`
	Negated   *EventExpression  `json:"negated,omitempty"`
}

type EventRelation struct {
	Expression EventExpression `json:"expression"`
	MustExist  bool            `json:"must_exist"`
}

type ProcessExpression struct {
	Predicate     *ProcessPredicate   `json:"predicate,omitempty"`
	Logical       Logical             `json:"logical,omitempty"`
	Children      []ProcessExpression `json:"children,omitempty"`
	Negated       *ProcessExpression  `json:"negated,omitempty"`
	EventRelation *EventRelation      `json:"event_relation,omitempty"`
}

type SortDirection uint8

const (
	SortAscending SortDirection = iota + 1
	SortDescending
)

type ProcessSort struct {
	Field     ProcessField  `json:"field"`
	Direction SortDirection `json:"direction"`
}

type QueryCursor struct {
	QueryHash string       `json:"query_hash"`
	Values    []QueryValue `json:"values"`
}

type ProcessQuery struct {
	TenantID       string             `json:"tenant_id"`
	Filter         *ProcessExpression `json:"filter,omitempty"`
	Sort           []ProcessSort      `json:"sort"`
	IncludeDeleted bool               `json:"include_deleted"`
	Limit          int                `json:"-"`
	After          *QueryCursor       `json:"-"`
}

type CompletedProcess struct {
	Instance   Instance
	DurationMS uint64
	EventCount uint64
}

type queryBudget struct {
	depth, predicates, relationJoins uint32
}

func (s *Service) QueryCompleted(
	ctx context.Context,
	query ProcessQuery,
) ([]CompletedProcess, int, ProcessQuery, error) {
	if query.TenantID == "" {
		return nil, 0, query, fmt.Errorf("%w: tenant is required", ErrInvalidQuery)
	}
	policy, err := s.policy.Policy(query.TenantID)
	if err != nil {
		return nil, 0, query, err
	}
	if err = normalizeAndValidateQuery(&query, policy); err != nil {
		return nil, 0, query, err
	}
	queryHash, err := query.Fingerprint()
	if err != nil {
		return nil, 0, query, fmt.Errorf("%w: fingerprint query", ErrInvalidQuery)
	}
	if query.After != nil && query.After.QueryHash != queryHash {
		return nil, 0, query, fmt.Errorf("%w: page token belongs to another query", ErrInvalidQuery)
	}
	effectiveLimit := query.Limit
	query.Limit++
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	processes, err := s.store.QueryCompleted(queryCtx, query)
	return processes, effectiveLimit, query, err
}

func normalizeAndValidateQuery(query *ProcessQuery, policy RuntimePolicy) error {
	if policy.QueryMaxFilterDepth == 0 || policy.QueryMaxPredicates == 0 ||
		policy.QueryMaxSortFields == 0 ||
		policy.QueryMaxInValues == 0 || policy.QueryTimeout <= 0 ||
		policy.QueryMaxStringBytes == 0 {
		return fmt.Errorf("%w: dynamic query policy is incomplete", ErrInvalidQuery)
	}
	if query.Limit == 0 {
		query.Limit = int(policy.QueryDefaultPageSize)
	}
	if query.Limit < 0 || uint32(query.Limit) > policy.QueryMaxPageSize {
		return fmt.Errorf("%w: page size exceeds policy", ErrInvalidQuery)
	}
	if len(query.Sort) == 0 {
		query.Sort = []ProcessSort{{Field: ProcessCompletedAt, Direction: SortDescending}}
	}
	if uint32(len(query.Sort)) > policy.QueryMaxSortFields {
		return fmt.Errorf("%w: sort count exceeds policy", ErrInvalidQuery)
	}
	seen := make(map[ProcessField]struct{}, len(query.Sort)+1)
	for _, sort := range query.Sort {
		if !validProcessField(sort.Field) ||
			(sort.Direction != SortAscending && sort.Direction != SortDescending) {
			return fmt.Errorf("%w: sort is unsupported", ErrInvalidQuery)
		}
		if _, duplicate := seen[sort.Field]; duplicate {
			return fmt.Errorf("%w: duplicate sort field", ErrInvalidQuery)
		}
		seen[sort.Field] = struct{}{}
	}
	if _, stable := seen[ProcessInstanceID]; !stable {
		query.Sort = append(query.Sort, ProcessSort{Field: ProcessInstanceID, Direction: SortAscending})
	}
	budget := queryBudget{}
	if query.Filter != nil {
		if err := validateProcessExpression(*query.Filter, 1, policy, &budget); err != nil {
			return err
		}
	}
	if query.After != nil {
		if len(query.After.Values) != len(query.Sort) {
			return fmt.Errorf("%w: page token sort arity is invalid", ErrInvalidQuery)
		}
		for index, sort := range query.Sort {
			if query.After.Values[index].Kind != processFieldKind(sort.Field) {
				return fmt.Errorf("%w: page token sort type is invalid", ErrInvalidQuery)
			}
		}
	}
	return nil
}

func validateProcessExpression(
	expression ProcessExpression,
	depth uint32,
	policy RuntimePolicy,
	budget *queryBudget,
) error {
	if depth > policy.QueryMaxFilterDepth {
		return fmt.Errorf("%w: filter depth exceeds policy", ErrInvalidQuery)
	}
	budget.depth = max(budget.depth, depth)
	kinds := 0
	if expression.Predicate != nil {
		kinds++
	}
	if expression.Logical != 0 || len(expression.Children) != 0 {
		kinds++
	}
	if expression.Negated != nil {
		kinds++
	}
	if expression.EventRelation != nil {
		kinds++
	}
	if kinds != 1 {
		return fmt.Errorf("%w: expression must contain exactly one node", ErrInvalidQuery)
	}
	switch {
	case expression.Predicate != nil:
		budget.predicates++
		if budget.predicates > policy.QueryMaxPredicates {
			return fmt.Errorf("%w: predicate count exceeds policy", ErrInvalidQuery)
		}
		return validatePredicate(expression.Predicate.Field, expression.Predicate.Operator, expression.Predicate.Values, policy)
	case expression.Negated != nil:
		return validateProcessExpression(*expression.Negated, depth+1, policy, budget)
	case expression.EventRelation != nil:
		budget.relationJoins++
		if budget.relationJoins > policy.QueryMaxRelationJoins {
			return fmt.Errorf("%w: relation join count exceeds policy", ErrInvalidQuery)
		}
		return validateEventExpression(expression.EventRelation.Expression, depth+1, policy, budget)
	default:
		if (expression.Logical != LogicalAnd && expression.Logical != LogicalOr) || len(expression.Children) < 2 {
			return fmt.Errorf("%w: logical group is invalid", ErrInvalidQuery)
		}
		for _, child := range expression.Children {
			if err := validateProcessExpression(child, depth+1, policy, budget); err != nil {
				return err
			}
		}
		return nil
	}
}

func validateEventExpression(expression EventExpression, depth uint32, policy RuntimePolicy, budget *queryBudget) error {
	if depth > policy.QueryMaxFilterDepth {
		return fmt.Errorf("%w: event filter depth exceeds policy", ErrInvalidQuery)
	}
	kinds := 0
	if expression.Predicate != nil {
		kinds++
	}
	if expression.Logical != 0 || len(expression.Children) != 0 {
		kinds++
	}
	if expression.Negated != nil {
		kinds++
	}
	if kinds != 1 {
		return fmt.Errorf("%w: event expression must contain exactly one node", ErrInvalidQuery)
	}
	if expression.Predicate != nil {
		budget.predicates++
		if budget.predicates > policy.QueryMaxPredicates {
			return fmt.Errorf("%w: predicate count exceeds policy", ErrInvalidQuery)
		}
		return validateEventPredicate(*expression.Predicate, policy)
	}
	if expression.Negated != nil {
		return validateEventExpression(*expression.Negated, depth+1, policy, budget)
	}
	if (expression.Logical != LogicalAnd && expression.Logical != LogicalOr) || len(expression.Children) < 2 {
		return fmt.Errorf("%w: event logical group is invalid", ErrInvalidQuery)
	}
	for _, child := range expression.Children {
		if err := validateEventExpression(child, depth+1, policy, budget); err != nil {
			return err
		}
	}
	return nil
}

func validateEventPredicate(predicate EventPredicate, policy RuntimePolicy) error {
	if predicate.Field < EventType || predicate.Field > EventPolicyVersion {
		return fmt.Errorf("%w: event field is unsupported", ErrInvalidQuery)
	}
	return validateValues(eventFieldKind(predicate.Field), predicate.Operator, predicate.Values, policy)
}

func validatePredicate(field ProcessField, operator Comparison, values []QueryValue, policy RuntimePolicy) error {
	if !validProcessField(field) {
		return fmt.Errorf("%w: process field is unsupported", ErrInvalidQuery)
	}
	return validateValues(processFieldKind(field), operator, values, policy)
}

func validateValues(kind ValueKind, operator Comparison, values []QueryValue, policy RuntimePolicy) error {
	wanted := 1
	if operator == CompareIn || operator == CompareNotIn {
		wanted = -1
	}
	if operator == CompareIsNull || operator == CompareIsNotNull {
		wanted = 0
	}
	if operator < CompareEqual || operator > CompareIsNotNull ||
		(wanted >= 0 && len(values) != wanted) ||
		(wanted == -1 && (len(values) == 0 || uint32(len(values)) > policy.QueryMaxInValues)) {
		return fmt.Errorf("%w: operator value cardinality is invalid", ErrInvalidQuery)
	}
	if (operator == CompareContains || operator == CompareStartsWith) && kind != ValueString {
		return fmt.Errorf("%w: text operator requires a string field", ErrInvalidQuery)
	}
	for _, value := range values {
		if value.Kind != kind || (kind == ValueString && (value.String == "" || uint32(len(value.String)) > policy.QueryMaxStringBytes)) ||
			(kind == ValueTimestamp && value.Time.IsZero()) {
			return fmt.Errorf("%w: predicate value type is invalid", ErrInvalidQuery)
		}
	}
	return nil
}

func validProcessField(field ProcessField) bool {
	return field >= ProcessInstanceID && field <= ProcessVersion
}

func processFieldKind(field ProcessField) ValueKind {
	switch field {
	case ProcessLastEventSequence, ProcessDurationMS, ProcessVersion:
		return ValueInteger
	case ProcessStartedAt, ProcessUpdatedAt, ProcessCompletedAt:
		return ValueTimestamp
	default:
		return ValueString
	}
}

func eventFieldKind(field EventField) ValueKind {
	if field == EventSequence {
		return ValueInteger
	}
	if field == EventOccurredAt {
		return ValueTimestamp
	}
	return ValueString
}

func (q ProcessQuery) Fingerprint() (string, error) {
	copy := q
	copy.Limit = 0
	copy.After = nil
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func CursorFor(query ProcessQuery, process CompletedProcess) (*QueryCursor, error) {
	hash, err := query.Fingerprint()
	if err != nil {
		return nil, err
	}
	values := make([]QueryValue, 0, len(query.Sort))
	for _, sort := range query.Sort {
		value, valueErr := sortValue(sort.Field, process)
		if valueErr != nil {
			return nil, valueErr
		}
		values = append(values, value)
	}
	return &QueryCursor{QueryHash: hash, Values: values}, nil
}

func sortValue(field ProcessField, process CompletedProcess) (QueryValue, error) {
	instance := process.Instance
	switch field {
	case ProcessInstanceID:
		return QueryValue{Kind: ValueString, String: instance.InstanceID}, nil
	case ProcessWorkflowType:
		return QueryValue{Kind: ValueString, String: instance.WorkflowType}, nil
	case ProcessWorkflowVersion:
		return QueryValue{Kind: ValueString, String: instance.WorkflowVersion}, nil
	case ProcessStatus:
		return QueryValue{Kind: ValueString, String: instance.Status}, nil
	case ProcessActiveNodeID:
		return QueryValue{Kind: ValueString, String: instance.ActiveNodeID}, nil
	case ProcessConfigVersion:
		return QueryValue{Kind: ValueString, String: instance.ConfigVersion}, nil
	case ProcessPolicyVersion:
		return QueryValue{Kind: ValueString, String: instance.PolicyVersion}, nil
	case ProcessLastEventSequence:
		return QueryValue{Kind: ValueInteger, Integer: int64(instance.LastEventSequence)}, nil
	case ProcessVersion:
		return QueryValue{Kind: ValueInteger, Integer: int64(instance.Version)}, nil
	case ProcessDurationMS:
		return QueryValue{Kind: ValueInteger, Integer: int64(process.DurationMS)}, nil
	case ProcessStartedAt:
		return QueryValue{Kind: ValueTimestamp, Time: instance.StartedAt}, nil
	case ProcessUpdatedAt:
		return QueryValue{Kind: ValueTimestamp, Time: instance.UpdatedAt}, nil
	case ProcessCompletedAt:
		if instance.CompletedAt == nil {
			return QueryValue{}, fmt.Errorf("%w: completed timestamp is absent", ErrInvalidQuery)
		}
		return QueryValue{Kind: ValueTimestamp, Time: *instance.CompletedAt}, nil
	default:
		return QueryValue{}, fmt.Errorf("%w: sort field is unsupported", ErrInvalidQuery)
	}
}
