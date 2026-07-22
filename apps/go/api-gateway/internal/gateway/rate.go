package gateway

import (
	"sync"
	"time"
)

type rateEntry struct {
	window time.Time
	count  uint32
}
type rateLimiter struct {
	mu          sync.Mutex
	entries     map[string]rateEntry
	requests    uint32
	window      time.Duration
	maxSubjects int
}

func newRateLimiter(requests uint32, window time.Duration, maxSubjects int) *rateLimiter {
	return &rateLimiter{entries: make(map[string]rateEntry), requests: requests, window: window, maxSubjects: maxSubjects}
}
func (l *rateLimiter) allow(subject string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[subject]
	if !ok && len(l.entries) >= l.maxSubjects {
		l.evictExpired(now)
		if len(l.entries) >= l.maxSubjects {
			return false
		}
	}
	if !ok || now.Sub(entry.window) >= l.window {
		l.entries[subject] = rateEntry{window: now, count: 1}
		return true
	}
	if entry.count >= l.requests {
		return false
	}
	entry.count++
	l.entries[subject] = entry
	return true
}
func (l *rateLimiter) evictExpired(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.window) >= l.window {
			delete(l.entries, key)
		}
	}
}
