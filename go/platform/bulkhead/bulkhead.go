package bulkhead

import (
	"context"
	"errors"
)

var (
	ErrInvalidConfig = errors.New("bulkhead configuration is invalid")
	ErrUnknownGroup  = errors.New("bulkhead group is not configured")
)

type Config struct {
	GroupLimits map[string]uint32
}

type Bulkhead struct {
	groups map[string]chan struct{}
}

func New(config Config) (*Bulkhead, error) {
	if len(config.GroupLimits) == 0 {
		return nil, ErrInvalidConfig
	}
	groups := make(map[string]chan struct{}, len(config.GroupLimits))
	for group, limit := range config.GroupLimits {
		if group == "" || limit == 0 {
			return nil, ErrInvalidConfig
		}
		groups[group] = make(chan struct{}, limit)
	}
	return &Bulkhead{groups: groups}, nil
}

func (b *Bulkhead) Do(ctx context.Context, group string, operation func(context.Context) error) error {
	slots, ok := b.groups[group]
	if !ok {
		return ErrUnknownGroup
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
		return operation(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}
