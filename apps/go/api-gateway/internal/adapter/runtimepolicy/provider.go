package runtimepolicy

import (
	"encoding/hex"
	"errors"
	"math"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/gateway"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
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
	return mapPolicy(snapshot, err)
}

func (p *Provider) PolicyForInstance(tenantID, instanceID string) (gateway.RuntimePolicy, error) {
	snapshot, err := p.cache.GetForInstance(tenantID, instanceID)
	return mapPolicy(snapshot, err)
}

func mapPolicy(
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
	err error,
) (gateway.RuntimePolicy, error) {
	if err != nil {
		return gateway.RuntimePolicy{}, err
	}
	policy := snapshot.GetApiGateway()
	retry := policy.GetUpstreamRetry()
	const maxDurationMillis = uint64(math.MaxInt64 / int64(time.Millisecond))
	if policy == nil ||
		policy.GetRateLimitRequests() == 0 ||
		policy.GetRateLimitWindowMs() == 0 ||
		policy.GetRateLimitWindowMs() > maxDurationMillis ||
		policy.GetUpstreamTimeoutMs() == 0 ||
		policy.GetUpstreamTimeoutMs() > maxDurationMillis ||
		policy.GetCircuitBreakerFailureThreshold() == 0 ||
		policy.GetCircuitBreakerOpenMs() == 0 ||
		policy.GetCircuitBreakerOpenMs() > maxDurationMillis ||
		policy.GetBulkheadMaxConcurrency() == 0 ||
		policy.GetMaxRequestBodyBytes() == 0 ||
		policy.GetMaxRequestBodyBytes() > math.MaxInt64 ||
		policy.GetMaxUpstreamResponseBytes() == 0 ||
		policy.GetMaxUpstreamResponseBytes() > math.MaxInt64 ||
		policy.GetBatchChunkSize() == 0 ||
		policy.GetBatchConcurrency() == 0 ||
		policy.GetBatchConcurrency() > policy.GetBatchChunkSize() ||
		retry.GetMaxAttempts() == 0 ||
		retry.GetInitialBackoffMs() == 0 ||
		retry.GetInitialBackoffMs() > maxDurationMillis ||
		retry.GetMaxBackoffMs() < retry.GetInitialBackoffMs() ||
		retry.GetMaxBackoffMs() > maxDurationMillis ||
		retry.GetMultiplierMillis() < 1000 ||
		policy.GetEncryptionKeyScope() == "" {
		return gateway.RuntimePolicy{}, errors.New("API Gateway runtime policy is invalid")
	}
	return gateway.RuntimePolicy{
		ConfigVersion:                  snapshot.GetConfigVersion(),
		PolicyVersion:                  snapshot.GetPolicyVersion(),
		ContentETag:                    hex.EncodeToString(snapshot.GetContentHash()),
		RateLimitRequests:              policy.GetRateLimitRequests(),
		RateLimitWindow:                time.Duration(policy.GetRateLimitWindowMs()) * time.Millisecond,
		UpstreamTimeout:                time.Duration(policy.GetUpstreamTimeoutMs()) * time.Millisecond,
		CircuitBreakerFailureThreshold: policy.GetCircuitBreakerFailureThreshold(),
		CircuitBreakerOpen:             time.Duration(policy.GetCircuitBreakerOpenMs()) * time.Millisecond,
		BulkheadMaxConcurrency:         policy.GetBulkheadMaxConcurrency(),
		MaxRequestBodyBytes:            int64(policy.GetMaxRequestBodyBytes()),
		MaxUpstreamResponseBytes:       int64(policy.GetMaxUpstreamResponseBytes()),
		BatchChunkSize:                 policy.GetBatchChunkSize(),
		BatchConcurrency:               policy.GetBatchConcurrency(),
		UpstreamRetry: gateway.RetryPolicy{
			MaxAttempts:      retry.GetMaxAttempts(),
			InitialBackoff:   time.Duration(retry.GetInitialBackoffMs()) * time.Millisecond,
			MaxBackoff:       time.Duration(retry.GetMaxBackoffMs()) * time.Millisecond,
			MultiplierMillis: retry.GetMultiplierMillis(),
		},
		EncryptionKeyScope: policy.GetEncryptionKeyScope(),
	}, nil
}
