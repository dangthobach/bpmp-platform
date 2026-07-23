package redislimit

import (
	"context"
	"strings"
	"testing"
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
