package runtimepolicy

import (
	"errors"
	"time"

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
		value.GetRebuildBatchSize() == 0 ||
		value.GetQueryDefaultPageSize() == 0 ||
		value.GetQueryMaxPageSize() < value.GetQueryDefaultPageSize() ||
		value.GetRealtimePublishBatchSize() == 0 ||
		value.GetCheckpointFlushMs() == 0 ||
		value.GetMaxProjectionLagMs() == 0 {
		return application.RuntimePolicy{}, errors.New("projection runtime policy is invalid")
	}
	return application.RuntimePolicy{
		ConsumeBatchSize:         int(value.GetConsumeBatchSize()),
		RebuildBatchSize:         int(value.GetRebuildBatchSize()),
		QueryDefaultPageSize:     value.GetQueryDefaultPageSize(),
		QueryMaxPageSize:         value.GetQueryMaxPageSize(),
		RealtimePublishBatchSize: int(value.GetRealtimePublishBatchSize()),
		CheckpointFlush:          time.Duration(value.GetCheckpointFlushMs()) * time.Millisecond,
		MaxProjectionLag:         time.Duration(value.GetMaxProjectionLagMs()) * time.Millisecond,
	}, nil
}

func (p *Provider) MinimumConsumeBatchSize(tenantIDs []string) (int, error) {
	policy, err := p.MinimumWorkerPolicy(tenantIDs)
	return policy.ConsumeBatchSize, err
}

func (p *Provider) MinimumWorkerPolicy(
	tenantIDs []string,
) (application.RuntimePolicy, error) {
	var minimum application.RuntimePolicy
	for _, tenantID := range tenantIDs {
		policy, err := p.Policy(tenantID)
		if err != nil {
			return application.RuntimePolicy{}, err
		}
		if minimum.ConsumeBatchSize == 0 ||
			policy.ConsumeBatchSize < minimum.ConsumeBatchSize {
			minimum.ConsumeBatchSize = policy.ConsumeBatchSize
		}
		if minimum.RebuildBatchSize == 0 ||
			policy.RebuildBatchSize < minimum.RebuildBatchSize {
			minimum.RebuildBatchSize = policy.RebuildBatchSize
		}
		if minimum.RealtimePublishBatchSize == 0 ||
			policy.RealtimePublishBatchSize < minimum.RealtimePublishBatchSize {
			minimum.RealtimePublishBatchSize = policy.RealtimePublishBatchSize
		}
		if minimum.CheckpointFlush == 0 ||
			policy.CheckpointFlush < minimum.CheckpointFlush {
			minimum.CheckpointFlush = policy.CheckpointFlush
		}
		if minimum.MaxProjectionLag == 0 ||
			policy.MaxProjectionLag < minimum.MaxProjectionLag {
			minimum.MaxProjectionLag = policy.MaxProjectionLag
		}
	}
	if minimum.ConsumeBatchSize == 0 {
		return application.RuntimePolicy{}, errors.New("projection worker policy is empty")
	}
	return minimum, nil
}
