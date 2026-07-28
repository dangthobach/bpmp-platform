package application

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidEvent = errors.New("projection event is invalid")
	ErrSequenceGap  = errors.New("projection event sequence has a gap")
	ErrNotFound     = errors.New("projection read model was not found")
)

type EventKind uint8

const (
	EventProgress EventKind = iota + 1
	EventStarted
	EventNodeActivated
	EventCompleted
	EventTerminatedForCompliance
)

type InstanceEvent struct {
	EventID         string
	TenantID        string
	InstanceID      string
	WorkflowType    string
	WorkflowVersion string
	Sequence        uint64
	OccurredAt      time.Time
	ConfigVersion   string
	PolicyVersion   string
	Kind            EventKind
	NodeID          string
}

func (e InstanceEvent) Validate() error {
	if e.EventID == "" || e.TenantID == "" || e.InstanceID == "" ||
		e.WorkflowType == "" || e.WorkflowVersion == "" || e.Sequence == 0 ||
		e.OccurredAt.IsZero() || e.ConfigVersion == "" || e.PolicyVersion == "" ||
		e.Kind < EventProgress || e.Kind > EventTerminatedForCompliance {
		return ErrInvalidEvent
	}
	if e.Kind == EventNodeActivated && e.NodeID == "" {
		return ErrInvalidEvent
	}
	return nil
}

type KafkaPosition struct {
	ConsumerName string
	Topic        string
	Partition    int32
	Offset       int64
}

func (p KafkaPosition) Validate() error {
	if p.ConsumerName == "" || p.Topic == "" || p.Partition < 0 || p.Offset < 0 {
		return ErrInvalidEvent
	}
	return nil
}

type Instance struct {
	TenantID          string
	InstanceID        string
	WorkflowType      string
	WorkflowVersion   string
	Status            string
	ActiveNodeID      string
	LastEventSequence uint64
	StartedAt         time.Time
	UpdatedAt         time.Time
	CompletedAt       *time.Time
	ConfigVersion     string
	PolicyVersion     string
	IsDeleted         bool
	Version           uint64
}

type Cursor struct {
	UpdatedAt  time.Time
	InstanceID string
}

type ListFilter struct {
	TenantID       string
	Statuses       []string
	WorkflowType   string
	IncludeDeleted bool
	Limit          int
	After          *Cursor
}

type Store interface {
	Apply(context.Context, KafkaPosition, InstanceEvent) (bool, error)
	Get(context.Context, string, string) (Instance, error)
	List(context.Context, ListFilter) ([]Instance, error)
}

type RuntimePolicy struct {
	ConsumeBatchSize     int
	QueryDefaultPageSize uint32
	QueryMaxPageSize     uint32
}

type RuntimePolicyProvider interface {
	Policy(string) (RuntimePolicy, error)
}

type Service struct {
	store  Store
	policy RuntimePolicyProvider
}

func NewService(store Store, policy RuntimePolicyProvider) (*Service, error) {
	if store == nil || policy == nil {
		return nil, errors.New("projection store and runtime policy are required")
	}
	return &Service{store: store, policy: policy}, nil
}

func (s *Service) Apply(ctx context.Context, position KafkaPosition, event InstanceEvent) (bool, error) {
	if err := position.Validate(); err != nil {
		return false, err
	}
	if err := event.Validate(); err != nil {
		return false, err
	}
	return s.store.Apply(ctx, position, event)
}

func (s *Service) Get(ctx context.Context, tenantID, instanceID string) (Instance, error) {
	if tenantID == "" || instanceID == "" {
		return Instance{}, ErrNotFound
	}
	return s.store.Get(ctx, tenantID, instanceID)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Instance, int, error) {
	policy, err := s.policy.Policy(filter.TenantID)
	if err != nil {
		return nil, 0, err
	}
	if filter.TenantID == "" {
		return nil, 0, errors.New("tenant is required")
	}
	if filter.Limit == 0 {
		filter.Limit = int(policy.QueryDefaultPageSize)
	}
	if filter.Limit < 0 || uint32(filter.Limit) > policy.QueryMaxPageSize {
		return nil, 0, errors.New("projection query page size exceeds policy")
	}
	effectiveLimit := filter.Limit
	filter.Limit++
	instances, err := s.store.List(ctx, filter)
	return instances, effectiveLimit, err
}
