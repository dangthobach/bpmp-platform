package runtimepolicy

import (
	"errors"
	"math"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

type Policy struct {
	ProjectionBatchSize     int
	EscalationBatchSize     int
	EscalationLease         time.Duration
	EscalationRetry         time.Duration
	EscalationPoll          time.Duration
	EngineCommandTimeout    time.Duration
	MaxAssignmentCandidates uint32
	MaxDelegationDepth      uint32
}

type Provider struct {
	cache    *runtimeconfig.Cache
	tenantID string
}

func New(cache *runtimeconfig.Cache, tenantID string) (*Provider, error) {
	if cache == nil || tenantID == "" {
		return nil, errors.New("human runtime policy provider is invalid")
	}
	return &Provider{cache: cache, tenantID: tenantID}, nil
}

func (p *Provider) Policy() (Policy, error) {
	snapshot, err := p.cache.Get(p.tenantID)
	if err != nil {
		return Policy{}, err
	}
	value := snapshot.GetHumanRuntime()
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
		value.GetEngineCommandTimeoutMs() > maxDurationMillis {
		return Policy{}, errors.New("human runtime policy is invalid")
	}
	return Policy{
		ProjectionBatchSize:     int(value.GetProjectionBatchSize()),
		EscalationBatchSize:     int(value.GetEscalationBatchSize()),
		EscalationLease:         time.Duration(value.GetEscalationLeaseMs()) * time.Millisecond,
		EscalationRetry:         time.Duration(value.GetEscalationRetryMs()) * time.Millisecond,
		EscalationPoll:          time.Duration(value.GetEscalationPollMs()) * time.Millisecond,
		EngineCommandTimeout:    time.Duration(value.GetEngineCommandTimeoutMs()) * time.Millisecond,
		MaxAssignmentCandidates: value.GetMaxAssignmentCandidates(),
		MaxDelegationDepth:      value.GetMaxDelegationDepth(),
	}, nil
}
