package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type outboxRepoStub struct {
	event      OutboxEvent
	claimed    bool
	published  bool
	retried    bool
	retryDelay time.Duration
}

func (s *outboxRepoStub) ClaimEvent(_ context.Context, _ time.Time, _ time.Duration, token uuid.UUID) (OutboxEvent, bool, error) {
	if s.claimed {
		return OutboxEvent{}, false, nil
	}
	s.claimed = true
	s.event.LeaseToken = token
	s.event.Attempts++
	return s.event, true, nil
}
func (s *outboxRepoStub) MarkPublished(_ context.Context, event OutboxEvent, _ time.Time) error {
	if event.EventID != s.event.EventID {
		return ErrInvalid
	}
	s.published = true
	return nil
}
func (s *outboxRepoStub) RetryEvent(_ context.Context, event OutboxEvent, now, next time.Time) error {
	if event.EventID != s.event.EventID {
		return ErrInvalid
	}
	s.retried, s.retryDelay = true, next.Sub(now)
	return nil
}

type publisherStub struct {
	got OutboxEvent
	err error
}

func (p *publisherStub) Publish(_ context.Context, event OutboxEvent) error {
	p.got = event
	return p.err
}

func TestOutboxRunnerPublishesAndRetriesSameEvent(t *testing.T) {
	for _, fail := range []bool{false, true} {
		repo := &outboxRepoStub{event: OutboxEvent{EventID: uuid.New(), TenantID: uuid.New(), AggregateID: uuid.New(), AggregateVersion: 1}}
		publisher := &publisherStub{}
		if fail {
			publisher.err = errors.New("broker unavailable")
		}
		runner, err := NewOutboxRunner(repo, publisher, time.Now, uuid.New)
		if err != nil {
			t.Fatal(err)
		}
		found, err := runner.RunOnce(context.Background())
		if err != nil || !found || publisher.got.EventID != repo.event.EventID {
			t.Fatalf("publish outcome fail=%v found=%v err=%v", fail, found, err)
		}
		if repo.published == fail || repo.retried != fail {
			t.Fatalf("incorrect durable outcome fail=%v published=%v retried=%v", fail, repo.published, repo.retried)
		}
		if fail && repo.retryDelay != time.Second {
			t.Fatalf("unexpected first backoff: %s", repo.retryDelay)
		}
	}
}
