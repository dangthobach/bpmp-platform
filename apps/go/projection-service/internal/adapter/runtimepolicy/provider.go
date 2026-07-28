package runtimepolicy

import (
	"errors"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
)

type Provider struct {
	cache *runtimeconfig.Cache
}

func New(cache *runtimeconfig.Cache) (*Provider, error) {
	if cache == nil {
		return nil, errors.New("projection runtime policy provider is invalid")
	}
	return &Provider{cache: cache}, nil
}

func (p *Provider) Policy(tenantID string) (application.RuntimePolicy, error) {
	if tenantID == "" {
		return application.RuntimePolicy{}, errors.New("projection runtime policy tenant is required")
	}
	snapshot, err := p.cache.Get(tenantID)
	if err != nil {
		return application.RuntimePolicy{}, err
	}
	value := snapshot.GetProjection()
	if value == nil || value.GetConsumeBatchSize() == 0 ||
		value.GetQueryDefaultPageSize() == 0 ||
		value.GetQueryMaxPageSize() < value.GetQueryDefaultPageSize() {
		return application.RuntimePolicy{}, errors.New("projection runtime policy is invalid")
	}
	return application.RuntimePolicy{
		ConsumeBatchSize:     int(value.GetConsumeBatchSize()),
		QueryDefaultPageSize: value.GetQueryDefaultPageSize(),
		QueryMaxPageSize:     value.GetQueryMaxPageSize(),
	}, nil
}

func (p *Provider) MinimumConsumeBatchSize(tenantIDs []string) (int, error) {
	minimum := 0
	for _, tenantID := range tenantIDs {
		policy, err := p.Policy(tenantID)
		if err != nil {
			return 0, err
		}
		if minimum == 0 || policy.ConsumeBatchSize < minimum {
			minimum = policy.ConsumeBatchSize
		}
	}
	if minimum == 0 {
		return 0, errors.New("projection consume batch policy is empty")
	}
	return minimum, nil
}
