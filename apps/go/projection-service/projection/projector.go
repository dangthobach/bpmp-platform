package projection

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidEvent    = errors.New("projection event is invalid")
	ErrSequenceGap     = errors.New("projection event sequence has a gap")
	ErrInvalidSnapshot = errors.New("projection snapshot is invalid")
)

type Event struct {
	TenantID string
	Sequence uint64
	EntityID string
	Status   string
	Deleted  bool
}

type Record struct {
	TenantID string
	EntityID string
	Status   string
	Deleted  bool
	Sequence uint64
}

type Filter struct {
	TenantID      string
	Statuses      map[string]struct{}
	IncludeDelete bool
}

type Snapshot struct {
	Checkpoints map[string]uint64
	Records     []Record
}

type Projector struct {
	mu          sync.RWMutex
	checkpoints map[string]uint64
	records     map[string]Record
}

func New() *Projector {
	return &Projector{
		checkpoints: make(map[string]uint64),
		records:     make(map[string]Record),
	}
}

func NewFromSnapshot(snapshot Snapshot) (*Projector, error) {
	projector := New()
	for tenant, checkpoint := range snapshot.Checkpoints {
		if tenant == "" {
			return nil, ErrInvalidSnapshot
		}
		projector.checkpoints[tenant] = checkpoint
	}
	for _, record := range snapshot.Records {
		if record.TenantID == "" || record.EntityID == "" ||
			record.Sequence > snapshot.Checkpoints[record.TenantID] {
			return nil, ErrInvalidSnapshot
		}
		projector.records[key(record.TenantID, record.EntityID)] = record
	}
	return projector, nil
}

// Apply is idempotent for already-checkpointed events and rejects sequence gaps.
func (p *Projector) Apply(event Event) error {
	if event.TenantID == "" || event.EntityID == "" || event.Sequence == 0 {
		return ErrInvalidEvent
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.checkpoints[event.TenantID]
	if event.Sequence <= current {
		return nil
	}
	if event.Sequence != current+1 {
		return ErrSequenceGap
	}
	p.records[key(event.TenantID, event.EntityID)] = Record{
		TenantID: event.TenantID,
		EntityID: event.EntityID,
		Status:   event.Status,
		Deleted:  event.Deleted,
		Sequence: event.Sequence,
	}
	p.checkpoints[event.TenantID] = event.Sequence
	return nil
}

func Rebuild(events []Event) (*Projector, error) {
	projector := New()
	for _, event := range events {
		if err := projector.Apply(event); err != nil {
			return nil, err
		}
	}
	return projector, nil
}

func (p *Projector) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snapshot := Snapshot{
		Checkpoints: make(map[string]uint64, len(p.checkpoints)),
		Records:     make([]Record, 0, len(p.records)),
	}
	for tenant, checkpoint := range p.checkpoints {
		snapshot.Checkpoints[tenant] = checkpoint
	}
	for _, record := range p.records {
		snapshot.Records = append(snapshot.Records, record)
	}
	sort.Slice(snapshot.Records, func(i, j int) bool {
		left, right := snapshot.Records[i], snapshot.Records[j]
		if left.TenantID != right.TenantID {
			return left.TenantID < right.TenantID
		}
		return left.EntityID < right.EntityID
	})
	return snapshot
}

func (p *Projector) Query(filter Filter) []Record {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]Record, 0)
	for _, record := range p.records {
		if record.TenantID != filter.TenantID ||
			(!filter.IncludeDelete && record.Deleted) {
			continue
		}
		if len(filter.Statuses) != 0 {
			if _, accepted := filter.Statuses[record.Status]; !accepted {
				continue
			}
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].EntityID < result[j].EntityID
	})
	return result
}

func key(tenantID, entityID string) string {
	return tenantID + "\x00" + entityID
}
