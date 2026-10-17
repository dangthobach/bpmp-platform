package application

import (
	"context"
	"math"
	"testing"
	"time"
)

type queryStore struct{ received ProcessQuery }

type queryAuthorizer struct{ authorization QueryAuthorization }

func (a queryAuthorizer) AuthorizeQuery(context.Context, QueryAuthorizationRequest) (QueryAuthorization, error) {
	return a.authorization, nil
}

func (*queryStore) Apply(context.Context, KafkaPosition, InstanceEvent) (bool, error) {
	return false, nil
}
func (*queryStore) Get(context.Context, string, string) (Instance, error) { return Instance{}, nil }
func (*queryStore) List(context.Context, ListFilter) ([]Instance, error)  { return nil, nil }
func (s *queryStore) QueryCompleted(_ context.Context, query ProcessQuery) ([]CompletedProcess, error) {
	s.received = query
	return []CompletedProcess{{}, {}, {}}, nil
}
func (s *queryStore) QueryInstances(ctx context.Context, query ProcessQuery) ([]CompletedProcess, error) {
	return s.QueryCompleted(ctx, query)
}

func completedQueryPolicy() RuntimePolicy {
	return RuntimePolicy{
		QueryDefaultPageSize: 2, QueryMaxPageSize: 100,
		QueryMaxFilterDepth: 8, QueryMaxPredicates: 10,
		QueryMaxSortFields: 4, QueryMaxRelationJoins: 2,
		QueryMaxInValues: 10, QueryTimeout: time.Second,
		QueryMaxStringBytes: 128,
		ConfigVersion:       "config-v1", PolicyVersion: "policy-v1",
		QueryCursorSigningKeyID: "cursor-v1",
		QueryAllCapability:      "workflow.query.all",
		QueryOwnCapability:      "workflow.query.own",
	}
}

func TestQueryRejectsOutOfRangeWireEnums(t *testing.T) {
	store := &queryStore{}
	service := newQueryService(t, store, completedQueryPolicy(), QueryAuthorization{SubjectID: "actor-1", PolicyVersion: "policy-v1"})
	for _, field := range []ProcessField{257, -255} {
		_, _, _, err := service.QueryCompleted(context.Background(), ProcessQuery{
			TenantID: "tenant-a",
			Filter: &ProcessExpression{Predicate: &ProcessPredicate{
				Field: field, Operator: CompareEqual,
				Values: []QueryValue{{Kind: ValueString, String: "i1"}},
			}},
		})
		if err == nil {
			t.Fatalf("unknown protobuf field %d was accepted", field)
		}
	}
}

func newQueryService(t *testing.T, store Store, policy RuntimePolicy, authorization QueryAuthorization) *Service {
	t.Helper()
	service, err := NewService(store, fakePolicy{value: policy}, WithQueryAuthorizer(queryAuthorizer{authorization: authorization}))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestQueryCompletedNormalizesStableSortAndAppliesLookahead(t *testing.T) {
	store := &queryStore{}
	service := newQueryService(t, store, completedQueryPolicy(), QueryAuthorization{SubjectID: "actor-1", PolicyVersion: "policy-v1"})
	filter := ProcessExpression{EventRelation: &EventRelation{
		Quantifier: RelationExists,
		Expression: EventExpression{Predicate: &EventPredicate{
			Field: EventType, Operator: CompareEqual,
			Values: []QueryValue{{Kind: ValueString, String: "user_task_completed"}},
		}},
	}}
	processes, limit, normalized, err := service.QueryCompleted(context.Background(), ProcessQuery{
		TenantID: "tenant-a", Filter: &filter,
		Sort: []ProcessSort{{Field: ProcessCompletedAt, Direction: SortDescending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 3 || limit != 2 || store.received.Limit != 3 {
		t.Fatalf("query lookahead was not applied: limit=%d query=%+v", limit, store.received)
	}
	if len(normalized.Sort) != 2 || normalized.Sort[1].Field != ProcessInstanceID {
		t.Fatalf("stable sort was not appended: %+v", normalized.Sort)
	}
}

func TestQueryValidationCountsStableSortAndRequiresValidPagePolicy(t *testing.T) {
	store := &queryStore{}
	policy := completedQueryPolicy()
	policy.QueryMaxSortFields = 1
	service := newQueryService(t, store, policy, QueryAuthorization{SubjectID: "actor-1", PolicyVersion: "policy-v1"})
	_, _, _, err := service.QueryCompleted(context.Background(), ProcessQuery{
		TenantID: "tenant-a",
		Sort:     []ProcessSort{{Field: ProcessCompletedAt, Direction: SortDescending}},
	})
	if err == nil {
		t.Fatal("implicit stable sort must count against the sort budget")
	}

	for _, mutate := range []func(*RuntimePolicy){
		func(value *RuntimePolicy) { value.QueryDefaultPageSize = 0 },
		func(value *RuntimePolicy) { value.QueryMaxPageSize = 0 },
		func(value *RuntimePolicy) { value.QueryDefaultPageSize = value.QueryMaxPageSize + 1 },
	} {
		invalid := completedQueryPolicy()
		mutate(&invalid)
		service = newQueryService(t, store, invalid, QueryAuthorization{SubjectID: "actor-1", PolicyVersion: "policy-v1"})
		if _, _, _, err = service.QueryCompleted(context.Background(), ProcessQuery{TenantID: "tenant-a"}); err == nil {
			t.Fatalf("invalid page policy was accepted: %+v", invalid)
		}
	}
}

func TestQueryCompletedRejectsBudgetAndForeignCursor(t *testing.T) {
	store := &queryStore{}
	policy := completedQueryPolicy()
	policy.QueryMaxPredicates = 1
	service := newQueryService(t, store, policy, QueryAuthorization{SubjectID: "actor-1", PolicyVersion: "policy-v1"})
	filter := ProcessExpression{Logical: LogicalAnd, Children: []ProcessExpression{
		{Predicate: &ProcessPredicate{Field: ProcessWorkflowType, Operator: CompareEqual, Values: []QueryValue{{Kind: ValueString, String: "order"}}}},
		{Predicate: &ProcessPredicate{Field: ProcessWorkflowVersion, Operator: CompareEqual, Values: []QueryValue{{Kind: ValueString, String: "1"}}}},
	}}
	if _, _, _, err := service.QueryCompleted(context.Background(), ProcessQuery{TenantID: "tenant-a", Filter: &filter}); err == nil {
		t.Fatal("predicate budget must be enforced")
	}
	policy.QueryMaxPredicates = 10
	service = newQueryService(t, store, policy, QueryAuthorization{SubjectID: "actor-1", PolicyVersion: "policy-v1"})
	query := ProcessQuery{TenantID: "tenant-a", After: &QueryCursor{
		QueryHash: "another-query", Values: []QueryValue{
			{Kind: ValueTimestamp, Time: time.UnixMilli(1000)},
			{Kind: ValueString, String: "instance-1"},
		},
	}}
	if _, _, _, err := service.QueryCompleted(context.Background(), query); err == nil {
		t.Fatal("cursor from another query must be rejected")
	}
}

func TestQueryCompletedIntersectsAuthorizationAndClientFilters(t *testing.T) {
	store := &queryStore{}
	authorizationFilter := &ProcessExpression{Predicate: &ProcessPredicate{
		Field: ProcessInitiatorActorID, Operator: CompareEqual,
		Values: []QueryValue{{Kind: ValueString, String: "actor-1"}},
	}}
	service := newQueryService(t, store, completedQueryPolicy(), QueryAuthorization{
		SubjectID: "actor-1", PolicyVersion: "policy-v1", Filter: authorizationFilter,
	})
	clientFilter := &ProcessExpression{Predicate: &ProcessPredicate{
		Field: ProcessInitiatorActorID, Operator: CompareNotEqual,
		Values: []QueryValue{{Kind: ValueString, String: "actor-1"}},
	}}
	_, _, normalized, err := service.QueryCompleted(context.Background(), ProcessQuery{
		TenantID: "tenant-a", Filter: clientFilter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Filter == nil || normalized.Filter.Logical != LogicalAnd || len(normalized.Filter.Children) != 2 {
		t.Fatalf("authorization filter was not intersected: %+v", normalized.Filter)
	}
	if normalized.AuthorizationSubject != "actor-1" || normalized.AuthorizationPolicyVersion != "policy-v1" {
		t.Fatalf("authorization binding is absent: %+v", normalized)
	}
}

func TestQueryValidationEnforcesFieldOperatorMatrix(t *testing.T) {
	policy := completedQueryPolicy()
	cases := []ProcessPredicate{
		{Field: ProcessDurationMS, Operator: CompareContains, Values: []QueryValue{{Kind: ValueInteger, Integer: 1}}},
		{Field: ProcessWorkflowType, Operator: CompareGreaterThan, Values: []QueryValue{{Kind: ValueString, String: "approval"}}},
		{Field: ProcessCompletedAt, Operator: CompareEqual, Values: []QueryValue{{Kind: ValueString, String: "not-a-time"}}},
	}
	for _, predicate := range cases {
		if err := validatePredicate(predicate.Field, predicate.Operator, predicate.Values, policy); err == nil {
			t.Fatalf("incompatible predicate was accepted: %+v", predicate)
		}
	}
	if err := validatePredicate(ProcessDurationMS, CompareGreaterThan,
		[]QueryValue{{Kind: ValueInteger, Integer: 1}}, policy); err != nil {
		t.Fatalf("compatible numeric predicate was rejected: %v", err)
	}
}

func TestQueryValidationAndCursorApplyTypedTransforms(t *testing.T) {
	policy := completedQueryPolicy()
	if err := validateProcessPredicate(ProcessPredicate{
		Field: ProcessDurationMS, Operator: CompareEqual,
		Values:        []QueryValue{{Kind: ValueInteger, Integer: 10}},
		TextTransform: TextTransformLower,
	}, policy); err == nil {
		t.Fatal("text transform on integer field was accepted")
	}
	if err := validateProcessPredicate(ProcessPredicate{
		Field: ProcessWorkflowType, Operator: CompareEqual,
		Values:   []QueryValue{{Kind: ValueString, String: "approval"}},
		Coalesce: &QueryValue{Kind: ValueInteger, Integer: 1},
	}, policy); err == nil {
		t.Fatal("coalesce value with a mismatched type was accepted")
	}

	completed := time.UnixMilli(2_000).UTC()
	query := ProcessQuery{
		TenantID: "tenant-a", AuthorizationSubject: "actor-1", AuthorizationPolicyVersion: "p1",
		Sort: []ProcessSort{
			{Field: ProcessWorkflowType, Direction: SortAscending, TextTransform: TextTransformTrimLower},
			{Field: ProcessInstanceID, Direction: SortAscending},
		},
	}
	cursor, err := CursorFor(query, CompletedProcess{Instance: Instance{
		InstanceID: "i1", WorkflowType: "  Approval ", CompletedAt: &completed,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Values[0].String != "approval" {
		t.Fatalf("cursor did not use transformed sort value: %+v", cursor.Values)
	}
}

func TestRangeAndTypedValueTransforms(t *testing.T) {
	policy := completedQueryPolicy()
	if err := validateProcessPredicate(ProcessPredicate{
		Field: ProcessDurationMS, Operator: CompareBetween,
		Values: []QueryValue{{Kind: ValueInteger, Integer: 20}, {Kind: ValueInteger, Integer: 10}},
	}, policy); err == nil {
		t.Fatal("reversed numeric range was accepted")
	}
	if err := validateProcessPredicate(ProcessPredicate{
		Field: ProcessCompletedAt, Operator: CompareBetween,
		Values:        []QueryValue{{Kind: ValueTimestamp, Time: time.Unix(10, 0)}, {Kind: ValueTimestamp, Time: time.Unix(20, 0)}},
		DateTransform: DateTransformUTCDay,
	}, policy); err != nil {
		t.Fatalf("valid date range was rejected: %v", err)
	}

	predicate := ProcessPredicate{
		Field: ProcessDurationMS, Operator: CompareBetween,
		Values:           []QueryValue{{Kind: ValueInteger, Integer: -20}, {Kind: ValueInteger, Integer: 30}},
		NumericTransform: NumericTransformAbs,
	}
	if err := normalizeProcessPredicate(&predicate); err != nil {
		t.Fatal(err)
	}
	if predicate.Values[0].Integer != 20 || predicate.Values[1].Integer != 30 {
		t.Fatalf("numeric values were not normalized: %+v", predicate.Values)
	}

	date := time.Date(2026, time.October, 17, 23, 42, 1, 0, time.FixedZone("UTC+7", 7*60*60))
	datePredicate := ProcessPredicate{
		Field: ProcessCompletedAt, Operator: CompareEqual,
		Values:        []QueryValue{{Kind: ValueTimestamp, Time: date}},
		DateTransform: DateTransformUTCDay,
	}
	if err := normalizeProcessPredicate(&datePredicate); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.October, 17, 0, 0, 0, 0, time.UTC)
	if !datePredicate.Values[0].Time.Equal(want) {
		t.Fatalf("date was not bucketed in UTC: got %s want %s", datePredicate.Values[0].Time, want)
	}

	overflow := ProcessPredicate{
		Field: ProcessDurationMS, Operator: CompareEqual,
		Values:           []QueryValue{{Kind: ValueInteger, Integer: math.MinInt64}},
		NumericTransform: NumericTransformAbs,
	}
	if err := normalizeProcessPredicate(&overflow); err == nil {
		t.Fatal("ABS overflow was accepted")
	}
}

func TestQueryFingerprintBindsAuthorizationSubjectAndPolicy(t *testing.T) {
	base := ProcessQuery{
		TenantID: "tenant-a", AuthorizationSubject: "actor-1",
		AuthorizationPolicyVersion: "policy-v1",
		Sort:                       []ProcessSort{{Field: ProcessInstanceID, Direction: SortAscending}},
	}
	first, err := base.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	base.AuthorizationSubject = "actor-2"
	second, err := base.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	base.AuthorizationSubject = "actor-1"
	base.AuthorizationPolicyVersion = "policy-v2"
	third, err := base.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first == third || second == third {
		t.Fatal("query fingerprint is not bound to authorization identity and policy")
	}
}
