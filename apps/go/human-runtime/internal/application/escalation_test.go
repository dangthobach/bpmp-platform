package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"testing/quick"
	"time"
)

type escalationMemory struct {
	claims         []Escalation
	acked, retried int
	retryAt        time.Time
}

func (m *escalationMemory) ClaimDueEscalations(context.Context, time.Time, time.Time, string, int) ([]Escalation, error) {
	return m.claims, nil
}
func (m *escalationMemory) AckEscalation(context.Context, Escalation, string, time.Time) error {
	m.acked++
	return nil
}
func (m *escalationMemory) RetryEscalation(_ context.Context, _ Escalation, _ string, at time.Time) error {
	m.retried++
	m.retryAt = at
	return nil
}

type failingPublisher struct{ fail bool }

func (p failingPublisher) PublishEscalation(context.Context, Escalation) error {
	if p.fail {
		return errors.New("unavailable")
	}
	return nil
}

func TestEscalationRetriesWithoutAcknowledgingFailedPublish(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	store := &escalationMemory{claims: []Escalation{{TenantID: "t", EscalationID: "e"}}}
	worker, _ := NewEscalationWorker(store, failingPublisher{fail: true}, "worker-1", 10, time.Minute, time.Second)
	published, err := worker.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if published != 0 || store.acked != 0 || store.retried != 1 || !store.retryAt.Equal(now.Add(time.Second)) {
		t.Fatalf("invalid retry state: %#v", store)
	}
}

type dueEscalation struct {
	value    Escalation
	deadline time.Time
}

type dueEscalationStore struct {
	values []dueEscalation
	acked  []string
}

func (s *dueEscalationStore) ClaimDueEscalations(_ context.Context, now, _ time.Time, _ string, limit int) ([]Escalation, error) {
	result := make([]Escalation, 0, limit)
	for _, candidate := range s.values {
		if !candidate.deadline.After(now) && len(result) < limit {
			result = append(result, candidate.value)
		}
	}
	return result, nil
}

func (s *dueEscalationStore) AckEscalation(_ context.Context, value Escalation, _ string, _ time.Time) error {
	s.acked = append(s.acked, value.EscalationID)
	return nil
}

func (*dueEscalationStore) RetryEscalation(context.Context, Escalation, string, time.Time) error {
	return nil
}

type recordingEscalationPublisher struct{ published []string }

func (p *recordingEscalationPublisher) PublishEscalation(_ context.Context, value Escalation) error {
	p.published = append(p.published, value.EscalationID)
	return nil
}

func TestProperty5OnlyDueSLAsTriggerTheirEscalation(t *testing.T) {
	property := func(rawCount, rawDue uint8) bool {
		count := int(rawCount%24) + 1
		dueCount := int(rawDue % uint8(count+1))
		now := time.Unix(10_000, 0).UTC()
		store := &dueEscalationStore{}
		expected := make([]string, 0, dueCount)
		for index := range count {
			id := fmt.Sprintf("escalation-%d", index)
			deadline := now.Add(time.Duration(index-dueCount+1) * time.Second)
			if index < dueCount {
				deadline = now.Add(-time.Duration(index) * time.Second)
				expected = append(expected, id)
			}
			store.values = append(store.values, dueEscalation{
				value: Escalation{
					TenantID:     "tenant-a",
					EscalationID: id,
					WorkItemID:   fmt.Sprintf("work-%d", index),
					PolicyRef:    "manager",
					Payload:      []byte(`{"policy":"manager"}`),
				},
				deadline: deadline,
			})
		}
		publisher := &recordingEscalationPublisher{}
		worker, err := NewEscalationWorker(store, publisher, "worker-1", count, time.Minute, time.Second)
		if err != nil {
			return false
		}
		published, err := worker.RunOnce(context.Background(), now)
		if err != nil || published != dueCount {
			return false
		}
		sort.Strings(expected)
		sort.Strings(store.acked)
		sort.Strings(publisher.published)
		return len(expected) == len(store.acked) &&
			fmt.Sprint(expected) == fmt.Sprint(store.acked) &&
			fmt.Sprint(expected) == fmt.Sprint(publisher.published)
	}

	// Feature: rust-bpm-platform, Property 5: overdue SLA emits exactly its escalation
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
