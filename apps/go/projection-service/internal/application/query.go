package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidQuery   = errors.New("projection query is invalid")
	ErrQueryForbidden = errors.New("projection query is forbidden")
)

type QueryViolation struct {
	Path    string
	Rule    string
	Message string
}

type QueryValidationError struct {
	Violations []QueryViolation
}

func (e *QueryValidationError) Error() string {
	if len(e.Violations) == 0 {
		return ErrInvalidQuery.Error()
	}
	return e.Violations[0].Message
}

func (e *QueryValidationError) Unwrap() error { return ErrInvalidQuery }

func invalidQuery(path, rule, message string) error {
	return &QueryValidationError{Violations: []QueryViolation{{Path: path, Rule: rule, Message: message}}}
}

type ActorProofType int32

const (
	ActorProofOriginalJWT ActorProofType = iota + 1
	ActorProofSignedInternalContext
)

type QueryCredential struct {
	Type        ActorProofType
	SignedProof []byte
}

type QueryAuthorizationRequest struct {
	TenantID   string
	Credential QueryCredential
	Policy     RuntimePolicy
}

type QueryAuthorization struct {
	SubjectID     string
	PolicyVersion string
	Filter        *ProcessExpression
	Fields        *FieldAuthorization
	Capabilities  []string
}

type FieldAuthorization struct {
	ReadFields  []string `json:"read_fields"`
	QueryFields []string `json:"query_fields"`
}

type QueryAuthorizer interface {
	AuthorizeQuery(context.Context, QueryAuthorizationRequest) (QueryAuthorization, error)
}

type denyQueryAuthorizer struct{}

func (denyQueryAuthorizer) AuthorizeQuery(context.Context, QueryAuthorizationRequest) (QueryAuthorization, error) {
	return QueryAuthorization{}, ErrQueryForbidden
}

type ValueKind uint8

const (
	ValueString ValueKind = iota + 1
	ValueInteger
	ValueBoolean
	ValueTimestamp
	ValueDecimal
)

type QueryValue struct {
	Kind    ValueKind `json:"kind"`
	String  string    `json:"string,omitempty"`
	Integer int64     `json:"integer,omitempty"`
	Boolean bool      `json:"boolean,omitempty"`
	Time    time.Time `json:"time,omitempty"`
	Decimal string    `json:"decimal,omitempty"`
}

type Comparison int32

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
	CompareEndsWith
	CompareBetween
	CompareNotBetween
)

type TextTransform int32

const (
	TextTransformNone TextTransform = iota + 1
	TextTransformTrim
	TextTransformLower
	TextTransformUpper
	TextTransformTrimLower
	TextTransformTrimUpper
)

type NumericTransform int32

const (
	NumericTransformNone NumericTransform = iota + 1
	NumericTransformAbs
)

type DateTransform int32

const (
	DateTransformNone DateTransform = iota + 1
	DateTransformUTCDay
	DateTransformUTCWeek
	DateTransformUTCMonth
	DateTransformUTCYear
)

type ProcessField int32

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
	ProcessInitiatorActorID
	ProcessEventCount
	ProcessIsDeleted
)

type EventField int32

const (
	EventType EventField = iota + 1
	EventNodeID
	EventActorID
	EventSequence
	EventOccurredAt
	EventConfigVersion
	EventPolicyVersion
)

type Logical int32

const (
	LogicalAnd Logical = iota + 1
	LogicalOr
)

type ProcessPredicate struct {
	Field            ProcessField     `json:"field"`
	Operator         Comparison       `json:"operator"`
	Values           []QueryValue     `json:"values,omitempty"`
	TextTransform    TextTransform    `json:"text_transform,omitempty"`
	Coalesce         *QueryValue      `json:"coalesce,omitempty"`
	NumericTransform NumericTransform `json:"numeric_transform,omitempty"`
	DateTransform    DateTransform    `json:"date_transform,omitempty"`
}

type EventPredicate struct {
	Field            EventField       `json:"field"`
	Operator         Comparison       `json:"operator"`
	Values           []QueryValue     `json:"values,omitempty"`
	TextTransform    TextTransform    `json:"text_transform,omitempty"`
	Coalesce         *QueryValue      `json:"coalesce,omitempty"`
	NumericTransform NumericTransform `json:"numeric_transform,omitempty"`
	DateTransform    DateTransform    `json:"date_transform,omitempty"`
}

type EventExpression struct {
	Predicate *EventPredicate   `json:"predicate,omitempty"`
	Logical   Logical           `json:"logical,omitempty"`
	Children  []EventExpression `json:"children,omitempty"`
	Negated   *EventExpression  `json:"negated,omitempty"`
}

type EventRelation struct {
	Expression EventExpression    `json:"expression"`
	Quantifier RelationQuantifier `json:"quantifier"`
}

type RelationQuantifier int32

const (
	RelationExists RelationQuantifier = iota + 1
	RelationNotExists
)

type ProcessExpression struct {
	Predicate     *ProcessPredicate   `json:"predicate,omitempty"`
	Logical       Logical             `json:"logical,omitempty"`
	Children      []ProcessExpression `json:"children,omitempty"`
	Negated       *ProcessExpression  `json:"negated,omitempty"`
	EventRelation *EventRelation      `json:"event_relation,omitempty"`
}

type SortDirection int32

const (
	SortAscending SortDirection = iota + 1
	SortDescending
)

type ProcessSort struct {
	Field            ProcessField     `json:"field"`
	Direction        SortDirection    `json:"direction"`
	TextTransform    TextTransform    `json:"text_transform,omitempty"`
	Coalesce         *QueryValue      `json:"coalesce,omitempty"`
	NumericTransform NumericTransform `json:"numeric_transform,omitempty"`
	DateTransform    DateTransform    `json:"date_transform,omitempty"`
}

type QueryCursor struct {
	QueryHash string       `json:"query_hash"`
	Values    []QueryValue `json:"values"`
}

type ProcessQuery struct {
	TenantID                   string              `json:"tenant_id"`
	Filter                     *ProcessExpression  `json:"filter,omitempty"`
	Sort                       []ProcessSort       `json:"sort"`
	IncludeDeleted             bool                `json:"include_deleted"`
	Limit                      int                 `json:"-"`
	After                      *QueryCursor        `json:"-"`
	Credential                 QueryCredential     `json:"-"`
	AuthorizationSubject       string              `json:"authorization_subject"`
	AuthorizationPolicyVersion string              `json:"authorization_policy_version"`
	CursorSigningKeyID         string              `json:"-"`
	Scope                      QueryScope          `json:"scope"`
	Fields                     *FieldAuthorization `json:"fields,omitempty"`
	SelectedFields             []ProcessField      `json:"selected_fields,omitempty"`
	Collapse                   ProcessField        `json:"collapse,omitempty"`
	ExpandEvents               *EventExpansion     `json:"expand_events,omitempty"`
}

type EventExpansion struct {
	Filter *EventExpression `json:"filter,omitempty"`
	Limit  uint32           `json:"limit"`
}

type QueryScope uint8

const (
	QueryScopeAll QueryScope = iota + 1
	QueryScopeActive
	QueryScopeCompleted
	QueryScopeDeleted
)

type CompletedProcess struct {
	Instance   Instance
	DurationMS uint64
	EventCount uint64
	Events     []WorkflowEvent
}

type WorkflowEvent struct {
	InstanceID, EventType, NodeID, ActorID, ConfigVersion, PolicyVersion string
	Sequence                                                             uint64
	OccurredAt                                                           time.Time
	IsDeleted                                                            bool
}

type queryBudget struct {
	depth, predicates, relationJoins uint32
}

func (s *Service) QueryCompleted(
	ctx context.Context,
	query ProcessQuery,
) ([]CompletedProcess, int, ProcessQuery, error) {
	query.Scope = QueryScopeCompleted
	return s.queryProcesses(ctx, query)
}

func (s *Service) QueryInstances(ctx context.Context, query ProcessQuery) ([]CompletedProcess, int, ProcessQuery, error) {
	if query.Scope == 0 {
		query.Scope = QueryScopeAll
	}
	if len(query.Sort) == 0 {
		query.Sort = []ProcessSort{{Field: ProcessUpdatedAt, Direction: SortDescending}, {Field: ProcessInstanceID, Direction: SortDescending}}
	}
	return s.queryProcesses(ctx, query)
}

func (s *Service) queryProcesses(ctx context.Context, query ProcessQuery) ([]CompletedProcess, int, ProcessQuery, error) {
	queryCtx, cancel, query, effectiveLimit, err := s.prepareProcessQuery(ctx, query)
	if err != nil { return nil, 0, query, err }
	defer cancel()
	var processes []CompletedProcess
	if query.Scope != QueryScopeCompleted { processes, err = s.store.QueryInstances(queryCtx, query) } else { processes, err = s.store.QueryCompleted(queryCtx, query) }
	return processes, effectiveLimit, query, err
}

func (s *Service) prepareProcessQuery(ctx context.Context, query ProcessQuery) (context.Context, context.CancelFunc, ProcessQuery, int, error) {
	noop:=func(){}
	if query.TenantID == "" {
		return ctx, noop, query, 0, fmt.Errorf("%w: tenant is required", ErrInvalidQuery)
	}
	policy, err := s.policy.Policy(query.TenantID)
	if err != nil {
		return ctx, noop, query, 0, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	authorization, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{
		TenantID:   query.TenantID,
		Credential: query.Credential,
		Policy:     policy,
	})
	if err != nil {
		cancel(); return ctx, noop, query, 0, err
	}
	if authorization.SubjectID == "" || authorization.PolicyVersion == "" {
		cancel(); return ctx, noop, query, 0, ErrQueryForbidden
	}
	query.AuthorizationSubject = authorization.SubjectID
	query.AuthorizationPolicyVersion = authorization.PolicyVersion
	query.CursorSigningKeyID = policy.QueryCursorSigningKeyID
	query.Fields = authorization.Fields
	if query.Scope < QueryScopeAll || query.Scope > QueryScopeDeleted {
		cancel(); return ctx, noop, query, 0, fmt.Errorf("%w: query scope is unsupported", ErrInvalidQuery)
	}
	if err = validateFieldAccess(query); err != nil {
		cancel(); return ctx, noop, query, 0, err
	}
	query.Filter = intersectFilters(query.Filter, authorization.Filter)
	if err = normalizeAndValidateQuery(&query, policy); err != nil {
		cancel(); return ctx, noop, query, 0, err
	}
	queryHash, err := query.Fingerprint()
	if err != nil {
		cancel(); return ctx, noop, query, 0, fmt.Errorf("%w: fingerprint query", ErrInvalidQuery)
	}
	if query.After != nil && query.After.QueryHash != queryHash {
		cancel(); return ctx, noop, query, 0, fmt.Errorf("%w: page token belongs to another query", ErrInvalidQuery)
	}
	effectiveLimit := query.Limit
	query.Limit++
	return queryCtx, cancel, query, effectiveLimit, nil
}

func intersectFilters(client, authorization *ProcessExpression) *ProcessExpression {
	if client == nil {
		return authorization
	}
	if authorization == nil {
		return client
	}
	return &ProcessExpression{
		Logical:  LogicalAnd,
		Children: []ProcessExpression{*authorization, *client},
	}
}

func normalizeAndValidateQuery(query *ProcessQuery, policy RuntimePolicy) error {
	if err := validateQueryPolicy(policy); err != nil {
		return err
	}
	if query.Limit == 0 {
		query.Limit = int(policy.QueryDefaultPageSize)
	}
	if query.Limit < 0 || uint32(query.Limit) > policy.QueryMaxPageSize {
		return invalidQuery("page_size", "page_size_limit", "page size exceeds tenant policy")
	}
	if len(query.Sort) == 0 {
		query.Sort = []ProcessSort{{Field: ProcessCompletedAt, Direction: SortDescending}}
	}
	seen := make(map[ProcessField]struct{}, len(query.Sort)+1)
	for index := range query.Sort {
		sort := &query.Sort[index]
		if sort.TextTransform == 0 {
			sort.TextTransform = TextTransformNone
		}
		if sort.NumericTransform == 0 {
			sort.NumericTransform = NumericTransformNone
		}
		if sort.DateTransform == 0 {
			sort.DateTransform = DateTransformNone
		}
		if !validProcessField(sort.Field) || (sort.Direction != SortAscending && sort.Direction != SortDescending) {
			return invalidQuery(fmt.Sprintf("sort[%d]", index), "unsupported_sort", "sort field or direction is unsupported")
		}
		if _, duplicate := seen[sort.Field]; duplicate {
			return invalidQuery(fmt.Sprintf("sort[%d].field", index), "duplicate_sort", "each sort field may appear only once")
		}
		seen[sort.Field] = struct{}{}
		if err := validateSort(*sort, policy); err != nil {
			return err
		}
	}
	if _, stable := seen[ProcessInstanceID]; !stable {
		query.Sort = append(query.Sort, ProcessSort{Field: ProcessInstanceID, Direction: SortAscending, TextTransform: TextTransformNone, NumericTransform: NumericTransformNone, DateTransform: DateTransformNone})
	}
	if uint32(len(query.Sort)) > policy.QueryMaxSortFields {
		return invalidQuery("sort", "sort_limit", "sort count including the stable tie-breaker exceeds tenant policy")
	}
	if query.Collapse != 0 {
		if !validProcessField(query.Collapse) || len(query.Sort) == 0 || query.Sort[0].Field != query.Collapse {
			return invalidQuery("collapse", "collapse_sort", "collapse field must be the first sort field")
		}
	}
	if query.ExpandEvents != nil {
		if query.ExpandEvents.Limit == 0 || query.ExpandEvents.Limit > policy.QueryMaxInValues {
			return invalidQuery("expand_events.limit", "expansion_limit", "event expansion limit exceeds tenant policy")
		}
		if err := normalizeEventExpression(query.ExpandEvents.Filter); err != nil {
			return err
		}
		if query.ExpandEvents.Filter != nil {
			expansionBudget := queryBudget{}
			if err := validateEventExpression(*query.ExpandEvents.Filter, 1, policy, &expansionBudget); err != nil {
				return err
			}
		}
	}
	budget := queryBudget{}
	if query.Filter != nil {
		if err := normalizeProcessExpression(query.Filter); err != nil {
			return err
		}
		if err := validateProcessExpression(*query.Filter, 1, policy, &budget); err != nil {
			return err
		}
	}
	if query.After != nil {
		if len(query.After.Values) != len(query.Sort) {
			return invalidQuery("page_token", "cursor_arity", "page token sort value count is invalid")
		}
		for index, sort := range query.Sort {
			if query.After.Values[index].Kind != processFieldKind(sort.Field) {
				return invalidQuery("page_token", "cursor_type", "page token sort value type is invalid")
			}
		}
	}
	return nil
}

func validateQueryPolicy(policy RuntimePolicy) error {
	if policy.QueryDefaultPageSize == 0 || policy.QueryMaxPageSize == 0 ||
		policy.QueryDefaultPageSize > policy.QueryMaxPageSize ||
		policy.QueryMaxFilterDepth == 0 || policy.QueryMaxPredicates == 0 ||
		policy.QueryMaxSortFields == 0 ||
		policy.QueryMaxInValues == 0 || policy.QueryTimeout <= 0 ||
		policy.QueryMaxStringBytes == 0 {
		return invalidQuery("policy", "incomplete_policy", "dynamic query policy is incomplete")
	}
	if policy.QueryCursorSigningKeyID == "" || policy.QueryAllCapability == "" ||
		policy.QueryOwnCapability == "" || policy.QueryAllCapability == policy.QueryOwnCapability {
		return invalidQuery("policy", "incomplete_authorization_policy", "query authorization policy is incomplete")
	}
	return nil
}

func normalizeProcessExpression(expression *ProcessExpression) error {
	if expression == nil {
		return nil
	}
	if expression.Predicate != nil {
		if err := normalizeProcessPredicate(expression.Predicate); err != nil {
			return err
		}
	}
	for index := range expression.Children {
		if err := normalizeProcessExpression(&expression.Children[index]); err != nil {
			return err
		}
	}
	if err := normalizeProcessExpression(expression.Negated); err != nil {
		return err
	}
	if expression.EventRelation != nil {
		return normalizeEventExpression(&expression.EventRelation.Expression)
	}
	return nil
}

func normalizeEventExpression(expression *EventExpression) error {
	if expression == nil {
		return nil
	}
	if expression.Predicate != nil {
		predicate := expression.Predicate
		normalizeTransforms(&predicate.TextTransform, &predicate.NumericTransform, &predicate.DateTransform)
		if err := normalizeValues(predicate.Values, predicate.TextTransform, predicate.NumericTransform, predicate.DateTransform); err != nil {
			return err
		}
	}
	for index := range expression.Children {
		if err := normalizeEventExpression(&expression.Children[index]); err != nil {
			return err
		}
	}
	return normalizeEventExpression(expression.Negated)
}

func normalizeProcessPredicate(predicate *ProcessPredicate) error {
	normalizeTransforms(&predicate.TextTransform, &predicate.NumericTransform, &predicate.DateTransform)
	return normalizeValues(predicate.Values, predicate.TextTransform, predicate.NumericTransform, predicate.DateTransform)
}

func normalizeTransforms(text *TextTransform, numeric *NumericTransform, date *DateTransform) {
	if *text == 0 {
		*text = TextTransformNone
	}
	if *numeric == 0 {
		*numeric = NumericTransformNone
	}
	if *date == 0 {
		*date = DateTransformNone
	}
}

func normalizeValues(values []QueryValue, text TextTransform, numeric NumericTransform, date DateTransform) error {
	for index := range values {
		switch values[index].Kind {
		case ValueString:
			values[index].String = applyTextTransform(values[index].String, text)
		case ValueInteger:
			if numeric == NumericTransformAbs {
				if values[index].Integer == math.MinInt64 {
					return fmt.Errorf("%w: absolute numeric transform overflows", ErrInvalidQuery)
				}
				values[index].Integer = int64Abs(values[index].Integer)
			}
		case ValueTimestamp:
			values[index].Time = applyDateTransform(values[index].Time, date)
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
		return validateProcessPredicate(*expression.Predicate, policy)
	case expression.Negated != nil:
		return validateProcessExpression(*expression.Negated, depth+1, policy, budget)
	case expression.EventRelation != nil:
		budget.relationJoins++
		if budget.relationJoins > policy.QueryMaxRelationJoins {
			return fmt.Errorf("%w: relation join count exceeds policy", ErrInvalidQuery)
		}
		if expression.EventRelation.Quantifier != RelationExists &&
			expression.EventRelation.Quantifier != RelationNotExists {
			return fmt.Errorf("%w: relation quantifier is invalid", ErrInvalidQuery)
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
	return validateValues(eventFieldKind(predicate.Field), predicate.Operator, predicate.Values, predicate.TextTransform, predicate.NumericTransform, predicate.DateTransform, predicate.Coalesce, policy)
}

func validatePredicate(field ProcessField, operator Comparison, values []QueryValue, policy RuntimePolicy) error {
	if !validProcessField(field) {
		return fmt.Errorf("%w: process field is unsupported", ErrInvalidQuery)
	}
	return validateValues(processFieldKind(field), operator, values, TextTransformNone, NumericTransformNone, DateTransformNone, nil, policy)
}

func validateSort(sort ProcessSort, policy RuntimePolicy) error {
	kind := processFieldKind(sort.Field)
	transform := sort.TextTransform
	if transform == 0 {
		transform = TextTransformNone
	}
	if transform < TextTransformNone || transform > TextTransformTrimUpper ||
		(kind != ValueString && transform != TextTransformNone) {
		return invalidQuery("sort.text_transform", "transform_type", "sort text transform is incompatible with field type")
	}
	numericTransform := sort.NumericTransform
	if numericTransform == 0 {
		numericTransform = NumericTransformNone
	}
	if numericTransform < NumericTransformNone || numericTransform > NumericTransformAbs ||
		(kind != ValueInteger && numericTransform != NumericTransformNone) {
		return invalidQuery("sort.numeric_transform", "transform_type", "sort numeric transform is incompatible with field type")
	}
	dateTransform := sort.DateTransform
	if dateTransform == 0 {
		dateTransform = DateTransformNone
	}
	if dateTransform < DateTransformNone || dateTransform > DateTransformUTCYear ||
		(kind != ValueTimestamp && dateTransform != DateTransformNone) {
		return invalidQuery("sort.date_transform", "transform_type", "sort date transform is incompatible with field type")
	}
	if sort.Coalesce != nil {
		value := sort.Coalesce
		if value.Kind != kind ||
			(kind == ValueString && (value.String == "" || uint32(len(value.String)) > policy.QueryMaxStringBytes)) ||
			(kind == ValueTimestamp && value.Time.IsZero()) {
			return invalidQuery("sort.coalesce_value", "value_type", "sort coalesce value type is invalid")
		}
	}
	return nil
}

func validateProcessPredicate(predicate ProcessPredicate, policy RuntimePolicy) error {
	if !validProcessField(predicate.Field) {
		return fmt.Errorf("%w: process field is unsupported", ErrInvalidQuery)
	}
	return validateValues(processFieldKind(predicate.Field), predicate.Operator, predicate.Values, predicate.TextTransform, predicate.NumericTransform, predicate.DateTransform, predicate.Coalesce, policy)
}

func validateValues(kind ValueKind, operator Comparison, values []QueryValue, textTransform TextTransform, numericTransform NumericTransform, dateTransform DateTransform, coalesce *QueryValue, policy RuntimePolicy) error {
	if textTransform == 0 {
		textTransform = TextTransformNone
	}
	if textTransform < TextTransformNone || textTransform > TextTransformTrimUpper ||
		(kind != ValueString && textTransform != TextTransformNone) {
		return invalidQuery("predicate.text_transform", "transform_type", "text transform is incompatible with field type")
	}
	if numericTransform == 0 {
		numericTransform = NumericTransformNone
	}
	if numericTransform < NumericTransformNone || numericTransform > NumericTransformAbs ||
		(kind != ValueInteger && numericTransform != NumericTransformNone) {
		return invalidQuery("predicate.numeric_transform", "transform_type", "numeric transform is incompatible with field type")
	}
	if dateTransform == 0 {
		dateTransform = DateTransformNone
	}
	if dateTransform < DateTransformNone || dateTransform > DateTransformUTCYear ||
		(kind != ValueTimestamp && dateTransform != DateTransformNone) {
		return invalidQuery("predicate.date_transform", "transform_type", "date transform is incompatible with field type")
	}
	if coalesce != nil {
		if coalesce.Kind != kind ||
			(kind == ValueString && (coalesce.String == "" || uint32(len(coalesce.String)) > policy.QueryMaxStringBytes)) ||
			(kind == ValueTimestamp && coalesce.Time.IsZero()) {
			return invalidQuery("predicate.coalesce_value", "value_type", "coalesce value type is invalid")
		}
	}
	if !operatorAllowed(kind, operator) {
		return invalidQuery("predicate.operator", "operator_type", "operator is incompatible with field type")
	}
	wanted := 1
	if operator == CompareIn || operator == CompareNotIn {
		wanted = -1
	}
	if operator == CompareIsNull || operator == CompareIsNotNull {
		wanted = 0
	}
	if operator == CompareBetween || operator == CompareNotBetween {
		wanted = 2
	}
	if operator < CompareEqual || operator > CompareNotBetween ||
		(wanted >= 0 && len(values) != wanted) ||
		(wanted == -1 && (len(values) == 0 || uint32(len(values)) > policy.QueryMaxInValues)) {
		return invalidQuery("predicate.values", "value_cardinality", "operator value cardinality is invalid")
	}
	if (operator == CompareContains || operator == CompareStartsWith || operator == CompareEndsWith) && kind != ValueString {
		return invalidQuery("predicate.operator", "operator_type", "text operator requires a string field")
	}
	for _, value := range values {
		if value.Kind != kind || (kind == ValueString && (value.String == "" || uint32(len(value.String)) > policy.QueryMaxStringBytes)) ||
			(kind == ValueTimestamp && value.Time.IsZero()) {
			return invalidQuery("predicate.values", "value_type", "predicate value type is invalid")
		}
	}
	if (operator == CompareBetween || operator == CompareNotBetween) && !orderedRange(values) {
		return invalidQuery("predicate.values", "range_order", "range lower bound exceeds upper bound")
	}
	return nil
}

func operatorAllowed(kind ValueKind, operator Comparison) bool {
	switch kind {
	case ValueString:
		return operator == CompareEqual || operator == CompareNotEqual ||
			operator == CompareIn || operator == CompareNotIn ||
			operator == CompareContains || operator == CompareStartsWith || operator == CompareEndsWith ||
			operator == CompareIsNull || operator == CompareIsNotNull
	case ValueInteger, ValueTimestamp:
		return operator == CompareEqual || operator == CompareNotEqual ||
			operator == CompareGreaterThan || operator == CompareGreaterThanOrEqual ||
			operator == CompareLessThan || operator == CompareLessThanOrEqual ||
			operator == CompareIn || operator == CompareNotIn ||
			operator == CompareBetween || operator == CompareNotBetween ||
			operator == CompareIsNull || operator == CompareIsNotNull
	case ValueBoolean:
		return operator == CompareEqual || operator == CompareNotEqual ||
			operator == CompareIn || operator == CompareNotIn ||
			operator == CompareIsNull || operator == CompareIsNotNull
	default:
		return false
	}
}

func orderedRange(values []QueryValue) bool {
	if len(values) != 2 || values[0].Kind != values[1].Kind {
		return false
	}
	switch values[0].Kind {
	case ValueInteger:
		return values[0].Integer <= values[1].Integer
	case ValueTimestamp:
		return !values[0].Time.After(values[1].Time)
	default:
		return false
	}
}

func validProcessField(field ProcessField) bool {
	return field >= ProcessInstanceID && field <= ProcessIsDeleted
}

func processFieldKind(field ProcessField) ValueKind {
	switch field {
	case ProcessLastEventSequence, ProcessDurationMS, ProcessVersion, ProcessEventCount:
		return ValueInteger
	case ProcessIsDeleted:
		return ValueBoolean
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
		value, valueErr := sortValue(sort, process)
		if valueErr != nil {
			return nil, valueErr
		}
		values = append(values, value)
	}
	return &QueryCursor{QueryHash: hash, Values: values}, nil
}

func sortValue(sort ProcessSort, process CompletedProcess) (QueryValue, error) {
	instance := process.Instance
	var value QueryValue
	switch sort.Field {
	case ProcessInstanceID:
		value = QueryValue{Kind: ValueString, String: instance.InstanceID}
	case ProcessWorkflowType:
		value = QueryValue{Kind: ValueString, String: instance.WorkflowType}
	case ProcessWorkflowVersion:
		value = QueryValue{Kind: ValueString, String: instance.WorkflowVersion}
	case ProcessStatus:
		value = QueryValue{Kind: ValueString, String: instance.Status}
	case ProcessActiveNodeID:
		value = QueryValue{Kind: ValueString, String: instance.ActiveNodeID}
	case ProcessConfigVersion:
		value = QueryValue{Kind: ValueString, String: instance.ConfigVersion}
	case ProcessPolicyVersion:
		value = QueryValue{Kind: ValueString, String: instance.PolicyVersion}
	case ProcessInitiatorActorID:
		value = QueryValue{Kind: ValueString, String: instance.InitiatorActorID}
	case ProcessLastEventSequence:
		value = QueryValue{Kind: ValueInteger, Integer: int64(instance.LastEventSequence)}
	case ProcessVersion:
		value = QueryValue{Kind: ValueInteger, Integer: int64(instance.Version)}
	case ProcessDurationMS:
		value = QueryValue{Kind: ValueInteger, Integer: int64(process.DurationMS)}
	case ProcessEventCount:
		value = QueryValue{Kind: ValueInteger, Integer: int64(process.EventCount)}
	case ProcessIsDeleted:
		value = QueryValue{Kind: ValueBoolean, Boolean: instance.IsDeleted}
	case ProcessStartedAt:
		value = QueryValue{Kind: ValueTimestamp, Time: instance.StartedAt}
	case ProcessUpdatedAt:
		value = QueryValue{Kind: ValueTimestamp, Time: instance.UpdatedAt}
	case ProcessCompletedAt:
		if instance.CompletedAt == nil {
			if sort.Coalesce == nil {
				return QueryValue{}, fmt.Errorf("%w: completed timestamp is absent", ErrInvalidQuery)
			}
			value = *sort.Coalesce
			break
		}
		value = QueryValue{Kind: ValueTimestamp, Time: *instance.CompletedAt}
	default:
		return QueryValue{}, fmt.Errorf("%w: sort field is unsupported", ErrInvalidQuery)
	}
	value.String = applyTextTransform(value.String, sort.TextTransform)
	if value.Kind == ValueInteger && sort.NumericTransform == NumericTransformAbs {
		if value.Integer == math.MinInt64 {
			return QueryValue{}, fmt.Errorf("%w: absolute numeric sort overflows", ErrInvalidQuery)
		}
		value.Integer = int64Abs(value.Integer)
	}
	if value.Kind == ValueTimestamp {
		value.Time = applyDateTransform(value.Time, sort.DateTransform)
	}
	return value, nil
}

func int64Abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func applyDateTransform(value time.Time, transform DateTransform) time.Time {
	value = value.UTC()
	switch transform {
	case DateTransformUTCDay:
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	case DateTransformUTCWeek:
		day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
		daysSinceMonday := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -daysSinceMonday)
	case DateTransformUTCMonth:
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	case DateTransformUTCYear:
		return time.Date(value.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return value
	}
}

func applyTextTransform(value string, transform TextTransform) string {
	switch transform {
	case TextTransformTrim:
		return strings.TrimSpace(value)
	case TextTransformLower:
		return strings.ToLower(value)
	case TextTransformUpper:
		return strings.ToUpper(value)
	case TextTransformTrimLower:
		return strings.ToLower(strings.TrimSpace(value))
	case TextTransformTrimUpper:
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return value
	}
}
