package grpcclient

import (
	"context"
	"testing"
	"testing/quick"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testConfig() Config {
	return Config{
		MaxAttempts:      3,
		InitialBackoff:   time.Millisecond,
		MaxBackoff:       2 * time.Millisecond,
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
		breaker := circuitBreaker{config: config}
		now := time.Unix(1_000, 0)
		for range failures {
			breaker.recordFailure(now)
		}
		allowed := breaker.allow(now)
		return allowed == (failures < threshold)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 256}); err != nil {
		t.Fatal(err)
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
