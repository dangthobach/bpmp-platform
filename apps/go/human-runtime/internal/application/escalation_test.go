package application

import (
	"context"
	"errors"
	"testing"
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
