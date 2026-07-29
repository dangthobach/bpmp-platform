package runtimepolicy

import (
	"errors"
	"math"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

type Provider struct {
	cache *runtimeconfig.Cache
}

func New(cache *runtimeconfig.Cache) (*Provider, error) {
	if cache == nil {
		return nil, errors.New("human runtime policy provider is invalid")
	}
	return &Provider{cache: cache}, nil
}

func (p *Provider) Policy(tenantID string) (application.RuntimePolicy, error) {
	if tenantID == "" {
		return application.RuntimePolicy{}, errors.New("human runtime policy tenant is required")
	}
	snapshot, err := p.cache.Get(tenantID)
	if err != nil {
		return application.RuntimePolicy{}, err
	}
	value := snapshot.GetHumanRuntime()
	retry := value.GetEngineRetry()
	const maxDurationMillis = uint64(math.MaxInt64 / int64(time.Millisecond))
	if value == nil ||
		value.GetProjectionBatchSize() == 0 ||
		value.GetEscalationBatchSize() == 0 ||
		value.GetEscalationLeaseMs() == 0 ||
		value.GetEscalationRetryMs() == 0 ||
		value.GetEscalationPollMs() == 0 ||
		value.GetEngineCommandTimeoutMs() == 0 ||
		value.GetEscalationLeaseMs() > maxDurationMillis ||
		value.GetEscalationRetryMs() > maxDurationMillis ||
		value.GetEscalationPollMs() > maxDurationMillis ||
		value.GetEngineCommandTimeoutMs() > maxDurationMillis ||
		value.GetMaxAssignmentCandidates() == 0 ||
		value.GetMaxDelegationDepth() == 0 ||
		value.GetQueryDefaultPageSize() == 0 ||
		value.GetQueryMaxPageSize() < value.GetQueryDefaultPageSize() ||
		retry.GetMaxAttempts() == 0 ||
		retry.GetInitialBackoffMs() == 0 ||
		retry.GetMaxBackoffMs() < retry.GetInitialBackoffMs() ||
		retry.GetInitialBackoffMs() > maxDurationMillis ||
		retry.GetMaxBackoffMs() > maxDurationMillis ||
		retry.GetMultiplierMillis() < 1000 ||
		value.GetEngineCircuitBreakerFailureThreshold() == 0 ||
		value.GetEngineCircuitBreakerOpenMs() == 0 ||
		value.GetEngineCircuitBreakerOpenMs() > maxDurationMillis ||
		len(value.GetEngineRetryableCodes()) == 0 {
		return application.RuntimePolicy{}, errors.New("human runtime policy is invalid")
	}
	return application.RuntimePolicy{
		ProjectionBatchSize:     int(value.GetProjectionBatchSize()),
		EscalationBatchSize:     int(value.GetEscalationBatchSize()),
		EscalationLease:         time.Duration(value.GetEscalationLeaseMs()) * time.Millisecond,
		EscalationRetry:         time.Duration(value.GetEscalationRetryMs()) * time.Millisecond,
		EscalationPoll:          time.Duration(value.GetEscalationPollMs()) * time.Millisecond,
		EngineCommandTimeout:    time.Duration(value.GetEngineCommandTimeoutMs()) * time.Millisecond,
		MaxAssignmentCandidates: value.GetMaxAssignmentCandidates(),
		MaxDelegationDepth:      value.GetMaxDelegationDepth(),
		QueryDefaultPageSize:    value.GetQueryDefaultPageSize(),
		QueryMaxPageSize:        value.GetQueryMaxPageSize(),
		EngineRetry: application.RetryPolicy{
			MaxAttempts:      retry.GetMaxAttempts(),
			InitialBackoff:   time.Duration(retry.GetInitialBackoffMs()) * time.Millisecond,
			MaxBackoff:       time.Duration(retry.GetMaxBackoffMs()) * time.Millisecond,
			MultiplierMillis: retry.GetMultiplierMillis(),
		},
		EngineCircuitThreshold: value.GetEngineCircuitBreakerFailureThreshold(),
		EngineCircuitOpen:      time.Duration(value.GetEngineCircuitBreakerOpenMs()) * time.Millisecond,
		EngineRetryableCodes:   append([]string(nil), value.GetEngineRetryableCodes()...),
	}, nil
}

func (p *Provider) WorkerPolicy() (application.RuntimePolicy, error) {
	tenantIDs := p.cache.TenantIDs()
	if len(tenantIDs) == 0 {
		return application.RuntimePolicy{}, errors.New("human runtime worker policy is empty")
	}
	var resolved application.RuntimePolicy
	for index, tenantID := range tenantIDs {
		policy, err := p.Policy(tenantID)
		if err != nil {
			return application.RuntimePolicy{}, err
		}
		if index == 0 {
			resolved = policy
			continue
		}
		resolved.ProjectionBatchSize = min(resolved.ProjectionBatchSize, policy.ProjectionBatchSize)
		resolved.EscalationBatchSize = min(resolved.EscalationBatchSize, policy.EscalationBatchSize)
		resolved.EscalationLease = min(resolved.EscalationLease, policy.EscalationLease)
		resolved.EscalationRetry = min(resolved.EscalationRetry, policy.EscalationRetry)
		resolved.EscalationPoll = min(resolved.EscalationPoll, policy.EscalationPoll)
	}
	return resolved, nil
}
