package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/quick"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Feature: rust-bpm-platform, Property 24: Timeout invokes configured fallback
func TestTimeoutInvokesConfiguredFallback(t *testing.T) {
	property := func(rawTimeout uint8) bool {
		timeout := time.Duration(rawTimeout%5+1) * time.Millisecond
		expected := fmt.Sprintf("fallback-%d", rawTimeout)
		got, err := InvokeWithFallback(
			context.Background(),
			FallbackConfig{Timeout: timeout},
			func(ctx context.Context) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			},
			func(_ context.Context, cause error) (string, error) {
				if !errors.Is(cause, context.DeadlineExceeded) {
					return "", cause
				}
				return expected, nil
			},
		)
		return err == nil && got == expected
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func testConfig() Config {
	return Config{
		MaxAttempts:      3,
		InitialBackoff:   time.Millisecond,
		MaxBackoff:       2 * time.Millisecond,
		MultiplierMillis: 2000,
		AttemptTimeout:   time.Second,
		FailureThreshold: 2,
		OpenDuration:     time.Minute,
		RetryableCodes:   map[codes.Code]struct{}{codes.Unavailable: {}},
	}
}

func TestCircuitThresholdProperty(t *testing.T) {
	// Feature: rust-bpm-platform, Property 25: circuit breaker state transition
	property := func(rawThreshold, rawFailures uint8) bool {
		threshold := uint32(rawThreshold%8) + 1
		failures := uint32(rawFailures % 16)
		config := testConfig()
		config.FailureThreshold = threshold
		breaker := circuitBreaker{}
		now := time.Unix(1_000, 0)
		for range failures {
			breaker.recordFailure(now, threshold)
		}
		allowed := breaker.allow(now, config.OpenDuration)
		return allowed == (failures < threshold)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 256}); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicInterceptorUsesLatestPolicy(t *testing.T) {
	config := testConfig()
	interceptor, err := DynamicUnaryClientInterceptor(func() (Config, error) {
		return config, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	config.MaxAttempts = 1
	attempts := 0
	err = interceptor(context.Background(), "/test", nil, nil, nil, func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		attempts++
		return status.Error(codes.Unavailable, "retry")
	})
	if status.Code(err) != codes.Unavailable || attempts != 1 {
		t.Fatalf("latest retry policy was not used: attempts=%d error=%v", attempts, err)
	}
}

func TestInterceptorRetriesOnlyConfiguredCodes(t *testing.T) {
	interceptor, err := UnaryClientInterceptor(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err = interceptor(context.Background(), "/test", nil, nil, nil, func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		attempts++
		if attempts < 3 {
			return status.Error(codes.Unavailable, "retry")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("expected three attempts and success, attempts=%d error=%v", attempts, err)
	}
}

func TestCircuitOpensAfterConfiguredFailures(t *testing.T) {
	config := testConfig()
	config.MaxAttempts = 1
	interceptor, err := UnaryClientInterceptor(config)
	if err != nil {
		t.Fatal(err)
	}
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "down")
	}
	for range config.FailureThreshold {
		if err = interceptor(context.Background(), "/test", nil, nil, nil, invoker); err == nil {
			t.Fatal("expected upstream failure")
		}
	}
	if err = interceptor(context.Background(), "/test", nil, nil, nil, invoker); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected open circuit error, got %v", err)
	}
}
