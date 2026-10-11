package application

import (
	"context"
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
