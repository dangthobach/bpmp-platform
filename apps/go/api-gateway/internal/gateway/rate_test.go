package gateway

import (
	"context"
	"sync"
	"testing"
	"testing/quick"
	"time"
)

type modelRateEntry struct {
	window time.Time
	count  uint32
}

type modelRateLimiter struct {
	mu       sync.Mutex
	entries  map[string]modelRateEntry
	requests uint32
	window   time.Duration
}

func newModelRateLimiter(requests uint32, window time.Duration) *modelRateLimiter {
	return &modelRateLimiter{
		entries:  make(map[string]modelRateEntry),
		requests: requests,
		window:   window,
	}
}

func (l *modelRateLimiter) Allow(_ context.Context, subject string) (bool, error) {
	return l.allow(subject, time.Now()), nil
}

func (l *modelRateLimiter) allow(subject string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[subject]
	if !ok || now.Sub(entry.window) >= l.window {
		l.entries[subject] = modelRateEntry{window: now, count: 1}
		return true
	}
	if entry.count >= l.requests {
		return false
	}
	entry.count++
	l.entries[subject] = entry
	return true
}

func TestRateLimitProperty(t *testing.T) {
	// Feature: rust-bpm-platform, Property 36: rate limit bounds accepted requests
	property := func(rawLimit, rawAttempts uint8) bool {
		limit := uint32(rawLimit%64) + 1
		attempts := int(rawAttempts)
		limiter := newModelRateLimiter(limit, time.Minute)
		now := time.Unix(1_000, 0)
		accepted := 0
		for range attempts {
			if limiter.allow("tenant-a\x00actor-a", now) {
				accepted++
			}
		}
		expected := min(attempts, int(limit))
		return accepted == expected
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 256}); err != nil {
		t.Fatal(err)
	}
}
