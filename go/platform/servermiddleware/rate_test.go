package servermiddleware

import "testing"

func TestTokenBucketRejectsBeyondConfiguredBurst(t *testing.T) {
	limiter, err := NewTokenBucket(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !limiter.Allow() || !limiter.Allow() || limiter.Allow() {
		t.Fatal("token bucket did not enforce its configured burst")
	}
}
