package gateway

import (
	"context"
	"errors"
	"hash/fnv"
	"math/bits"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errCircuitOpen = errors.New("upstream circuit is open")

const (
	engineDependency        = "engine"
	humanDependency         = "human-runtime"
	configurationDependency = "configuration-service"
)

type upstreamController struct {
	mu     sync.Mutex
	states map[string]*upstreamState
	now    func() time.Time
}

type upstreamState struct {
	mu       sync.Mutex
	active   uint32
	failures uint32
	openedAt time.Time
	probe    bool
	changed  chan struct{}
}

func newUpstreamController() *upstreamController {
	return &upstreamController{
		states: make(map[string]*upstreamState),
		now:    time.Now,
	}
}

func (c *upstreamController) state(tenantID, dependency string) *upstreamState {
	key := tenantID + "\x00" + dependency
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.states[key]
	if state == nil {
		state = &upstreamState{changed: make(chan struct{})}
		c.states[key] = state
	}
	return state
}

func invokeUpstream[T any](
	h *Handler,
	rctx context.Context,
	scope requestScope,
	dependency string,
	operation func(context.Context) (T, error),
) (T, error) {
	return doUpstream(
		rctx,
		h.upstreams,
		scope.tenantID,
		dependency,
		scope.runtimePolicy,
		operation,
	)
}

func doUpstream[T any](
	ctx context.Context,
	controller *upstreamController,
	tenantID string,
	dependency string,
	policy RuntimePolicy,
	operation func(context.Context) (T, error),
) (T, error) {
	var zero T
	callCtx, cancel := context.WithTimeout(ctx, policy.UpstreamTimeout)
	defer cancel()
	state := controller.state(tenantID, dependency)
	if !state.allow(controller.now(), policy) {
		return zero, errCircuitOpen
	}
	if err := state.acquire(callCtx, policy.BulkheadMaxConcurrency); err != nil {
		state.releaseProbe()
		return zero, err
	}
	defer state.release()

	delay := policy.UpstreamRetry.InitialBackoff
	var lastErr error
	for attempt := uint32(1); attempt <= policy.UpstreamRetry.MaxAttempts; attempt++ {
		result, err := operation(callCtx)
		if err == nil {
			state.recordSuccess()
			return result, nil
		}
		lastErr = err
		if !retryableUpstreamError(err) || attempt == policy.UpstreamRetry.MaxAttempts {
			break
		}
		wait := jitteredDelay(delay, tenantID, dependency, attempt, controller.now())
		timer := time.NewTimer(wait)
		select {
		case <-callCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			lastErr = callCtx.Err()
			attempt = policy.UpstreamRetry.MaxAttempts
		case <-timer.C:
			delay = nextBackoff(delay, policy.UpstreamRetry)
		}
	}
	if retryableUpstreamError(lastErr) || errors.Is(lastErr, context.DeadlineExceeded) {
		state.recordFailure(controller.now(), policy.CircuitBreakerFailureThreshold)
	} else {
		state.recordSuccess()
	}
	return zero, lastErr
}

func (s *upstreamState) allow(now time.Time, policy RuntimePolicy) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openedAt.IsZero() {
		return true
	}
	if now.Sub(s.openedAt) < policy.CircuitBreakerOpen || s.probe {
		return false
	}
	s.probe = true
	return true
}

func (s *upstreamState) acquire(ctx context.Context, limit uint32) error {
	for {
		s.mu.Lock()
		if s.active < limit {
			s.active++
			s.mu.Unlock()
			return nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (s *upstreamState) release() {
	s.mu.Lock()
	s.active--
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *upstreamState) releaseProbe() {
	s.mu.Lock()
	s.probe = false
	s.mu.Unlock()
}

func (s *upstreamState) recordSuccess() {
	s.mu.Lock()
	s.failures = 0
	s.openedAt = time.Time{}
	s.probe = false
	s.mu.Unlock()
}

func (s *upstreamState) recordFailure(now time.Time, threshold uint32) {
	s.mu.Lock()
	s.probe = false
	s.failures++
	if s.failures >= threshold {
		s.openedAt = now
	}
	s.mu.Unlock()
}

func retryableUpstreamError(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Aborted:
		return true
	default:
		return false
	}
}

func nextBackoff(current time.Duration, policy RetryPolicy) time.Duration {
	high, low := bits.Mul64(uint64(current), uint64(policy.MultiplierMillis))
	if high >= 1000 {
		return policy.MaxBackoff
	}
	next, _ := bits.Div64(high, low, 1000)
	if next > uint64(policy.MaxBackoff) {
		return policy.MaxBackoff
	}
	return time.Duration(next)
}

func jitteredDelay(
	delay time.Duration,
	tenantID string,
	dependency string,
	attempt uint32,
	now time.Time,
) time.Duration {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(tenantID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(dependency))
	value := hash.Sum64() ^ uint64(now.UnixNano()) ^ uint64(attempt)
	// Full jitter in [50%, 100%] prevents synchronized retries while retaining a
	// strictly bounded delay.
	return delay/2 + time.Duration(value%uint64(delay/2+1))
}
