package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidConfig   = errors.New("circuit breaker configuration is invalid")
	ErrInvalidOverride = errors.New("circuit breaker manual override is invalid")
	ErrOpenState       = errors.New("circuit breaker is open")
	ErrTooManyProbes   = errors.New("circuit breaker half-open probe limit reached")
	ErrDraining        = errors.New("circuit breaker is draining")
)

const (
	defaultBucketCount = 10
	maxBasisPoints     = 10000
)

// IsBlocked reports whether err indicates a call rejected by circuit breaker
// state or manual override before reaching the downstream dependency.
func IsBlocked(err error) bool {
	return errors.Is(err, ErrOpenState) ||
		errors.Is(err, ErrTooManyProbes) ||
		errors.Is(err, ErrDraining)
}

type State string

const (
	StateClosed   State = "CLOSED"
	StateOpen     State = "OPEN"
	StateHalfOpen State = "HALF_OPEN"
)

func (s State) String() string {
	return string(s)
}

type OverrideMode string

const (
	OverrideNone       OverrideMode = "NONE"
	OverrideForceOpen  OverrideMode = "FORCE_OPEN"
	OverrideForceClose OverrideMode = "FORCE_CLOSE"
	OverrideDraining   OverrideMode = "DRAINING"
)

func (m OverrideMode) String() string {
	if m == "" {
		return string(OverrideNone)
	}
	return string(m)
}

func (m OverrideMode) Valid() bool {
	switch m {
	case "", OverrideNone, OverrideForceOpen, OverrideForceClose, OverrideDraining:
		return true
	default:
		return false
	}
}

type Config struct {
	RollingWindow            time.Duration
	MinRequests              uint32
	FailureRateThresholdBPS  uint32
	SlowCallThreshold        time.Duration
	SlowCallRateThresholdBPS uint32
	OpenDuration             time.Duration
	HalfOpenMaxProbes        uint32
	MaxManualOverrideTTL     time.Duration
	BucketCount              int
	Now                      func() time.Time
	IsExcluded               func(error) bool
	OnStateChange            func(key string, from, to State, snap HealthSnapshot)
}

func (c Config) Validate() error {
	if c.RollingWindow <= 0 ||
		c.MinRequests == 0 ||
		c.FailureRateThresholdBPS == 0 ||
		c.FailureRateThresholdBPS > maxBasisPoints ||
		c.SlowCallThreshold < 0 ||
		c.SlowCallRateThresholdBPS > maxBasisPoints ||
		(c.SlowCallRateThresholdBPS > 0 && c.SlowCallThreshold <= 0) ||
		c.OpenDuration <= 0 ||
		c.HalfOpenMaxProbes == 0 ||
		c.MaxManualOverrideTTL < 0 ||
		c.BucketCount < 0 {
		return ErrInvalidConfig
	}
	return nil
}

func (c Config) normalized() Config {
	out := c
	if out.Now == nil {
		out.Now = time.Now
	}
	if out.BucketCount == 0 {
		out.BucketCount = defaultBucketCount
	}
	if out.RollingWindow/time.Duration(out.BucketCount) <= 0 {
		out.BucketCount = 1
	}
	return out
}

type HealthSnapshot struct {
	Key                  string
	State                State
	EffectiveState       State
	Override             OverrideMode
	OverrideExpiresAt    time.Time
	Requests             uint64
	Successes            uint64
	Failures             uint64
	Excluded             uint64
	BufferedCalls        uint64
	FailedCalls          uint64
	SlowCalls            uint64
	FailureRateBPS       uint32
	SlowCallRateBPS      uint32
	CooldownRemaining    time.Duration
	OpenedAt             time.Time
	HalfOpenProbesIssued uint32
	HalfOpenProbeSuccess uint32
	LastError            error
	UpdatedAt            time.Time
}

type windowBucket struct {
	successes uint64
	failures  uint64
	excluded  uint64
	slow      uint64
}

type rollingWindow struct {
	origin       time.Time
	bucketSpan   time.Duration
	buckets      []windowBucket
	lastBucketID int64
	initialized  bool
}

func newRollingWindow(window time.Duration, bucketCount int) rollingWindow {
	span := window / time.Duration(bucketCount)
	if span <= 0 {
		bucketCount = 1
		span = window
	}
	return rollingWindow{
		bucketSpan: span,
		buckets:    make([]windowBucket, bucketCount),
	}
}

func (w *rollingWindow) reset(now time.Time) {
	for i := range w.buckets {
		w.buckets[i] = windowBucket{}
	}
	w.origin = now
	w.lastBucketID = 0
	w.initialized = true
}

func (w *rollingWindow) advance(now time.Time) int {
	if !w.initialized {
		w.origin = now
		w.lastBucketID = 0
		w.initialized = true
		return 0
	}
	if now.Before(w.origin) {
		return int(w.lastBucketID % int64(len(w.buckets)))
	}
	targetID := int64(now.Sub(w.origin) / w.bucketSpan)
	if targetID > w.lastBucketID {
		steps := targetID - w.lastBucketID
		n := int64(len(w.buckets))
		if steps >= n {
			for i := range w.buckets {
				w.buckets[i] = windowBucket{}
			}
		} else {
			for id := w.lastBucketID + 1; id <= targetID; id++ {
				w.buckets[int(id%n)] = windowBucket{}
			}
		}
		w.lastBucketID = targetID
	}
	return int(w.lastBucketID % int64(len(w.buckets)))
}

func (w *rollingWindow) recordSuccess(now time.Time, slow bool) {
	idx := w.advance(now)
	w.buckets[idx].successes++
	if slow {
		w.buckets[idx].slow++
	}
}

func (w *rollingWindow) recordFailure(now time.Time, slow bool) {
	idx := w.advance(now)
	w.buckets[idx].failures++
	if slow {
		w.buckets[idx].slow++
	}
}

func (w *rollingWindow) recordExcluded(now time.Time) {
	idx := w.advance(now)
	w.buckets[idx].excluded++
}

func (w *rollingWindow) totals(now time.Time) (successes, failures, excluded, slow uint64) {
	w.advance(now)
	for i := range w.buckets {
		successes += w.buckets[i].successes
		failures += w.buckets[i].failures
		excluded += w.buckets[i].excluded
		slow += w.buckets[i].slow
	}
	return successes, failures, excluded, slow
}

type transitionNotification struct {
	from State
	to   State
	snap HealthSnapshot
}

type Breaker struct {
	key    string
	config Config

	mu                sync.Mutex
	state             State
	generation        uint64
	openedAt          time.Time
	halfOpenIssued    uint32
	halfOpenSuccesses uint32
	window            rollingWindow
	override          OverrideMode
	overrideExpiresAt time.Time
	lastError         error
	updatedAt         time.Time
}

func NewBreaker(key string, config Config) (*Breaker, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	cfg := config.normalized()
	now := cfg.Now()
	w := newRollingWindow(cfg.RollingWindow, cfg.BucketCount)
	w.reset(now)
	return &Breaker{
		key:       key,
		config:    cfg,
		state:     StateClosed,
		window:    w,
		override:  OverrideNone,
		updatedAt: now,
	}, nil
}

func (b *Breaker) Key() string {
	return b.key
}

func (b *Breaker) Allow() (func(error), error) {
	b.mu.Lock()
	now := b.config.Now()
	b.expireOverrideLocked(now)

	var notify *transitionNotification
	switch b.override {
	case OverrideForceOpen:
		b.mu.Unlock()
		return nil, ErrOpenState
	case OverrideDraining:
		b.mu.Unlock()
		return nil, ErrDraining
	case OverrideForceClose:
		gen := b.generation
		startedAt := now
		b.mu.Unlock()
		var once sync.Once
		return func(err error) {
			once.Do(func() {
				b.completeFromStart(gen, false, startedAt, err)
			})
		}, nil
	}

	notify = b.advanceStateLocked(now)
	switch b.state {
	case StateOpen:
		b.mu.Unlock()
		b.emit(notify)
		return nil, ErrOpenState
	case StateHalfOpen:
		if b.halfOpenIssued >= b.config.HalfOpenMaxProbes {
			b.mu.Unlock()
			b.emit(notify)
			return nil, ErrTooManyProbes
		}
		b.halfOpenIssued++
		b.updatedAt = now
		gen := b.generation
		startedAt := now
		b.mu.Unlock()
		b.emit(notify)
		var once sync.Once
		return func(err error) {
			once.Do(func() {
				b.completeFromStart(gen, true, startedAt, err)
			})
		}, nil
	default:
		gen := b.generation
		startedAt := now
		b.mu.Unlock()
		b.emit(notify)
		var once sync.Once
		return func(err error) {
			once.Do(func() {
				b.completeFromStart(gen, false, startedAt, err)
			})
		}, nil
	}
}

func (b *Breaker) Execute(operation func() error) error {
	done, err := b.Allow()
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			done(errors.New("circuit breaker operation panicked"))
		}
	}()
	opErr := operation()
	completed = true
	done(opErr)
	return opErr
}

func (b *Breaker) ExecuteContext(ctx context.Context, operation func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Execute(func() error {
		return operation(ctx)
	})
}

func (b *Breaker) SetOverride(mode OverrideMode, expiresAt time.Time) error {
	if mode == "" {
		mode = OverrideNone
	}
	if !mode.Valid() {
		return ErrInvalidOverride
	}

	b.mu.Lock()
	now := b.config.Now()
	if mode == OverrideNone {
		b.override = OverrideNone
		b.overrideExpiresAt = time.Time{}
		b.updatedAt = now
		b.mu.Unlock()
		return nil
	}
	if b.config.MaxManualOverrideTTL > 0 {
		if expiresAt.IsZero() || expiresAt.Sub(now) > b.config.MaxManualOverrideTTL {
			b.mu.Unlock()
			return ErrInvalidOverride
		}
	}
	if !expiresAt.IsZero() && !now.Before(expiresAt) {
		b.override = OverrideNone
		b.overrideExpiresAt = time.Time{}
		b.updatedAt = now
		b.mu.Unlock()
		return nil
	}

	b.override = mode
	b.overrideExpiresAt = expiresAt
	b.updatedAt = now

	var notify *transitionNotification
	if mode == OverrideForceClose && b.state != StateClosed {
		notify = b.transitionToLocked(StateClosed, now)
	}
	b.mu.Unlock()
	b.emit(notify)
	return nil
}

func (b *Breaker) ClearOverride() {
	_ = b.SetOverride(OverrideNone, time.Time{})
}

func (b *Breaker) Probe() {
	b.mu.Lock()
	now := b.config.Now()
	b.override = OverrideNone
	b.overrideExpiresAt = time.Time{}
	b.updatedAt = now

	var notify *transitionNotification
	if b.state == StateOpen {
		notify = b.transitionToLocked(StateHalfOpen, now)
	}
	b.mu.Unlock()
	b.emit(notify)
}

func (b *Breaker) Record(duration time.Duration, err error) {
	if duration < 0 {
		duration = 0
	}
	b.mu.Lock()
	now := b.config.Now()
	b.expireOverrideLocked(now)
	advanceNotify := b.advanceStateLocked(now)
	recordNotify := b.recordLocked(now, b.generation, b.state == StateHalfOpen, duration, err)
	b.mu.Unlock()
	b.emit(advanceNotify)
	b.emit(recordNotify)
}

func (b *Breaker) State() State {
	b.mu.Lock()
	now := b.config.Now()
	b.expireOverrideLocked(now)
	notify := b.advanceStateLocked(now)
	effective := b.effectiveStateLocked()
	b.mu.Unlock()
	b.emit(notify)
	return effective
}

func (b *Breaker) Snapshot() HealthSnapshot {
	b.mu.Lock()
	now := b.config.Now()
	b.expireOverrideLocked(now)
	notify := b.advanceStateLocked(now)
	snap := b.snapshotLocked(now)
	b.mu.Unlock()
	b.emit(notify)
	return snap
}

func (b *Breaker) completeFromStart(gen uint64, halfOpenProbe bool, startedAt time.Time, err error) {
	b.mu.Lock()
	now := b.config.Now()
	duration := now.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	b.expireOverrideLocked(now)
	notify := b.recordLocked(now, gen, halfOpenProbe, duration, err)
	b.mu.Unlock()
	b.emit(notify)
}

func (b *Breaker) recordLocked(now time.Time, gen uint64, halfOpenProbe bool, duration time.Duration, err error) *transitionNotification {
	b.updatedAt = now

	excluded := err != nil && b.config.IsExcluded != nil && b.config.IsExcluded(err)
	slow := !excluded && b.isSlowCall(duration)

	if gen != b.generation {
		if b.state == StateClosed {
			if err == nil {
				b.window.recordSuccess(now, slow)
			} else if excluded {
				b.window.recordExcluded(now)
			} else {
				b.lastError = err
				b.window.recordFailure(now, slow)
			}
		}
		return nil
	}

	if halfOpenProbe && b.state == StateHalfOpen {
		if err == nil {
			b.halfOpenSuccesses++
			if b.halfOpenSuccesses >= b.config.HalfOpenMaxProbes {
				return b.transitionToLocked(StateClosed, now)
			}
		} else if excluded {
			if b.halfOpenIssued > 0 {
				b.halfOpenIssued--
			}
			b.window.recordExcluded(now)
		} else {
			b.lastError = err
			return b.transitionToLocked(StateOpen, now)
		}
		return nil
	}

	if b.state == StateClosed {
		if err == nil {
			b.window.recordSuccess(now, slow)
			if slow && b.override != OverrideForceClose && b.shouldTripLocked(now) {
				return b.transitionToLocked(StateOpen, now)
			}
		} else if excluded {
			b.window.recordExcluded(now)
		} else {
			b.lastError = err
			b.window.recordFailure(now, slow)
			if b.override != OverrideForceClose && b.shouldTripLocked(now) {
				return b.transitionToLocked(StateOpen, now)
			}
		}
	}
	return nil
}

func (b *Breaker) slowCallEnabled() bool {
	return b.config.SlowCallThreshold > 0 && b.config.SlowCallRateThresholdBPS > 0
}

func (b *Breaker) isSlowCall(duration time.Duration) bool {
	return b.slowCallEnabled() && duration > b.config.SlowCallThreshold
}

func (b *Breaker) shouldTripLocked(now time.Time) bool {
	successes, failures, _, slowCalls := b.window.totals(now)
	requests := successes + failures
	if requests < uint64(b.config.MinRequests) {
		return false
	}
	if failures*maxBasisPoints >= requests*uint64(b.config.FailureRateThresholdBPS) {
		return true
	}
	if b.slowCallEnabled() && slowCalls*maxBasisPoints >= requests*uint64(b.config.SlowCallRateThresholdBPS) {
		return true
	}
	return false
}

func (b *Breaker) expireOverrideLocked(now time.Time) {
	if b.override != OverrideNone &&
		!b.overrideExpiresAt.IsZero() &&
		!now.Before(b.overrideExpiresAt) {
		b.override = OverrideNone
		b.overrideExpiresAt = time.Time{}
		b.updatedAt = now
	}
}

func (b *Breaker) advanceStateLocked(now time.Time) *transitionNotification {
	if b.state == StateOpen && now.Sub(b.openedAt) >= b.config.OpenDuration {
		return b.transitionToLocked(StateHalfOpen, now)
	}
	return nil
}

func (b *Breaker) transitionToLocked(next State, now time.Time) *transitionNotification {
	prev := b.state
	if prev == next {
		return nil
	}
	b.state = next
	b.generation++
	b.updatedAt = now
	switch next {
	case StateClosed:
		b.openedAt = time.Time{}
		b.halfOpenIssued = 0
		b.halfOpenSuccesses = 0
		b.window.reset(now)
	case StateOpen:
		b.openedAt = now
		b.halfOpenIssued = 0
		b.halfOpenSuccesses = 0
		b.window.reset(now)
	case StateHalfOpen:
		b.halfOpenIssued = 0
		b.halfOpenSuccesses = 0
	}
	return &transitionNotification{
		from: prev,
		to:   next,
		snap: b.snapshotLocked(now),
	}
}

func (b *Breaker) effectiveStateLocked() State {
	switch b.override {
	case OverrideForceOpen, OverrideDraining:
		return StateOpen
	case OverrideForceClose:
		return StateClosed
	default:
		return b.state
	}
}

func (b *Breaker) snapshotLocked(now time.Time) HealthSnapshot {
	successes, failures, excluded, slowCalls := b.window.totals(now)
	requests := successes + failures
	var failureRateBPS uint32
	var slowCallRateBPS uint32
	if requests > 0 {
		failureRateBPS = uint32((failures * maxBasisPoints) / requests)
		slowCallRateBPS = uint32((slowCalls * maxBasisPoints) / requests)
	}
	var cooldownRemaining time.Duration
	if b.state == StateOpen && !b.openedAt.IsZero() {
		if rem := b.config.OpenDuration - now.Sub(b.openedAt); rem > 0 {
			cooldownRemaining = rem
		}
	}
	return HealthSnapshot{
		Key:                  b.key,
		State:                b.state,
		EffectiveState:       b.effectiveStateLocked(),
		Override:             b.override,
		OverrideExpiresAt:    b.overrideExpiresAt,
		Requests:             requests,
		Successes:            successes,
		Failures:             failures,
		Excluded:             excluded,
		BufferedCalls:        requests,
		FailedCalls:          failures,
		SlowCalls:            slowCalls,
		FailureRateBPS:       failureRateBPS,
		SlowCallRateBPS:      slowCallRateBPS,
		CooldownRemaining:    cooldownRemaining,
		OpenedAt:             b.openedAt,
		HalfOpenProbesIssued: b.halfOpenIssued,
		HalfOpenProbeSuccess: b.halfOpenSuccesses,
		LastError:            b.lastError,
		UpdatedAt:            b.updatedAt,
	}
}

func (b *Breaker) emit(notify *transitionNotification) {
	if notify != nil && b.config.OnStateChange != nil {
		b.config.OnStateChange(b.key, notify.from, notify.to, notify.snap)
	}
}

type Registry struct {
	config Config

	mu       sync.RWMutex
	breakers map[string]*Breaker
}

func NewRegistry(config Config) (*Registry, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Registry{
		config:   config.normalized(),
		breakers: make(map[string]*Breaker),
	}, nil
}

func (r *Registry) GetOrCreate(key string) *Breaker {
	r.mu.RLock()
	existing, ok := r.breakers[key]
	r.mu.RUnlock()
	if ok {
		return existing
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok = r.breakers[key]; ok {
		return existing
	}
	created, _ := NewBreaker(key, r.config)
	r.breakers[key] = created
	return created
}

func (r *Registry) Get(key string) (*Breaker, bool) {
	r.mu.RLock()
	breaker, ok := r.breakers[key]
	r.mu.RUnlock()
	return breaker, ok
}

func (r *Registry) SetOverride(key string, mode OverrideMode, expiresAt time.Time) error {
	if mode == "" || mode == OverrideNone {
		r.ClearOverride(key)
		return nil
	}
	if !mode.Valid() {
		return ErrInvalidOverride
	}
	return r.GetOrCreate(key).SetOverride(mode, expiresAt)
}

func (r *Registry) ClearOverride(key string) {
	r.mu.RLock()
	breaker, ok := r.breakers[key]
	r.mu.RUnlock()
	if ok {
		breaker.ClearOverride()
	}
}

func (r *Registry) Probe(key string) {
	r.mu.RLock()
	breaker, ok := r.breakers[key]
	r.mu.RUnlock()
	if ok {
		breaker.Probe()
	}
}

func (r *Registry) Record(key string, duration time.Duration, err error) {
	r.GetOrCreate(key).Record(duration, err)
}

func (r *Registry) Snapshot(key string) (HealthSnapshot, bool) {
	r.mu.RLock()
	breaker, ok := r.breakers[key]
	r.mu.RUnlock()
	if !ok {
		return HealthSnapshot{}, false
	}
	return breaker.Snapshot(), true
}

func (r *Registry) Snapshots() map[string]HealthSnapshot {
	r.mu.RLock()
	items := make([]*Breaker, 0, len(r.breakers))
	for _, b := range r.breakers {
		items = append(items, b)
	}
	r.mu.RUnlock()

	out := make(map[string]HealthSnapshot, len(items))
	for _, b := range items {
		out[b.Key()] = b.Snapshot()
	}
	return out
}
