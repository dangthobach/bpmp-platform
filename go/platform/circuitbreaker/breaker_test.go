package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

type permanentRecipientError struct {
	reason string
}

func (e permanentRecipientError) Error() string { return e.reason }

type stateEvent struct {
	key  string
	from State
	to   State
	snap HealthSnapshot
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	valid := Config{
		RollingWindow:           10 * time.Second,
		MinRequests:             4,
		FailureRateThresholdBPS: 5000,
		OpenDuration:            5 * time.Second,
		HalfOpenMaxProbes:       2,
	}
	if _, err := NewBreaker("ses", valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []func(*Config){
		func(c *Config) { c.RollingWindow = 0 },
		func(c *Config) { c.MinRequests = 0 },
		func(c *Config) { c.FailureRateThresholdBPS = 0 },
		func(c *Config) { c.FailureRateThresholdBPS = 10001 },
		func(c *Config) { c.OpenDuration = 0 },
		func(c *Config) { c.HalfOpenMaxProbes = 0 },
		func(c *Config) { c.MaxManualOverrideTTL = -time.Second },
		func(c *Config) { c.BucketCount = -1 },
		func(c *Config) { c.SlowCallThreshold = -time.Millisecond },
		func(c *Config) { c.SlowCallRateThresholdBPS = 10001 },
		func(c *Config) { c.SlowCallThreshold = 0; c.SlowCallRateThresholdBPS = 5000 },
	}
	for i, mutate := range cases {
		cfg := valid
		mutate(&cfg)
		if _, err := NewBreaker("ses", cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: expected ErrInvalidConfig, got %v", i, err)
		}
		if _, err := NewRegistry(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d registry: expected ErrInvalidConfig, got %v", i, err)
		}
	}
}

func TestRollingWindowBucketExpiryAndOpening(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	var events []stateEvent
	breaker, err := NewBreaker("tenant-a:smtp", Config{
		RollingWindow:           10 * time.Second,
		BucketCount:             10,
		MinRequests:             4,
		FailureRateThresholdBPS: 5000,
		OpenDuration:            30 * time.Second,
		HalfOpenMaxProbes:       2,
		Now:                     clock.Now,
		OnStateChange: func(key string, from, to State, snap HealthSnapshot) {
			events = append(events, stateEvent{key: key, from: from, to: to, snap: snap})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	providerDown := errors.New("smtp connection timeout")

	// Record 2 failures at t=0s (< MinRequests=4, so stays CLOSED).
	for range 2 {
		_ = breaker.Execute(func() error { return providerDown })
	}
	if breaker.State() != StateClosed {
		t.Fatalf("breaker tripped before MinRequests: %s", breaker.State())
	}

	// Advance 6s and record 2 successes -> 2 failures / 4 requests = 5000 BPS? Wait:
	// let's record 3 successes so 2/5 = 4000 BPS (< 5000 BPS).
	clock.Advance(6 * time.Second)
	for range 3 {
		if err = breaker.Execute(func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if snap := breaker.Snapshot(); snap.State != StateClosed || snap.FailureRateBPS != 4000 {
		t.Fatalf("unexpected rolling window snapshot at t=6s: %+v", snap)
	}

	// Advance another 4s (t=10s): bucket from t=0s (2 failures) expires!
	// Now window has 3 successes, 0 failures.
	clock.Advance(4 * time.Second)
	if snap := breaker.Snapshot(); snap.Failures != 0 || snap.Successes != 3 {
		t.Fatalf("expired bucket at t=0s was not evicted: %+v", snap)
	}

	// One failure at t=10s gives 1/4 = 2500 BPS (< 5000 BPS), still CLOSED.
	_ = breaker.Execute(func() error { return providerDown })
	if breaker.State() != StateClosed {
		t.Fatalf("expected CLOSED after 1 failure post-eviction, got %s", breaker.State())
	}

	// Advance 6s (t=16s) so t=6s successes expire too; record 3 more failures -> 4/4 = 10000 BPS -> OPEN.
	clock.Advance(6 * time.Second)
	for range 3 {
		_ = breaker.Execute(func() error { return providerDown })
	}
	if breaker.State() != StateOpen {
		t.Fatalf("expected OPEN after exceeding failure rate threshold, got %s", breaker.State())
	}
	if len(events) != 1 || events[0].from != StateClosed || events[0].to != StateOpen || events[0].key != "tenant-a:smtp" {
		t.Fatalf("unexpected state change events: %+v", events)
	}

	// Subsequent call while OPEN is blocked immediately.
	err = breaker.Execute(func() error {
		t.Fatal("operation must not run while OPEN")
		return nil
	})
	if !errors.Is(err, ErrOpenState) || !IsBlocked(err) {
		t.Fatalf("expected ErrOpenState, got %v", err)
	}
}

func TestExcludedPermanentErrorsAreIgnored(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	breaker, err := NewBreaker("tenant-a:ses", Config{
		RollingWindow:           10 * time.Second,
		MinRequests:             4,
		FailureRateThresholdBPS: 5000,
		OpenDuration:            5 * time.Second,
		HalfOpenMaxProbes:       2,
		Now:                     clock.Now,
		IsExcluded: func(err error) bool {
			var perm permanentRecipientError
			return errors.As(err, &perm)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 25 permanent recipient validation errors must never trip the breaker.
	for range 25 {
		opErr := breaker.Execute(func() error {
			return permanentRecipientError{reason: "invalid email syntax"}
		})
		if opErr == nil {
			t.Fatal("expected caller to still receive permanent error")
		}
	}
	snap := breaker.Snapshot()
	if snap.State != StateClosed || snap.Requests != 0 || snap.Failures != 0 || snap.Excluded != 25 {
		t.Fatalf("excluded errors polluted breaker counts: %+v", snap)
	}

	// Excluded errors must also not dilute real infrastructure failures:
	// 4 real failures alongside those 25 excluded errors must represent 100% failure rate and trip OPEN.
	infraErr := errors.New("503 service unavailable")
	for range 4 {
		_ = breaker.Execute(func() error { return infraErr })
	}
	if breaker.State() != StateOpen {
		t.Fatalf("excluded errors diluted real failures; expected OPEN, got %s", breaker.State())
	}
}

func TestHalfOpenProbeRecoveryAndReopen(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	var transitions []stateEvent
	breaker, err := NewBreaker("tenant-a:fcm", Config{
		RollingWindow:           10 * time.Second,
		MinRequests:             2,
		FailureRateThresholdBPS: 5000,
		OpenDuration:            15 * time.Second,
		HalfOpenMaxProbes:       2,
		Now:                     clock.Now,
		IsExcluded: func(err error) bool {
			var perm permanentRecipientError
			return errors.As(err, &perm)
		},
		OnStateChange: func(key string, from, to State, snap HealthSnapshot) {
			transitions = append(transitions, stateEvent{key: key, from: from, to: to, snap: snap})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	infraErr := errors.New("upstream reset")
	_ = breaker.Execute(func() error { return infraErr })
	_ = breaker.Execute(func() error { return infraErr })
	if breaker.State() != StateOpen {
		t.Fatalf("expected OPEN, got %s", breaker.State())
	}

	// Before OpenDuration expires, stays OPEN.
	clock.Advance(14 * time.Second)
	if _, err = breaker.Allow(); !errors.Is(err, ErrOpenState) {
		t.Fatalf("expected ErrOpenState at 14s, got %v", err)
	}

	// At 15s, transitions OPEN -> HALF_OPEN and issues at most HalfOpenMaxProbes=2 probes.
	clock.Advance(time.Second)
	done1, err := breaker.Allow()
	if err != nil {
		t.Fatalf("probe 1 rejected: %v", err)
	}
	done2, err := breaker.Allow()
	if err != nil {
		t.Fatalf("probe 2 rejected: %v", err)
	}
	if _, err = breaker.Allow(); !errors.Is(err, ErrTooManyProbes) || !IsBlocked(err) {
		t.Fatalf("expected ErrTooManyProbes for 3rd concurrent probe, got %v", err)
	}

	// If probe 1 fails with an excluded permanent error, it does NOT reopen the circuit
	// and frees the probe slot for another real probe.
	done1(permanentRecipientError{reason: "unregistered token"})
	if breaker.State() != StateHalfOpen {
		t.Fatalf("excluded error in HALF_OPEN must not reopen circuit, got %s", breaker.State())
	}
	doneReplacement, err := breaker.Allow()
	if err != nil {
		t.Fatalf("replacement probe after excluded error should be allowed: %v", err)
	}

	// Probe 2 succeeds, but replacement probe fails with an infrastructure error -> immediately reopens!
	done2(nil)
	doneReplacement(infraErr)
	if breaker.State() != StateOpen {
		t.Fatalf("expected HALF_OPEN failure to reopen breaker, got %s", breaker.State())
	}

	// Advance another full OpenDuration=15s -> HALF_OPEN again; 2 successful probes recover to CLOSED.
	clock.Advance(15 * time.Second)
	if err = breaker.ExecuteContext(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("recovery probe 1 failed: %v", err)
	}
	if breaker.State() != StateHalfOpen {
		t.Fatalf("expected still HALF_OPEN after 1/2 probes, got %s", breaker.State())
	}
	if err = breaker.ExecuteContext(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("recovery probe 2 failed: %v", err)
	}
	snap := breaker.Snapshot()
	if snap.State != StateClosed || snap.Failures != 0 || snap.Requests != 0 {
		t.Fatalf("expected clean CLOSED state after probe recovery, got %+v", snap)
	}

	wantSeq := [][2]State{
		{StateClosed, StateOpen},
		{StateOpen, StateHalfOpen},
		{StateHalfOpen, StateOpen},
		{StateOpen, StateHalfOpen},
		{StateHalfOpen, StateClosed},
	}
	if len(transitions) != len(wantSeq) {
		t.Fatalf("expected %d transitions, got %d: %+v", len(wantSeq), len(transitions), transitions)
	}
	for i, want := range wantSeq {
		if transitions[i].from != want[0] || transitions[i].to != want[1] {
			t.Fatalf("transition %d: want %s->%s, got %s->%s", i, want[0], want[1], transitions[i].from, transitions[i].to)
		}
	}
}

func TestManualOverridesAndTTLExpiry(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	registry, err := NewRegistry(Config{
		RollingWindow:           10 * time.Second,
		MinRequests:             2,
		FailureRateThresholdBPS: 5000,
		OpenDuration:            30 * time.Second,
		HalfOpenMaxProbes:       1,
		MaxManualOverrideTTL:    time.Hour,
		Now:                     clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	const key = "tenant-a:zalo-zns"

	// Reject override exceeding MaxManualOverrideTTL or unknown mode.
	if err = registry.SetOverride(key, OverrideForceOpen, clock.Now().Add(2*time.Hour)); !errors.Is(err, ErrInvalidOverride) {
		t.Fatalf("expected TTL cap violation ErrInvalidOverride, got %v", err)
	}
	if err = registry.SetOverride(key, OverrideMode("UNKNOWN"), clock.Now().Add(time.Minute)); !errors.Is(err, ErrInvalidOverride) {
		t.Fatalf("expected invalid mode ErrInvalidOverride, got %v", err)
	}

	// Pre-traffic FORCE_OPEN blocks calls until now >= expiresAt.
	expiresAt := clock.Now().Add(5 * time.Minute)
	if err = registry.SetOverride(key, OverrideForceOpen, expiresAt); err != nil {
		t.Fatal(err)
	}
	breaker := registry.GetOrCreate(key)
	if err = breaker.Execute(func() error { return nil }); !errors.Is(err, ErrOpenState) {
		t.Fatalf("expected FORCE_OPEN to reject with ErrOpenState, got %v", err)
	}
	snap, ok := registry.Snapshot(key)
	if !ok || snap.Override != OverrideForceOpen || snap.EffectiveState != StateOpen || snap.State != StateClosed {
		t.Fatalf("unexpected snapshot during FORCE_OPEN: %+v", snap)
	}

	// Advance to exact expiresAt (now == expiresAt) -> override automatically expires!
	clock.Advance(5 * time.Minute)
	if err = breaker.Execute(func() error { return nil }); err != nil {
		t.Fatalf("expected FORCE_OPEN to expire at now == expiresAt, got %v", err)
	}
	if snap = breaker.Snapshot(); snap.Override != OverrideNone || snap.EffectiveState != StateClosed {
		t.Fatalf("override metadata was not cleared on expiry: %+v", snap)
	}

	// Trip breaker to OPEN, then apply FORCE_CLOSE for 2 minutes.
	infraErr := errors.New("gateway timeout")
	_ = breaker.Execute(func() error { return infraErr })
	_ = breaker.Execute(func() error { return infraErr })
	if breaker.State() != StateOpen {
		t.Fatalf("expected OPEN before FORCE_CLOSE, got %s", breaker.State())
	}

	if err = registry.SetOverride(key, OverrideForceClose, clock.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Even repeated failures while FORCE_CLOSE is active do not block calls.
	for range 4 {
		_ = breaker.Execute(func() error { return infraErr })
	}
	if breaker.State() != StateClosed {
		t.Fatalf("FORCE_CLOSE should keep effective state CLOSED before expiry, got %s", breaker.State())
	}

	// Once FORCE_CLOSE expires (advance 2m; note RollingWindow=10s so old failures aged out),
	// 2 new failures trip normal circuit breaking again.
	clock.Advance(2 * time.Minute)
	_ = breaker.Execute(func() error { return infraErr })
	_ = breaker.Execute(func() error { return infraErr })
	if breaker.State() != StateOpen {
		t.Fatalf("normal circuit breaking did not resume after FORCE_CLOSE expired, got %s", breaker.State())
	}

	// Clear OPEN via ClearOverride after setting DRAINING and testing in-flight completion.
	clock.Advance(30 * time.Second)
	if err = breaker.Execute(func() error { return nil }); err != nil {
		t.Fatalf("recover from HALF_OPEN failed: %v", err)
	}
	inFlightDone, err := breaker.Allow()
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.SetOverride(key, OverrideDraining, clock.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = breaker.Execute(func() error { return nil }); !errors.Is(err, ErrDraining) || !IsBlocked(err) {
		t.Fatalf("expected ErrDraining while draining, got %v", err)
	}
	inFlightDone(nil)

	// Explicit ClearOverride immediately restores traffic before TTL.
	registry.ClearOverride(key)
	if err = breaker.Execute(func() error { return nil }); err != nil {
		t.Fatalf("expected traffic restored after ClearOverride, got %v", err)
	}

	// Key isolation check in Registry.
	other := registry.GetOrCreate("tenant-a:smtp-backup")
	_ = breaker.SetOverride(OverrideForceOpen, clock.Now().Add(time.Minute))
	if err = other.Execute(func() error { return nil }); err != nil {
		t.Fatalf("override leaked across registry keys: %v", err)
	}
	if len(registry.Snapshots()) != 2 {
		t.Fatalf("expected 2 snapshots in registry, got %d", len(registry.Snapshots()))
	}
}

func TestSlowCallRateOpensBreakerAndReportsSnapshot(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	var events []stateEvent
	breaker, err := NewBreaker("tenant-a:slow-smtp", Config{
		RollingWindow:            10 * time.Second,
		BucketCount:              10,
		MinRequests:              4,
		FailureRateThresholdBPS:  8000,
		SlowCallThreshold:        500 * time.Millisecond,
		SlowCallRateThresholdBPS: 5000,
		OpenDuration:             30 * time.Second,
		HalfOpenMaxProbes:        2,
		Now:                      clock.Now,
		IsExcluded: func(err error) bool {
			var perm permanentRecipientError
			return errors.As(err, &perm)
		},
		OnStateChange: func(key string, from, to State, snap HealthSnapshot) {
			events = append(events, stateEvent{key: key, from: from, to: to, snap: snap})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2 fast successes (duration == SlowCallThreshold is NOT slow, strictly > SlowCallThreshold).
	for range 2 {
		if err = breaker.Execute(func() error {
			clock.Advance(500 * time.Millisecond)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 1 excluded slow error must not count toward buffered calls or slow calls.
	_ = breaker.ExecuteContext(context.Background(), func(context.Context) error {
		clock.Advance(2 * time.Second)
		return permanentRecipientError{reason: "bad address"}
	})
	// 1 slow success -> 1/3 slow (< MinRequests=4), stays CLOSED.
	if err = breaker.ExecuteContext(context.Background(), func(context.Context) error {
		clock.Advance(600 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snap := breaker.Snapshot()
	if snap.State != StateClosed ||
		snap.BufferedCalls != 3 ||
		snap.FailedCalls != 0 ||
		snap.SlowCalls != 1 ||
		snap.FailureRateBPS != 0 ||
		snap.SlowCallRateBPS != 3333 ||
		snap.CooldownRemaining != 0 {
		t.Fatalf("unexpected snapshot before slow-call trip: %+v", snap)
	}

	// 2nd slow success -> 2/4 = 5000 BPS >= SlowCallRateThresholdBPS -> trips OPEN!
	if err = breaker.Execute(func() error {
		clock.Advance(501 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if breaker.State() != StateOpen {
		t.Fatalf("expected OPEN after slow-call rate reached threshold, got %s", breaker.State())
	}
	if len(events) != 1 || events[0].from != StateClosed || events[0].to != StateOpen {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].snap.CooldownRemaining != 30*time.Second {
		t.Fatalf("expected initial CooldownRemaining=30s, got %s", events[0].snap.CooldownRemaining)
	}

	// CooldownRemaining decreases deterministically with fake clock.
	clock.Advance(12 * time.Second)
	snap = breaker.Snapshot()
	if snap.State != StateOpen || snap.CooldownRemaining != 18*time.Second {
		t.Fatalf("expected OPEN with CooldownRemaining=18s, got %+v", snap)
	}
}

func TestRecordExternalDurationAndRegistryRecord(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	registry, err := NewRegistry(Config{
		RollingWindow:            10 * time.Second,
		MinRequests:              4,
		FailureRateThresholdBPS:  5000,
		SlowCallThreshold:        time.Second,
		SlowCallRateThresholdBPS: 5000,
		OpenDuration:             20 * time.Second,
		HalfOpenMaxProbes:        1,
		Now:                      clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	const key = "tenant-a:webhook"
	registry.Record(key, 200*time.Millisecond, nil)
	registry.Record(key, 1500*time.Millisecond, nil)
	registry.Record(key, 1200*time.Millisecond, errors.New("timeout"))

	snap, ok := registry.Snapshot(key)
	if !ok {
		t.Fatal("expected breaker to exist after Registry.Record")
	}
	if snap.State != StateClosed || snap.BufferedCalls != 3 || snap.FailedCalls != 1 || snap.SlowCalls != 2 || snap.SlowCallRateBPS != 6666 {
		t.Fatalf("unexpected snapshot after 3 Record calls: %+v", snap)
	}

	// 4th call (fast success) still leaves slow rate 2/4 = 5000 BPS? Wait: fast call doesn't trigger slow-call trip?
	// Let's test a 4th slow call -> 3/4 = 7500 BPS -> trips OPEN!
	registry.Record(key, 2*time.Second, nil)
	snap, _ = registry.Snapshot(key)
	if snap.State != StateOpen || snap.CooldownRemaining != 20*time.Second {
		t.Fatalf("expected OPEN with 20s cooldown after 4th slow Record, got %+v", snap)
	}
}

func TestProbeClearsOverrideAndTransitionsOpenToHalfOpen(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	var events []stateEvent
	registry, err := NewRegistry(Config{
		RollingWindow:           10 * time.Second,
		MinRequests:             2,
		FailureRateThresholdBPS: 5000,
		OpenDuration:            60 * time.Second,
		HalfOpenMaxProbes:       1,
		MaxManualOverrideTTL:    time.Hour,
		Now:                     clock.Now,
		OnStateChange: func(key string, from, to State, snap HealthSnapshot) {
			events = append(events, stateEvent{key: key, from: from, to: to, snap: snap})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	const key = "tenant-a:apns"
	b := registry.GetOrCreate(key)
	infraErr := errors.New("connection refused")
	_ = b.Execute(func() error { return infraErr })
	_ = b.Execute(func() error { return infraErr })
	if b.State() != StateOpen {
		t.Fatalf("expected OPEN, got %s", b.State())
	}

	// Apply FORCE_OPEN override while OPEN.
	if err = registry.SetOverride(key, OverrideForceOpen, clock.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Operator triggers Probe() well before OpenDuration (60s) expires.
	clock.Advance(5 * time.Second)
	registry.Probe(key)

	snap, ok := registry.Snapshot(key)
	if !ok {
		t.Fatal("expected snapshot")
	}
	if snap.State != StateHalfOpen || snap.EffectiveState != StateHalfOpen || snap.Override != OverrideNone || snap.CooldownRemaining != 0 {
		t.Fatalf("expected HALF_OPEN with cleared override after Probe, got %+v", snap)
	}
	if len(events) != 2 || events[1].from != StateOpen || events[1].to != StateHalfOpen {
		t.Fatalf("expected OPEN->HALF_OPEN OnStateChange event from Probe, got %+v", events)
	}

	// A single successful probe closes the breaker.
	if err = b.Execute(func() error { return nil }); err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}
	if b.State() != StateClosed {
		t.Fatalf("expected CLOSED after successful probe, got %s", b.State())
	}

	// Probe on unknown key is a safe no-op.
	registry.Probe("unknown-key")
	if _, exists := registry.Get("unknown-key"); exists {
		t.Fatal("Probe must not create breaker for unknown key")
	}
}
