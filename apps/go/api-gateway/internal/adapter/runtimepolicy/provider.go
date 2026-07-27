package runtimepolicy

import (
	"errors"
	"math"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/gateway"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

type Provider struct {
	cache *runtimeconfig.Cache
}

func New(cache *runtimeconfig.Cache) (*Provider, error) {
	if cache == nil {
		return nil, errors.New("runtime configuration cache is required")
	}
	return &Provider{cache: cache}, nil
}

func (p *Provider) Policy(tenantID string) (gateway.RuntimePolicy, error) {
	snapshot, err := p.cache.Get(tenantID)
	if err != nil {
		return gateway.RuntimePolicy{}, err
	}
	policy := snapshot.GetApiGateway()
	const maxDurationMillis = uint64(math.MaxInt64 / int64(time.Millisecond))
	if policy == nil ||
		policy.GetRateLimitRequests() == 0 ||
		policy.GetRateLimitWindowMs() == 0 ||
		policy.GetRateLimitWindowMs() > maxDurationMillis ||
		policy.GetMaxRequestBodyBytes() == 0 ||
		policy.GetMaxRequestBodyBytes() > math.MaxInt64 ||
		policy.GetMaxUpstreamResponseBytes() == 0 ||
		policy.GetMaxUpstreamResponseBytes() > math.MaxInt64 {
		return gateway.RuntimePolicy{}, errors.New("API Gateway runtime policy is invalid")
	}
	return gateway.RuntimePolicy{
		RateLimitRequests:        policy.GetRateLimitRequests(),
		RateLimitWindow:          time.Duration(policy.GetRateLimitWindowMs()) * time.Millisecond,
		MaxRequestBodyBytes:      int64(policy.GetMaxRequestBodyBytes()),
		MaxUpstreamResponseBytes: int64(policy.GetMaxUpstreamResponseBytes()),
	}, nil
}
