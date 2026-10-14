package kafkaconfig

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestFetchRecoveryClassification(t *testing.T) {
	for _, err := range []error{kerr.UnknownMemberID, kerr.IllegalGeneration, kerr.RebalanceInProgress, kerr.NotCoordinator, &net.DNSError{IsTimeout: true}, &kgo.ErrGroupSession{Err: kerr.UnknownMemberID}} {
		if got := CheckFetchErrors(context.Background(), []kgo.FetchError{{Err: fmt.Errorf("fetch: %w", err)}}); got != nil {
			t.Fatalf("recoverable error rejected: %v", got)
		}
	}
	for _, err := range []error{kerr.TopicAuthorizationFailed, kerr.GroupAuthorizationFailed, kerr.InvalidTopicException, errors.New("unexpected")} {
		if got := CheckFetchErrors(context.Background(), []kgo.FetchError{{Err: err}}); !errors.Is(got, err) {
			t.Fatalf("fatal error ignored: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckFetchErrors(ctx, []kgo.FetchError{{Err: kerr.UnknownMemberID}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type flakyCommitter struct {
	failures []error
	calls    int
}

func (c *flakyCommitter) CommitRecords(context.Context, ...*kgo.Record) error {
	c.calls++
	if len(c.failures) == 0 {
		return nil
	}
	err := c.failures[0]
	c.failures = c.failures[1:]
	return err
}
func TestOffsetCommitRecoveryIsBounded(t *testing.T) {
	client := &flakyCommitter{failures: []error{kerr.UnknownMemberID, kerr.RebalanceInProgress}}
	if err := RetryOffsetCommit(context.Background(), client, 3, time.Millisecond, time.Second); err != nil || client.calls != 3 {
		t.Fatalf("rejoin failed: %v calls=%d", err, client.calls)
	}
	client = &flakyCommitter{failures: []error{kerr.UnknownMemberID, kerr.UnknownMemberID, kerr.UnknownMemberID}}
	if err := RetryOffsetCommit(context.Background(), client, 2, time.Millisecond, time.Second); err == nil || client.calls != 2 {
		t.Fatal("retry bound ignored")
	}
	client = &flakyCommitter{failures: []error{kerr.GroupAuthorizationFailed}}
	if err := RetryOffsetCommit(context.Background(), client, 3, time.Millisecond, time.Second); !errors.Is(err, kerr.GroupAuthorizationFailed) || client.calls != 1 {
		t.Fatal("fatal commit retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client = &flakyCommitter{failures: []error{kerr.UnknownMemberID}}
	if err := RetryOffsetCommit(ctx, client, 3, time.Hour, time.Second); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("unbounded backoff accepted")
	}
	if err := RetryOffsetCommit(ctx, client, 3, time.Millisecond, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
