package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type publicationRepositoryStub struct {
	records       []domain.Publication
	completed     bool
	failed        bool
	nextAttemptAt time.Time
}

func (r *publicationRepositoryStub) ClaimPublicationBatch(context.Context, string, int, time.Duration, time.Time) ([]domain.Publication, error) {
	return r.records, nil
}
func (r *publicationRepositoryStub) CompletePublicationBatch(context.Context, string, []domain.Publication, time.Time) error {
	r.completed = true
	return nil
}
func (r *publicationRepositoryStub) FailPublicationBatch(_ context.Context, _ string, _ []domain.Publication, next time.Time, _ string) error {
	r.failed = true
	r.nextAttemptAt = next
	return nil
}

type publicationSinkStub struct {
	failAt int
	calls  int
}

func (s *publicationSinkStub) Publish(context.Context, domain.Publication) error {
	s.calls++
	if s.calls == s.failAt {
		return errors.New("broker unavailable")
	}
	return nil
}

func TestPublisherCheckpointsOnlyAfterWholeBatchAcknowledged(t *testing.T) {
	t.Parallel()
	repository := &publicationRepositoryStub{records: []domain.Publication{
		{EventID: "one", EventSequence: 1, AttemptCount: 1},
		{EventID: "two", EventSequence: 2, AttemptCount: 1},
	}}
	sink := &publicationSinkStub{}
	publisher, err := NewPublisher(repository, sink, publisherTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	published, err := publisher.RunOnce(context.Background())
	if err != nil || published != 2 || !repository.completed || repository.failed {
		t.Fatalf("unexpected outcome: published=%d complete=%v failed=%v err=%v", published, repository.completed, repository.failed, err)
	}
}

func TestPublisherReleasesWholeBatchAfterPartialBrokerAcknowledgement(t *testing.T) {
	t.Parallel()
	repository := &publicationRepositoryStub{records: []domain.Publication{
		{EventID: "one", EventSequence: 1, AttemptCount: 2},
		{EventID: "two", EventSequence: 2, AttemptCount: 2},
	}}
	sink := &publicationSinkStub{failAt: 2}
	publisher, err := NewPublisher(repository, sink, publisherTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	publisher.now = func() time.Time { return now }
	published, err := publisher.RunOnce(context.Background())
	if err == nil || published != 0 || repository.completed || !repository.failed {
		t.Fatalf("unexpected outcome: published=%d complete=%v failed=%v err=%v", published, repository.completed, repository.failed, err)
	}
	if !repository.nextAttemptAt.Equal(now.Add(2 * time.Second)) {
		t.Fatalf("unexpected retry deadline: %s", repository.nextAttemptAt)
	}
}

func publisherTestConfig() PublisherConfig {
	return PublisherConfig{
		WorkerID: "publisher-1", BatchSize: 10, LeaseDuration: time.Minute,
		InitialRetryDelay: time.Second, MaxRetryDelay: time.Minute, RetryMultiplier: 2000,
	}
}
