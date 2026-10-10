package application

import (
	"context"
	"testing"
	"time"
)

type queryStore struct{ received ProcessQuery }

func (*queryStore) Apply(context.Context, KafkaPosition, InstanceEvent) (bool, error) {
	return false, nil
}
func (*queryStore) Get(context.Context, string, string) (Instance, error) { return Instance{}, nil }
func (*queryStore) List(context.Context, ListFilter) ([]Instance, error)  { return nil, nil }
func (s *queryStore) QueryCompleted(_ context.Context, query ProcessQuery) ([]CompletedProcess, error) {
	s.received = query
	return []CompletedProcess{{}, {}, {}}, nil
}

func completedQueryPolicy() RuntimePolicy {
	return RuntimePolicy{
		QueryDefaultPageSize: 2, QueryMaxPageSize: 100,
		QueryMaxFilterDepth: 8, QueryMaxPredicates: 10,
		QueryMaxSortFields: 4, QueryMaxRelationJoins: 2,
		QueryMaxInValues: 10, QueryTimeout: time.Second,
		QueryMaxStringBytes: 128,
	}
}

func TestQueryCompletedNormalizesStableSortAndAppliesLookahead(t *testing.T) {
	store := &queryStore{}
	service, err := NewService(store, fakePolicy{value: completedQueryPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	filter := ProcessExpression{EventRelation: &EventRelation{
		MustExist: true,
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
	service, err := NewService(store, fakePolicy{value: policy})
	if err != nil {
		t.Fatal(err)
	}
	filter := ProcessExpression{Logical: LogicalAnd, Children: []ProcessExpression{
		{Predicate: &ProcessPredicate{Field: ProcessWorkflowType, Operator: CompareEqual, Values: []QueryValue{{Kind: ValueString, String: "order"}}}},
		{Predicate: &ProcessPredicate{Field: ProcessWorkflowVersion, Operator: CompareEqual, Values: []QueryValue{{Kind: ValueString, String: "1"}}}},
	}}
	if _, _, _, err = service.QueryCompleted(context.Background(), ProcessQuery{TenantID: "tenant-a", Filter: &filter}); err == nil {
		t.Fatal("predicate budget must be enforced")
	}
	policy.QueryMaxPredicates = 10
	service, err = NewService(store, fakePolicy{value: policy})
	if err != nil {
		t.Fatal(err)
	}
	query := ProcessQuery{TenantID: "tenant-a", After: &QueryCursor{
		QueryHash: "another-query", Values: []QueryValue{
			{Kind: ValueTimestamp, Time: time.UnixMilli(1000)},
			{Kind: ValueString, String: "instance-1"},
		},
	}}
	if _, _, _, err = service.QueryCompleted(context.Background(), query); err == nil {
		t.Fatal("cursor from another query must be rejected")
	}
}
