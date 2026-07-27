package redislimit

import (
	"context"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestLimitIsSharedAcrossGatewayReplicasAndHashesSubject(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: server.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		if err := clientA.Close(); err != nil {
			t.Errorf("close first Redis client: %v", err)
		}
		if err := clientB.Close(); err != nil {
			t.Errorf("close second Redis client: %v", err)
		}
	})
	config := Config{
		Prefix:           "bpmp:test",
		Requests:         2,
		Window:           time.Minute,
		OperationTimeout: time.Second,
	}
	first, err := New(clientA, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(clientB, config)
	if err != nil {
		t.Fatal(err)
	}
	subject := "tenant-a\x00actor-sensitive"
	for index, limiter := range []*Limiter{first, second} {
		allowed, allowErr := limiter.Allow(context.Background(), subject)
		if allowErr != nil || !allowed {
			t.Fatalf("replica %d should be allowed: %v", index, allowErr)
		}
	}
	allowed, err := first.Allow(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("shared distributed limit was exceeded")
	}
	for _, key := range server.Keys() {
		if strings.Contains(key, "tenant-a") || strings.Contains(key, "actor-sensitive") {
			t.Fatalf("rate-limit key leaked subject identity: %s", key)
		}
	}
}

func TestDistributedRateLimitProperty(t *testing.T) {
	// Feature: rust-bpm-platform, Property 36: rate limit bounds accepted requests
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	property := func(rawLimit, rawAttempts uint8) bool {
		server.FlushAll()
		limit := uint32(rawLimit%32) + 1
		attempts := int(rawAttempts % 96)
		limiter, err := New(client, Config{
			Prefix: "bpmp:p36", Requests: limit, Window: time.Minute,
			OperationTimeout: time.Second,
		})
		if err != nil {
			return false
		}
		accepted := 0
		for range attempts {
			allowed, allowErr := limiter.Allow(context.Background(), "tenant-a\x00actor-a")
			if allowErr != nil {
				return false
			}
			if allowed {
				accepted++
			}
		}
		return accepted == min(attempts, int(limit))
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestTenantLimitsAreIndependentProperty(t *testing.T) {
	// Feature: rust-bpm-platform, Property 47: tenant-scoped resource bounds are independent
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	property := func(rawLimit uint8) bool {
		server.FlushAll()
		limit := uint32(rawLimit%32) + 1
		limiter, err := New(client, Config{
			Prefix: "bpmp:p47", Requests: limit, Window: time.Minute,
			OperationTimeout: time.Second,
		})
		if err != nil {
			return false
		}
		for range limit {
			allowed, allowErr := limiter.Allow(context.Background(), "tenant-a\x00actor")
			if allowErr != nil || !allowed {
				return false
			}
		}
		blocked, err := limiter.Allow(context.Background(), "tenant-a\x00actor")
		if err != nil || blocked {
			return false
		}
		otherAllowed, err := limiter.Allow(context.Background(), "tenant-b\x00actor")
		return err == nil && otherAllowed
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
