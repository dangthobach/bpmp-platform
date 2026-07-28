package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUpstreamRetriesTransientFailureWithinDynamicPolicy(t *testing.T) {
	controller := newUpstreamController()
	var calls atomic.Uint32
	result, err := doUpstream(
		context.Background(),
		controller,
		"tenant-a",
		engineDependency,
		upstreamTestPolicy(),
		func(context.Context) (string, error) {
			if calls.Add(1) < 3 {
				return "", status.Error(codes.Unavailable, "transient")
			}
			return "committed", nil
		},
	)
	if err != nil || result != "committed" || calls.Load() != 3 {
		t.Fatalf("dynamic retry policy was not applied: result=%q calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestUpstreamCircuitUsesDynamicThresholdAndOpenDuration(t *testing.T) {
	controller := newUpstreamController()
	now := time.Unix(100, 0)
	controller.now = func() time.Time { return now }
	policy := upstreamTestPolicy()
	policy.UpstreamRetry.MaxAttempts = 1
	policy.CircuitBreakerFailureThreshold = 2
	for range 2 {
		_, _ = doUpstream(
			context.Background(), controller, "tenant-a", engineDependency, policy,
			func(context.Context) (struct{}, error) {
				return struct{}{}, status.Error(codes.Unavailable, "down")
			},
		)
	}
	if _, err := doUpstream(
		context.Background(), controller, "tenant-a", engineDependency, policy,
		func(context.Context) (struct{}, error) { return struct{}{}, nil },
	); !errors.Is(err, errCircuitOpen) {
		t.Fatalf("expected open circuit, got %v", err)
	}
	now = now.Add(policy.CircuitBreakerOpen)
	if _, err := doUpstream(
		context.Background(), controller, "tenant-a", engineDependency, policy,
		func(context.Context) (struct{}, error) { return struct{}{}, nil },
	); err != nil {
		t.Fatalf("half-open probe did not recover circuit: %v", err)
	}
}

func TestUpstreamBulkheadAppliesChangedConcurrencyWithoutReplacingState(t *testing.T) {
	controller := newUpstreamController()
	policy := upstreamTestPolicy()
	policy.UpstreamRetry.MaxAttempts = 1
	policy.BulkheadMaxConcurrency = 1
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := doUpstream(
			context.Background(), controller, "tenant-a", humanDependency, policy,
			func(context.Context) (struct{}, error) {
				close(started)
				<-release
				return struct{}{}, nil
			},
		)
		done <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := doUpstream(
		ctx, controller, "tenant-a", humanDependency, policy,
		func(context.Context) (struct{}, error) { return struct{}{}, nil },
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bulkhead did not bound concurrent calls: %v", err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func upstreamTestPolicy() RuntimePolicy {
	return RuntimePolicy{
		UpstreamTimeout:                500 * time.Millisecond,
		CircuitBreakerFailureThreshold: 3,
		CircuitBreakerOpen:             time.Second,
		BulkheadMaxConcurrency:         4,
		UpstreamRetry: RetryPolicy{
			MaxAttempts:      3,
			InitialBackoff:   time.Millisecond,
			MaxBackoff:       5 * time.Millisecond,
			MultiplierMillis: 2000,
		},
	}
}
