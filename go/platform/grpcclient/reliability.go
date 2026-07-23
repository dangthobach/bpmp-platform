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

type Config struct {
	MaxAttempts      uint32
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
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
	breaker := circuitBreaker{config: config}
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		connection *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if !breaker.allow(time.Now()) {
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
				delay = min(delay*2, config.MaxBackoff)
			}
		}
		breaker.recordFailure(time.Now())
		return lastErr
	}, nil
}

type circuitBreaker struct {
	config Config
	mu     sync.Mutex
	fails  uint32
	opened time.Time
	probe  bool
}

func (b *circuitBreaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.opened.IsZero() {
		return true
	}
	if now.Sub(b.opened) < b.config.OpenDuration || b.probe {
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

func (b *circuitBreaker) recordFailure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probe = false
	b.fails++
	if b.fails >= b.config.FailureThreshold {
		b.opened = now
	}
}
