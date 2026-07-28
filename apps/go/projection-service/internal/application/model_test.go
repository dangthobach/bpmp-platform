package application

import (
	"context"
	"testing"
	"time"
)

type fakeStore struct {
	filter ListFilter
}

func (*fakeStore) Apply(context.Context, KafkaPosition, InstanceEvent) (bool, error) {
	return false, nil
}

func (*fakeStore) Get(context.Context, string, string) (Instance, error) {
	return Instance{}, nil
}

func (s *fakeStore) List(_ context.Context, filter ListFilter) ([]Instance, error) {
	s.filter = filter
	return []Instance{{InstanceID: "one"}, {InstanceID: "two"}, {InstanceID: "three"}}, nil
}

type fakePolicy struct{ value RuntimePolicy }

func (p fakePolicy) Policy(string) (RuntimePolicy, error) { return p.value, nil }

func TestListAppliesTenantPolicyAndFetchesLookahead(t *testing.T) {
	store := &fakeStore{}
	service, err := NewService(store, fakePolicy{value: RuntimePolicy{
		ConsumeBatchSize: 10, QueryDefaultPageSize: 2, QueryMaxPageSize: 5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	instances, limit, err := service.List(context.Background(), ListFilter{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if limit != 2 || store.filter.Limit != 3 || len(instances) != 3 {
		t.Fatalf("default page lookahead was not applied: limit=%d filter=%+v", limit, store.filter)
	}
	if _, _, err = service.List(context.Background(), ListFilter{TenantID: "tenant-a", Limit: 6}); err == nil {
		t.Fatal("page size above dynamic policy must be rejected")
	}
}

func TestEventValidationRejectsIncompleteDurableMetadata(t *testing.T) {
	event := InstanceEvent{
		EventID: "event-1", TenantID: "tenant-a", InstanceID: "instance-1",
		WorkflowType: "approval", WorkflowVersion: "1", Sequence: 1,
		OccurredAt: time.UnixMilli(1), ConfigVersion: "config-v1",
		PolicyVersion: "policy-v1", Kind: EventStarted,
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.ConfigVersion = ""
	if err := event.Validate(); err == nil {
		t.Fatal("missing replay configuration metadata must be rejected")
	}
}
