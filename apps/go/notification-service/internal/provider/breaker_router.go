package provider

import (
	"context"
	"errors"
	"sync"
	"time"
)

type BreakerState string

const (
	BreakerClosed   BreakerState = "CLOSED"
	BreakerOpen     BreakerState = "OPEN"
	BreakerHalfOpen BreakerState = "HALF_OPEN"
)

type OverrideMode string

const (
	OverrideNone       OverrideMode = ""
	OverrideForceOpen  OverrideMode = "FORCE_OPEN"
	OverrideForceClose OverrideMode = "FORCE_CLOSE"
	OverrideDraining   OverrideMode = "DRAINING"
)

type BreakerPolicy struct {
	RollingWindow            time.Duration
	MinRequests              uint32
	FailureRateThresholdBps  uint32
	SlowCallThreshold        time.Duration
	SlowCallRateThresholdBps uint32
	OpenStateDuration        time.Duration
	HalfOpenMaxProbes        uint32
	MaxManualOverrideTTL     time.Duration
}

func DefaultBreakerPolicy() BreakerPolicy {
	return BreakerPolicy{
		RollingWindow:            60 * time.Second,
		MinRequests:              5,
		FailureRateThresholdBps:  5000,
		SlowCallThreshold:        2 * time.Second,
		SlowCallRateThresholdBps: 5000,
		OpenStateDuration:        30 * time.Second,
		HalfOpenMaxProbes:        2,
		MaxManualOverrideTTL:     4 * time.Hour,
	}
}

type sample struct {
	at      time.Time
	failed  bool
	slow    bool
	latency time.Duration
}

type ManualOverride struct {
	Mode      OverrideMode
	Reason    string
	Ordinal   uint64
	ExpiresAt time.Time
}

type BreakerTransitionEvent struct {
	FromState       BreakerState
	ToState         BreakerState
	TriggerKind     string
	Reason          string
	ErrorRateBps    uint32
	SlowCallRateBps uint32
	OccurredAt      time.Time
}

type BreakerSnapshot struct {
	BreakerState        BreakerState
	ErrorRateBps        uint32
	SlowCallRateBps     uint32
	SuccessRateBps      uint32
	AvgLatencyMS        uint32
	WindowRequests      uint32
	WindowFailures      uint32
	BufferedCalls       uint32
	FailedCalls         uint32
	SlowCalls           uint32
	CooldownRemainingMS uint64
	ActiveOverride      OverrideMode
	OverrideExpiresAt   time.Time
	LastTransitionAt    time.Time
	LastErrorCode       string
}

type providerBreaker struct {
	state            BreakerState
	openedAt         time.Time
	lastTransitionAt time.Time
	halfOpenInFlight uint32
	halfOpenSuccess  uint32
	samples          []sample
	lastErrorCode    string
	override         *ManualOverride
}

type BreakerRegistry struct {
	mu           sync.Mutex
	policy       BreakerPolicy
	breakers     map[string]*providerBreaker
	onTransition func(tenantID, providerID string, ev BreakerTransitionEvent)
}

func NewBreakerRegistry(policy BreakerPolicy) *BreakerRegistry {
	if policy.RollingWindow <= 0 {
		policy = DefaultBreakerPolicy()
	}
	return &BreakerRegistry{
		policy:   policy,
		breakers: make(map[string]*providerBreaker),
	}
}

func (r *BreakerRegistry) SetTransitionCallback(fn func(tenantID, providerID string, ev BreakerTransitionEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onTransition = fn
}

func breakerKey(tenantID, providerID string) string {
	return tenantID + ":" + providerID
}

func (r *BreakerRegistry) getOrCreateLocked(tenantID, providerID string, now time.Time) *providerBreaker {
	key := breakerKey(tenantID, providerID)
	b := r.breakers[key]
	if b == nil {
		b = &providerBreaker{
			state:            BreakerClosed,
			lastTransitionAt: now,
		}
		r.breakers[key] = b
	}
	r.advanceTimersLocked(b, now)
	return b
}

func (r *BreakerRegistry) advanceTimersLocked(b *providerBreaker, now time.Time) {
	if b.override != nil && !now.Before(b.override.ExpiresAt) {
		b.override = nil
	}
	cutoff := now.Add(-r.policy.RollingWindow)
	idx := 0
	for idx < len(b.samples) && b.samples[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		b.samples = append([]sample(nil), b.samples[idx:]...)
	}
	if b.state == BreakerOpen && !now.Before(b.openedAt.Add(r.policy.OpenStateDuration)) {
		b.state = BreakerHalfOpen
		b.halfOpenInFlight = 0
		b.halfOpenSuccess = 0
		b.lastTransitionAt = now
	}
}

// Allow checks whether traffic can be sent to (tenantID, providerID).
// When includeDraining is false, DRAINING providers are skipped so primary
// traffic drains gracefully.
func (r *BreakerRegistry) Allow(tenantID, providerID string, includeDraining bool, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.getOrCreateLocked(tenantID, providerID, now)
	if b.override != nil {
		switch b.override.Mode {
		case OverrideForceOpen:
			return false
		case OverrideForceClose:
			return true
		case OverrideDraining:
			if !includeDraining {
				return false
			}
		}
	}
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		return false
	case BreakerHalfOpen:
		if b.halfOpenInFlight >= r.policy.HalfOpenMaxProbes {
			return false
		}
		b.halfOpenInFlight++
		return true
	default:
		return true
	}
}

func (r *BreakerRegistry) RecordOutcome(tenantID, providerID string, outcome OutcomeClass, errorCode string, now time.Time) BreakerSnapshot {
	snap, _ := r.RecordOutcomeWithLatency(tenantID, providerID, outcome, errorCode, 0, now)
	return snap
}

func (r *BreakerRegistry) RecordOutcomeWithLatency(tenantID, providerID string, outcome OutcomeClass, errorCode string, latency time.Duration, now time.Time) (BreakerSnapshot, *BreakerTransitionEvent) {
	r.mu.Lock()
	b := r.getOrCreateLocked(tenantID, providerID, now)

	// Recipient/data PERMANENT errors are excluded from provider health so a bad
	// address batch never trips a healthy provider's circuit breaker (ADR-013 §9).
	if outcome == OutcomePermanent {
		if b.state == BreakerHalfOpen && b.halfOpenInFlight > 0 {
			b.halfOpenInFlight--
		}
		snap := r.snapshotLocked(b, now)
		r.mu.Unlock()
		return snap, nil
	}

	failed := outcome != OutcomeOK
	slow := r.policy.SlowCallThreshold > 0 && latency >= r.policy.SlowCallThreshold
	if failed && errorCode != "" {
		b.lastErrorCode = errorCode
	}
	b.samples = append(b.samples, sample{at: now, failed: failed, slow: slow, latency: latency})

	var transition *BreakerTransitionEvent
	prevState := b.state
	switch b.state {
	case BreakerHalfOpen:
		if b.halfOpenInFlight > 0 {
			b.halfOpenInFlight--
		}
		if failed {
			b.state = BreakerOpen
			b.openedAt = now
			b.halfOpenInFlight = 0
			b.halfOpenSuccess = 0
			b.lastTransitionAt = now
			_, _, errBps, _, slowBps, _ := windowStats(b.samples)
			transition = &BreakerTransitionEvent{
				FromState: prevState, ToState: BreakerOpen, TriggerKind: "AUTO_PROBE_FAILURE",
				Reason: "half-open probe failed: " + errorCode, ErrorRateBps: errBps, SlowCallRateBps: slowBps, OccurredAt: now,
			}
		} else {
			b.halfOpenSuccess++
			if b.halfOpenSuccess >= r.policy.HalfOpenMaxProbes {
				b.state = BreakerClosed
				b.samples = nil
				b.halfOpenInFlight = 0
				b.halfOpenSuccess = 0
				b.lastTransitionAt = now
				transition = &BreakerTransitionEvent{
					FromState: prevState, ToState: BreakerClosed, TriggerKind: "AUTO_PROBE_SUCCESS",
					Reason: "half-open probes succeeded", OccurredAt: now,
				}
			}
		}
	case BreakerClosed:
		total, _, rateBps, _, slowRateBps, _ := windowStats(b.samples)
		if total >= r.policy.MinRequests && rateBps >= r.policy.FailureRateThresholdBps {
			b.state = BreakerOpen
			b.openedAt = now
			b.lastTransitionAt = now
			transition = &BreakerTransitionEvent{
				FromState: prevState, ToState: BreakerOpen, TriggerKind: "AUTO_FAILURE_RATE",
				Reason: "failure rate threshold exceeded", ErrorRateBps: rateBps, SlowCallRateBps: slowRateBps, OccurredAt: now,
			}
		} else if total >= r.policy.MinRequests && r.policy.SlowCallRateThresholdBps > 0 && slowRateBps >= r.policy.SlowCallRateThresholdBps {
			b.state = BreakerOpen
			b.openedAt = now
			b.lastTransitionAt = now
			transition = &BreakerTransitionEvent{
				FromState: prevState, ToState: BreakerOpen, TriggerKind: "AUTO_SLOW_CALL_RATE",
				Reason: "slow-call rate threshold exceeded", ErrorRateBps: rateBps, SlowCallRateBps: slowRateBps, OccurredAt: now,
			}
		}
	}
	snap := r.snapshotLocked(b, now)
	cb := r.onTransition
	r.mu.Unlock()
	if transition != nil && cb != nil {
		cb(tenantID, providerID, *transition)
	}
	return snap, transition
}

// Probe clears any manual override and transitions the breaker into HALF_OPEN
// so controlled probe traffic can test provider recovery.
func (r *BreakerRegistry) Probe(tenantID, providerID, reason string, now time.Time) (BreakerSnapshot, *BreakerTransitionEvent) {
	r.mu.Lock()
	b := r.getOrCreateLocked(tenantID, providerID, now)
	prevState := b.state
	b.override = nil
	b.state = BreakerHalfOpen
	b.halfOpenInFlight = 0
	b.halfOpenSuccess = 0
	b.lastTransitionAt = now
	_, _, errBps, _, slowBps, _ := windowStats(b.samples)
	if reason == "" {
		reason = "manual probe requested"
	}
	ev := &BreakerTransitionEvent{
		FromState: prevState, ToState: BreakerHalfOpen, TriggerKind: "MANUAL_PROBE",
		Reason: reason, ErrorRateBps: errBps, SlowCallRateBps: slowBps, OccurredAt: now,
	}
	snap := r.snapshotLocked(b, now)
	cb := r.onTransition
	r.mu.Unlock()
	if cb != nil {
		cb(tenantID, providerID, *ev)
	}
	return snap, ev
}

func (r *BreakerRegistry) SetOverride(tenantID, providerID string, mode OverrideMode, reason string, ttl time.Duration, ordinal uint64, now time.Time) (BreakerSnapshot, error) {
	if ttl <= 0 || ttl > r.policy.MaxManualOverrideTTL {
		return BreakerSnapshot{}, errors.New("breaker override TTL is outside policy bounds")
	}
	if mode != OverrideForceOpen && mode != OverrideForceClose && mode != OverrideDraining {
		return BreakerSnapshot{}, errors.New("breaker override mode is invalid")
	}
	if reason == "" {
		return BreakerSnapshot{}, errors.New("breaker override reason is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.getOrCreateLocked(tenantID, providerID, now)
	b.override = &ManualOverride{
		Mode:      mode,
		Reason:    reason,
		Ordinal:   ordinal,
		ExpiresAt: now.Add(ttl),
	}
	return r.snapshotLocked(b, now), nil
}

func (r *BreakerRegistry) ClearOverride(tenantID, providerID string, now time.Time) BreakerSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.getOrCreateLocked(tenantID, providerID, now)
	b.override = nil
	return r.snapshotLocked(b, now)
}

func (r *BreakerRegistry) Snapshot(tenantID, providerID string, now time.Time) BreakerSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.getOrCreateLocked(tenantID, providerID, now)
	return r.snapshotLocked(b, now)
}

func (r *BreakerRegistry) snapshotLocked(b *providerBreaker, now time.Time) BreakerSnapshot {
	total, failures, rateBps, slowCalls, slowRateBps, avgLatencyMS := windowStats(b.samples)
	successBps := uint32(10000)
	if total > 0 {
		successBps = 10000 - rateBps
	}
	var cooldownMS uint64
	if b.state == BreakerOpen {
		rem := b.openedAt.Add(r.policy.OpenStateDuration).Sub(now)
		if rem > 0 {
			cooldownMS = uint64(rem.Milliseconds())
		}
	}
	snap := BreakerSnapshot{
		BreakerState:        b.state,
		ErrorRateBps:        rateBps,
		SlowCallRateBps:     slowRateBps,
		SuccessRateBps:      successBps,
		AvgLatencyMS:        avgLatencyMS,
		WindowRequests:      total,
		WindowFailures:      failures,
		BufferedCalls:       total,
		FailedCalls:         failures,
		SlowCalls:           slowCalls,
		CooldownRemainingMS: cooldownMS,
		LastTransitionAt:    b.lastTransitionAt,
		LastErrorCode:       b.lastErrorCode,
	}
	if b.override != nil {
		snap.ActiveOverride = b.override.Mode
		snap.OverrideExpiresAt = b.override.ExpiresAt
	}
	return snap
}

func windowStats(samples []sample) (total, failures, rateBps, slowCalls, slowRateBps, avgLatencyMS uint32) {
	total = uint32(len(samples))
	if total == 0 {
		return 0, 0, 0, 0, 0, 0
	}
	var totalLatencyMS uint64
	for _, s := range samples {
		if s.failed {
			failures++
		}
		if s.slow {
			slowCalls++
		}
		totalLatencyMS += uint64(s.latency.Milliseconds())
	}
	rateBps = uint32((uint64(failures) * 10000) / uint64(total))
	slowRateBps = uint32((uint64(slowCalls) * 10000) / uint64(total))
	avgLatencyMS = uint32(totalLatencyMS / uint64(total))
	return total, failures, rateBps, slowCalls, slowRateBps, avgLatencyMS
}

type RouteCandidate struct {
	ProviderID string
	Revision   ProviderRevision
	Weight     uint32
}

type RouteTier struct {
	Candidates []RouteCandidate
}

type AttemptRecord struct {
	ProviderID         string
	ProviderRevisionID string
	AttemptNo          int
	Result             SendResult
	Err                error
}

type DispatchOutcome string

const (
	DispatchSent               DispatchOutcome = "SENT"
	DispatchDeferred           DispatchOutcome = "DEFERRED"
	DispatchReconcileRequired  DispatchOutcome = "DEFERRED_RECONCILE"
	DispatchFailedPermanent    DispatchOutcome = "FAILED_PERMANENT"
	DispatchFailedRetryable    DispatchOutcome = "FAILED_RETRYABLE"
	DispatchFailedAmbiguous    DispatchOutcome = "FAILED_AMBIGUOUS"
)

type RouteExecutionResult struct {
	Outcome            DispatchOutcome
	SelectedProviderID string
	ProviderMessageID  string
	Attempts           []AttemptRecord
}

type Router struct {
	drivers  map[string]Driver
	breakers *BreakerRegistry
	secrets  SecretResolver
	now      func() time.Time
}

func NewRouter(drivers []Driver, breakers *BreakerRegistry, secrets SecretResolver, now func() time.Time) *Router {
	dm := make(map[string]Driver, len(drivers))
	for _, d := range drivers {
		dm[d.Kind()] = d
	}
	if breakers == nil {
		breakers = NewBreakerRegistry(DefaultBreakerPolicy())
	}
	if now == nil {
		now = time.Now
	}
	return &Router{
		drivers:  dm,
		breakers: breakers,
		secrets:  secrets,
		now:      now,
	}
}

func (r *Router) Breakers() *BreakerRegistry {
	return r.breakers
}

func (r *Router) Dispatch(
	ctx context.Context,
	tiers []RouteTier,
	ambiguousPolicy AmbiguousPolicy,
	selectionSeed uint64,
	msg RenderedMessage,
) RouteExecutionResult {
	now := r.now().UTC()
	ordered := r.selectHealthyCandidates(msg.TenantID, tiers, selectionSeed, false, now)
	if len(ordered) == 0 {
		// Fall back to DRAINING providers only if no non-draining healthy route exists.
		ordered = r.selectHealthyCandidates(msg.TenantID, tiers, selectionSeed, true, now)
	}
	if len(ordered) == 0 {
		return RouteExecutionResult{Outcome: DispatchDeferred}
	}

	attempts := make([]AttemptRecord, 0, len(ordered))
	for i, cand := range ordered {
		drv, ok := r.drivers[cand.Revision.DriverKind]
		if !ok {
			res := SendResult{Outcome: OutcomeRetryable, ErrorCode: "UNKNOWN_DRIVER"}
			r.breakers.RecordOutcome(msg.TenantID, cand.ProviderID, res.Outcome, res.ErrorCode, r.now().UTC())
			attempts = append(attempts, AttemptRecord{
				ProviderID: cand.ProviderID, ProviderRevisionID: cand.Revision.RevisionID,
				AttemptNo: i + 1, Result: res, Err: errors.New("driver not registered"),
			})
			continue
		}
		sender, openErr := drv.Open(ctx, cand.Revision, r.secrets)
		if openErr != nil {
			res := SendResult{Outcome: OutcomeAuthFailed, ErrorCode: "DRIVER_OPEN_FAILED"}
			r.breakers.RecordOutcome(msg.TenantID, cand.ProviderID, res.Outcome, res.ErrorCode, r.now().UTC())
			attempts = append(attempts, AttemptRecord{
				ProviderID: cand.ProviderID, ProviderRevisionID: cand.Revision.RevisionID,
				AttemptNo: i + 1, Result: res, Err: openErr,
			})
			continue
		}
		res, sendErr := sender.Send(ctx, msg)
		_ = sender.Close()
		r.breakers.RecordOutcomeWithLatency(msg.TenantID, cand.ProviderID, res.Outcome, res.ErrorCode, res.Latency, r.now().UTC())
		attempts = append(attempts, AttemptRecord{
			ProviderID: cand.ProviderID, ProviderRevisionID: cand.Revision.RevisionID,
			AttemptNo: i + 1, Result: res, Err: sendErr,
		})

		switch res.Outcome {
		case OutcomeOK:
			return RouteExecutionResult{
				Outcome:            DispatchSent,
				SelectedProviderID: cand.ProviderID,
				ProviderMessageID:  res.ProviderMessageID,
				Attempts:           attempts,
			}
		case OutcomePermanent:
			// Never fail over on PERMANENT recipient/data errors (ADR-013 §8.2).
			return RouteExecutionResult{
				Outcome:            DispatchFailedPermanent,
				SelectedProviderID: cand.ProviderID,
				Attempts:           attempts,
			}
		case OutcomeAmbiguous:
			switch ambiguousPolicy {
			case AmbiguousResendOnFailover:
				continue
			case AmbiguousReconcileBeforeRetry:
				return RouteExecutionResult{
					Outcome:            DispatchReconcileRequired,
					SelectedProviderID: cand.ProviderID,
					Attempts:           attempts,
				}
			default:
				return RouteExecutionResult{
					Outcome:            DispatchFailedAmbiguous,
					SelectedProviderID: cand.ProviderID,
					Attempts:           attempts,
				}
			}
		default:
			// RETRYABLE, THROTTLED, AUTH_FAILED -> fail over to next candidate.
			continue
		}
	}
	return RouteExecutionResult{
		Outcome:            DispatchFailedRetryable,
		SelectedProviderID: attempts[len(attempts)-1].ProviderID,
		Attempts:           attempts,
	}
}

type PlannedCandidate struct {
	ProviderID   string `json:"provider_id"`
	ProviderKey  string `json:"provider_key"`
	BreakerState string `json:"breaker_state"`
	Healthy      bool   `json:"healthy"`
}

// PlanRoute inspects routing tiers and breaker snapshots without mutating any
// breaker state (used for dry-run / PlanNotification).
func (r *Router) PlanRoute(tenantID string, tiers []RouteTier, now time.Time) (candidates []PlannedCandidate, selectedProviderID, failoverReason string) {
	var skippedPrimary string
	for tierIdx, tier := range tiers {
		for _, c := range tier.Candidates {
			snap := r.breakers.Snapshot(tenantID, c.ProviderID, now)
			healthy := snap.BreakerState != BreakerOpen
			if snap.ActiveOverride == OverrideForceOpen {
				healthy = false
			} else if snap.ActiveOverride == OverrideForceClose {
				healthy = true
			} else if snap.ActiveOverride == OverrideDraining {
				healthy = false
			}
			candidates = append(candidates, PlannedCandidate{
				ProviderID:   c.ProviderID,
				ProviderKey:  c.Revision.ProviderKey,
				BreakerState: string(snap.BreakerState),
				Healthy:      healthy,
			})
			if healthy && selectedProviderID == "" {
				selectedProviderID = c.ProviderID
				if tierIdx > 0 || skippedPrimary != "" {
					if skippedPrimary != "" {
						failoverReason = "primary provider " + skippedPrimary + " unavailable"
					} else {
						failoverReason = "failed over to secondary tier"
					}
				}
			} else if !healthy && skippedPrimary == "" {
				skippedPrimary = c.Revision.ProviderKey
			}
		}
	}
	if selectedProviderID == "" && len(candidates) > 0 {
		failoverReason = "no healthy provider available; delivery would be DEFERRED"
	}
	return candidates, selectedProviderID, failoverReason
}

func (r *Router) selectHealthyCandidates(tenantID string, tiers []RouteTier, seed uint64, includeDraining bool, now time.Time) []RouteCandidate {
	var out []RouteCandidate
	for _, tier := range tiers {
		healthy := make([]RouteCandidate, 0, len(tier.Candidates))
		var totalWeight uint64
		for _, c := range tier.Candidates {
			if !r.breakers.Allow(tenantID, c.ProviderID, includeDraining, now) {
				continue
			}
			w := c.Weight
			if w == 0 {
				w = 1
			}
			c.Weight = w
			healthy = append(healthy, c)
			totalWeight += uint64(w)
		}
		if len(healthy) == 0 {
			continue
		}
		if len(healthy) == 1 {
			out = append(out, healthy[0])
			continue
		}
		pick := seed % totalWeight
		var acc uint64
		primaryIdx := 0
		for idx, c := range healthy {
			acc += uint64(c.Weight)
			if pick < acc {
				primaryIdx = idx
				break
			}
		}
		out = append(out, healthy[primaryIdx])
		for idx, c := range healthy {
			if idx != primaryIdx {
				out = append(out, c)
			}
		}
	}
	return out
}
