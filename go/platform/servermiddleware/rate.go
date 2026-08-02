package servermiddleware

import (
	"errors"
	"sync"
	"time"
)

// TokenBucket is a process-local admission limiter for transport protection.
// Tenant and actor quotas remain in distributed policy-aware limiters.
type TokenBucket struct {
	mu            sync.Mutex
	capacity      float64
	tokens        float64
	refillPerNano float64
	lastRefill    time.Time
}

func NewTokenBucket(requestsPerSecond, burst uint32) (*TokenBucket, error) {
	if requestsPerSecond == 0 || burst == 0 {
		return nil, errors.New("admission rate and burst must be positive")
	}
	capacity := float64(burst)
	return &TokenBucket{
		capacity: capacity, tokens: capacity,
		refillPerNano: float64(requestsPerSecond) / float64(time.Second),
		lastRefill:    time.Now(),
	}, nil
}

func (l *TokenBucket) Allow() bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	elapsed := now.Sub(l.lastRefill)
	if elapsed > 0 {
		l.tokens += float64(elapsed) * l.refillPerNano
		if l.tokens > l.capacity {
			l.tokens = l.capacity
		}
		l.lastRefill = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
