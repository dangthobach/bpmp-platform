package grpcclient

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrCircuitOpen = status.Error(codes.Unavailable, "upstream circuit is open")

type FallbackConfig struct {
	Timeout time.Duration
}

func (c FallbackConfig) Validate() error {
	if c.Timeout <= 0 {
		return errors.New("fallback timeout configuration is invalid")
	}
	return nil
}

// InvokeWithFallback bounds the primary call and invokes the configured
// fallback only after the primary fails or exceeds its deadline.
func InvokeWithFallback[T any](
	ctx context.Context,
	config FallbackConfig,
	primary func(context.Context) (T, error),
	fallback func(context.Context, error) (T, error),
) (T, error) {
	var zero T
	if err := config.Validate(); err != nil {
		return zero, err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	result, err := primary(attemptCtx)
	cancel()
	if err == nil {
		return result, nil
	}
	return fallback(ctx, err)
}

type Config struct {
	MaxAttempts      uint32
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	MultiplierMillis uint32
	AttemptTimeout   time.Duration
	FailureThreshold uint32
	OpenDuration     time.Duration
	RetryableCodes   map[codes.Code]struct{}
}

func RetryableCodes(names []string) (map[codes.Code]struct{}, error) {
	result := make(map[codes.Code]struct{}, len(names))
	for _, name := range names {
		var code codes.Code
		switch name {
		case "UNAVAILABLE":
			code = codes.Unavailable
		case "RESOURCE_EXHAUSTED":
			code = codes.ResourceExhausted
		case "DEADLINE_EXCEEDED":
			code = codes.DeadlineExceeded
		case "ABORTED":
			code = codes.Aborted
		default:
			return nil, errors.New("unsupported retryable gRPC status code")
		}
		result[code] = struct{}{}
	}
	if len(result) == 0 {
		return nil, errors.New("retryable gRPC status codes are required")
	}
	return result, nil
}

func (c Config) Validate() error {
	if c.MaxAttempts == 0 ||
		c.InitialBackoff <= 0 ||
		c.MaxBackoff < c.InitialBackoff ||
		c.MultiplierMillis < 1000 ||
		c.AttemptTimeout <= 0 ||
		c.FailureThreshold == 0 ||
		c.OpenDuration <= 0 ||
		len(c.RetryableCodes) == 0 {
		return errors.New("gRPC reliability configuration is invalid")
	}
	return nil
}

func UnaryClientInterceptor(config Config) (grpc.UnaryClientInterceptor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return dynamicUnaryClientInterceptor(func(context.Context) (Config, error) { return config, nil }), nil
}

func DynamicUnaryClientInterceptor(
	provider func() (Config, error),
) (grpc.UnaryClientInterceptor, error) {
	if provider == nil {
		return nil, errors.New("gRPC reliability configuration provider is required")
	}
	return dynamicUnaryClientInterceptor(func(context.Context) (Config, error) {
		return provider()
	}), nil
}

func DynamicUnaryClientInterceptorForContext(
	provider func(context.Context) (Config, error),
) (grpc.UnaryClientInterceptor, error) {
	if provider == nil {
		return nil, errors.New("context-aware gRPC reliability configuration provider is required")
	}
	return dynamicUnaryClientInterceptor(provider), nil
}

func dynamicUnaryClientInterceptor(
	provider func(context.Context) (Config, error),
) grpc.UnaryClientInterceptor {
	breaker := circuitBreaker{}
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		connection *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		config, err := provider(ctx)
		if err != nil {
			return err
		}
		if err = config.Validate(); err != nil {
			return err
		}
		if !breaker.allow(time.Now(), config.OpenDuration) {
			return ErrCircuitOpen
		}
		delay := config.InitialBackoff
		var lastErr error
		for attempt := uint32(1); attempt <= config.MaxAttempts; attempt++ {
			attemptCtx, cancel := context.WithTimeout(ctx, config.AttemptTimeout)
			lastErr = invoker(attemptCtx, method, req, reply, connection, opts...)
			cancel()
			if lastErr == nil {
				breaker.recordSuccess()
				return nil
			}
			if _, retryable := config.RetryableCodes[status.Code(lastErr)]; !retryable ||
				attempt == config.MaxAttempts ||
				ctx.Err() != nil {
				break
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				lastErr = ctx.Err()
				attempt = config.MaxAttempts
			case <-timer.C:
				delay = nextBackoff(delay, config)
			}
		}
		breaker.recordFailure(time.Now(), config.FailureThreshold)
		return lastErr
	}
}

type circuitBreaker struct {
	mu     sync.Mutex
	fails  uint32
	opened time.Time
	probe  bool
}

func (b *circuitBreaker) allow(now time.Time, openDuration time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.opened.IsZero() {
		return true
	}
	if now.Sub(b.opened) < openDuration || b.probe {
		return false
	}
	b.probe = true
	return true
}

func (b *circuitBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fails = 0
	b.opened = time.Time{}
	b.probe = false
}

func (b *circuitBreaker) recordFailure(now time.Time, failureThreshold uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probe = false
	b.fails++
	if b.fails >= failureThreshold {
		b.opened = now
	}
}

func nextBackoff(current time.Duration, config Config) time.Duration {
	if current >= config.MaxBackoff {
		return config.MaxBackoff
	}
	whole := time.Duration(config.MultiplierMillis / 1000)
	remainder := time.Duration(config.MultiplierMillis % 1000)
	if whole > 0 && current > config.MaxBackoff/whole {
		return config.MaxBackoff
	}
	next := current * whole
	fraction := (current/1000)*remainder + (current%1000)*remainder/1000
	if fraction > config.MaxBackoff-next {
		return config.MaxBackoff
	}
	next += fraction
	return min(next, config.MaxBackoff)
}
