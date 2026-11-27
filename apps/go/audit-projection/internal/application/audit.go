package application

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
)

var allowedMetadataKeys = map[string]bool{
	"status":         true,
	"version":        true,
	"owner_ref_type": true,
	"owner_ref_id":   true,
	"endpoint_id":    true,
	"dataset_code":   true,
	"response_class": true,
}

type Actor struct {
	TenantID     string
	ActorID      string
	Capabilities []string
}

func (a Actor) HasCapability(cap string) bool {
	for _, c := range a.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// CommittedOwnerEvent represents an immutable audit event emitted by a domain owner.
type CommittedOwnerEvent struct {
	EventID       string            `json:"event_id"`
	TenantID      string            `json:"tenant_id"`
	SourceService string            `json:"source_service"`
	AggregateType string            `json:"aggregate_type"`
	AggregateID   string            `json:"aggregate_id"`
	Action        string            `json:"action"`
	ActorID       string            `json:"actor_id"`
	CorrelationID string            `json:"correlation_id"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	LegalHold     bool              `json:"legal_hold,omitempty"`
}

type ProjectedEntry struct {
	EventID              string            `json:"event_id"`
	TenantID             string            `json:"tenant_id"`
	SourceService        string            `json:"source_service"`
	AggregateType        string            `json:"aggregate_type"`
	AggregateID          string            `json:"aggregate_id"`
	Action               string            `json:"action"`
	ActorID              string            `json:"actor_id"`
	CorrelationID        string            `json:"correlation_id"`
	OccurredAt           time.Time         `json:"occurred_at"`
	RedactedMetadata     map[string]string `json:"redacted_metadata"`
	LegalHold            bool              `json:"legal_hold"`
	ProjectionGeneration int64             `json:"projection_generation"`
}

type SearchQuery struct {
	SourceService string
	AggregateType string
	AggregateID   string
	ActorID       string
	Limit         int
	AfterEventID  string
}

type SearchResult struct {
	Entries              []ProjectedEntry `json:"entries"`
	NextAfterEventID     string           `json:"next_after_event_id,omitempty"`
	ProjectionGeneration int64            `json:"projection_generation"`
	AsOf                 time.Time        `json:"as_of"`
}

type Service struct {
	mu         sync.RWMutex
	now        func() time.Time
	generation int64
	entries    map[string]ProjectedEntry // tenant|event_id -> ProjectedEntry
}

func NewService(now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		now:        now,
		generation: 1,
		entries:    make(map[string]ProjectedEntry),
	}
}

func redactMetadata(in map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range in {
		if allowedMetadataKeys[strings.ToLower(k)] {
			out[strings.ToLower(k)] = v
		}
	}
	return out
}

func (s *Service) Ingest(_ context.Context, evt CommittedOwnerEvent) (bool, error) {
	if evt.TenantID == "" || evt.EventID == "" || evt.SourceService == "" || evt.Action == "" {
		return false, ErrInvalidInput
	}
	key := evt.TenantID + "|" + evt.EventID
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.entries[key]; exists {
		return false, nil // idempotent dedup
	}
	s.entries[key] = ProjectedEntry{
		EventID:              evt.EventID,
		TenantID:             evt.TenantID,
		SourceService:        evt.SourceService,
		AggregateType:        evt.AggregateType,
		AggregateID:          evt.AggregateID,
		Action:               evt.Action,
		ActorID:              evt.ActorID,
		CorrelationID:        evt.CorrelationID,
		OccurredAt:           evt.OccurredAt.UTC(),
		RedactedMetadata:     redactMetadata(evt.Metadata),
		LegalHold:            evt.LegalHold,
		ProjectionGeneration: s.generation,
	}
	return true, nil
}

// SimulateIndexLoss wipes the rebuildable projection store (proving it is never the source of truth).
func (s *Service) SimulateIndexLoss() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]ProjectedEntry)
}

// Rebuild reconstructs the entire searchable projection from authoritative committed owner events.
func (s *Service) Rebuild(ctx context.Context, authoritativeEvents []CommittedOwnerEvent) (int64, int, error) {
	s.mu.Lock()
	s.generation++
	gen := s.generation
	s.entries = make(map[string]ProjectedEntry, len(authoritativeEvents))
	s.mu.Unlock()

	count := 0
	for _, evt := range authoritativeEvents {
		inserted, err := s.Ingest(ctx, evt)
		if err != nil {
			return gen, count, err
		}
		if inserted {
			count++
		}
	}
	return gen, count, nil
}

func (s *Service) ApplyRetention(_ context.Context, tenantID string, retentionDays int) (int, error) {
	if tenantID == "" || retentionDays < 30 {
		return 0, ErrInvalidInput
	}
	cutoff := s.now().UTC().AddDate(0, 0, -retentionDays)
	s.mu.Lock()
	defer s.mu.Unlock()
	purged := 0
	for key, entry := range s.entries {
		if entry.TenantID != tenantID || entry.LegalHold {
			continue
		}
		if entry.OccurredAt.Before(cutoff) {
			delete(s.entries, key)
			purged++
		}
	}
	return purged, nil
}

func (s *Service) Search(_ context.Context, actor Actor, q SearchQuery) (SearchResult, error) {
	if actor.TenantID == "" {
		return SearchResult{}, ErrUnauthorized
	}
	if !actor.HasCapability("audit.projection.read") && !actor.HasCapability("audit.read") {
		return SearchResult{}, ErrForbidden
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]ProjectedEntry, 0)
	for _, entry := range s.entries {
		if entry.TenantID != actor.TenantID {
			continue
		}
		if q.SourceService != "" && entry.SourceService != q.SourceService {
			continue
		}
		if q.AggregateType != "" && entry.AggregateType != q.AggregateType {
			continue
		}
		if q.AggregateID != "" && entry.AggregateID != q.AggregateID {
			continue
		}
		if q.ActorID != "" && entry.ActorID != q.ActorID {
			continue
		}
		matched = append(matched, entry)
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].OccurredAt.Equal(matched[j].OccurredAt) {
			return matched[i].OccurredAt.After(matched[j].OccurredAt)
		}
		return matched[i].EventID > matched[j].EventID
	})

	if q.AfterEventID != "" {
		idx := -1
		for i, e := range matched {
			if e.EventID == q.AfterEventID {
				idx = i
				break
			}
		}
		if idx >= 0 && idx+1 < len(matched) {
			matched = matched[idx+1:]
		} else if idx >= 0 {
			matched = nil
		}
	}

	nextID := ""
	if len(matched) > limit {
		matched = matched[:limit]
		nextID = matched[len(matched)-1].EventID
	}
	return SearchResult{
		Entries:              matched,
		NextAfterEventID:     nextID,
		ProjectionGeneration: s.generation,
		AsOf:                 s.now().UTC(),
	}, nil
}
