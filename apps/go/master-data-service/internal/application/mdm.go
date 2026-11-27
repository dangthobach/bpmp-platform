package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid input")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
)

type Actor struct {
	TenantID      string
	ActorID       string
	CorrelationID string
	Capabilities  []string
}

func (a Actor) HasCapability(cap string) bool {
	for _, c := range a.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

type Dataset struct {
	ID                        string    `json:"id"`
	TenantID                  string    `json:"tenant_id"`
	Code                      string    `json:"code"`
	Name                      string    `json:"name"`
	CurrentPublicationVersion int64     `json:"current_publication_version"`
	Version                   int64     `json:"version"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type Publication struct {
	DatasetID          string            `json:"dataset_id"`
	DatasetCode        string            `json:"dataset_code"`
	TenantID           string            `json:"tenant_id"`
	PublicationVersion int64             `json:"publication_version"`
	EffectiveFrom      time.Time         `json:"effective_from"`
	EffectiveTo        *time.Time        `json:"effective_to,omitempty"`
	ContentSHA256      string            `json:"content_sha256"`
	Records            map[string]string `json:"records"`
	RollbackOfVersion  *int64            `json:"rollback_of_version,omitempty"`
	PublishedBy        string            `json:"published_by"`
	PublishedAt        time.Time         `json:"published_at"`
}

type ConsumerAck struct {
	TenantID            string    `json:"tenant_id"`
	DatasetID           string    `json:"dataset_id"`
	ConsumerID          string    `json:"consumer_id"`
	AcknowledgedVersion int64     `json:"acknowledged_version"`
	CurrentVersion      int64     `json:"current_version"`
	Lag                 int64     `json:"lag"`
	AckedAt             time.Time `json:"acked_at"`
}

type OutboxEvent struct {
	EventID            string    `json:"event_id"`
	TenantID           string    `json:"tenant_id"`
	DatasetID          string    `json:"dataset_id"`
	DatasetCode        string    `json:"dataset_code"`
	PublicationVersion int64     `json:"publication_version"`
	EventType          string    `json:"event_type"`
	ContentSHA256      string    `json:"content_sha256"`
	OccurredAt         time.Time `json:"occurred_at"`
}

type PublishRequest struct {
	DatasetCode     string            `json:"dataset_code"`
	DatasetName     string            `json:"dataset_name"`
	ExpectedVersion int64             `json:"expected_version"`
	EffectiveFrom   time.Time         `json:"effective_from"`
	EffectiveTo     *time.Time        `json:"effective_to,omitempty"`
	Records         map[string]string `json:"records"`
	IdempotencyKey  string            `json:"-"`
}

type RollbackRequest struct {
	DatasetCode     string `json:"dataset_code"`
	TargetVersion   int64  `json:"target_version"`
	ExpectedVersion int64  `json:"expected_version"`
	IdempotencyKey  string `json:"-"`
}

type ResolveBatchRequest struct {
	DatasetCode string   `json:"dataset_code"`
	Version     int64    `json:"version"` // 0 means current effective
	Keys        []string `json:"keys"`
}

type ResolveBatchResponse struct {
	DatasetCode        string            `json:"dataset_code"`
	PublicationVersion int64             `json:"publication_version"`
	ContentSHA256      string            `json:"content_sha256"`
	Resolved           map[string]string `json:"resolved"`
	Missing            []string          `json:"missing,omitempty"`
}

type idempotencyEntry struct {
	Digest   string
	Response []byte
}

type Service struct {
	mu           sync.RWMutex
	seq          int64
	now          func() time.Time
	datasets     map[string]*Dataset                // tenant|code -> Dataset
	publications map[string]map[int64]*Publication  // datasetID -> version -> immutable Publication
	cache        map[string]*Publication            // bounded local cache: tenant|code|ver -> Publication
	acks         map[string]map[string]*ConsumerAck // datasetID -> consumerID -> Ack
	idempotency  map[string]idempotencyEntry
	outbox       []OutboxEvent
	outboxSink   func(context.Context, OutboxEvent) error
}

func (s *Service) SetOutboxSink(sink func(context.Context, OutboxEvent) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outboxSink = sink
}

func (s *Service) recordOutbox(evt OutboxEvent) {
	s.outbox = append(s.outbox, evt)
	if s.outboxSink != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.outboxSink(ctx, evt)
		cancel()
	}
}

func NewService(now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		now:          now,
		datasets:     make(map[string]*Dataset),
		publications: make(map[string]map[int64]*Publication),
		cache:        make(map[string]*Publication),
		acks:         make(map[string]map[string]*ConsumerAck),
		idempotency:  make(map[string]idempotencyEntry),
	}
}

func (s *Service) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%06d", prefix, s.seq)
}

func canonicalRecordsSHA256(records map[string]string) string {
	b, _ := json.Marshal(records)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ClearCache simulates Redis/local cache loss to verify authoritative persistence is unaffected.
func (s *Service) ClearCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = make(map[string]*Publication)
}

func (s *Service) PublishDataset(_ context.Context, actor Actor, req PublishRequest) (Publication, bool, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Publication{}, false, ErrUnauthorized
	}
	if !actor.HasCapability("mdm.publish") {
		return Publication{}, false, ErrForbidden
	}
	code := strings.TrimSpace(req.DatasetCode)
	if code == "" || len(req.Records) == 0 || req.IdempotencyKey == "" {
		return Publication{}, false, fmt.Errorf("%w: dataset_code, non-empty records, and idempotency_key are required", ErrInvalidInput)
	}
	if req.EffectiveFrom.IsZero() {
		req.EffectiveFrom = s.now().UTC()
	}
	if req.EffectiveTo != nil && !req.EffectiveTo.After(req.EffectiveFrom) {
		return Publication{}, false, fmt.Errorf("%w: effective_to must be after effective_from", ErrInvalidInput)
	}

	reqRaw, _ := json.Marshal(req)
	reqDigest := sha256Hex(reqRaw)
	idemKey := actor.TenantID + "|" + actor.ActorID + "|publish|" + req.IdempotencyKey

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.idempotency[idemKey]; ok {
		if existing.Digest != reqDigest {
			return Publication{}, false, ErrConflict
		}
		var replay Publication
		_ = json.Unmarshal(existing.Response, &replay)
		return replay, true, nil
	}

	dsKey := actor.TenantID + "|" + code
	ds, exists := s.datasets[dsKey]
	now := s.now().UTC()
	if !exists {
		if req.ExpectedVersion != 0 {
			return Publication{}, false, ErrConflict
		}
		name := strings.TrimSpace(req.DatasetName)
		if name == "" {
			name = code
		}
		ds = &Dataset{
			ID:        s.nextID("mdm"),
			TenantID:  actor.TenantID,
			Code:      code,
			Name:      name,
			Version:   0,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.datasets[dsKey] = ds
		s.publications[ds.ID] = make(map[int64]*Publication)
	} else if ds.Version != req.ExpectedVersion {
		return Publication{}, false, fmt.Errorf("%w: expected version %d, current %d", ErrConflict, req.ExpectedVersion, ds.Version)
	}

	nextPubVer := ds.CurrentPublicationVersion + 1
	pub := &Publication{
		DatasetID:          ds.ID,
		DatasetCode:        ds.Code,
		TenantID:           actor.TenantID,
		PublicationVersion: nextPubVer,
		EffectiveFrom:      req.EffectiveFrom.UTC(),
		EffectiveTo:        req.EffectiveTo,
		ContentSHA256:      canonicalRecordsSHA256(req.Records),
		Records:            cloneMap(req.Records),
		PublishedBy:        actor.ActorID,
		PublishedAt:        now,
	}
	s.publications[ds.ID][nextPubVer] = pub
	ds.CurrentPublicationVersion = nextPubVer
	ds.Version++
	ds.UpdatedAt = now

	// Invalidate & warm cache for the new immutable publication.
	delete(s.cache, actor.TenantID+"|"+code+"|current")
	s.cache[fmt.Sprintf("%s|%s|%d", actor.TenantID, code, nextPubVer)] = pub

	s.recordOutbox(OutboxEvent{
		EventID:            s.nextID("evt"),
		TenantID:           actor.TenantID,
		DatasetID:          ds.ID,
		DatasetCode:        ds.Code,
		PublicationVersion: nextPubVer,
		EventType:          "MasterDataPublished",
		ContentSHA256:      pub.ContentSHA256,
		OccurredAt:         now,
	})

	encoded, _ := json.Marshal(pub)
	s.idempotency[idemKey] = idempotencyEntry{Digest: reqDigest, Response: encoded}
	return *pub, false, nil
}

// RollbackDataset publishes a NEW monotonic generation copying TargetVersion (never mutates history).
func (s *Service) RollbackDataset(_ context.Context, actor Actor, req RollbackRequest) (Publication, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Publication{}, ErrUnauthorized
	}
	if !actor.HasCapability("mdm.publish") {
		return Publication{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ds, ok := s.datasets[actor.TenantID+"|"+req.DatasetCode]
	if !ok {
		return Publication{}, ErrNotFound
	}
	if ds.Version != req.ExpectedVersion {
		return Publication{}, ErrConflict
	}
	targetPub, ok := s.publications[ds.ID][req.TargetVersion]
	if !ok {
		return Publication{}, ErrNotFound
	}

	now := s.now().UTC()
	nextPubVer := ds.CurrentPublicationVersion + 1
	targetVer := req.TargetVersion
	pub := &Publication{
		DatasetID:          ds.ID,
		DatasetCode:        ds.Code,
		TenantID:           actor.TenantID,
		PublicationVersion: nextPubVer,
		EffectiveFrom:      now,
		ContentSHA256:      targetPub.ContentSHA256,
		Records:            cloneMap(targetPub.Records),
		RollbackOfVersion:  &targetVer,
		PublishedBy:        actor.ActorID,
		PublishedAt:        now,
	}
	s.publications[ds.ID][nextPubVer] = pub
	ds.CurrentPublicationVersion = nextPubVer
	ds.Version++
	ds.UpdatedAt = now
	delete(s.cache, actor.TenantID+"|"+ds.Code+"|current")
	s.recordOutbox(OutboxEvent{
		EventID:            s.nextID("evt"),
		TenantID:           actor.TenantID,
		DatasetID:          ds.ID,
		DatasetCode:        ds.Code,
		PublicationVersion: nextPubVer,
		EventType:          "MasterDataRolledBack",
		ContentSHA256:      pub.ContentSHA256,
		OccurredAt:         now,
	})
	return *pub, nil
}

func (s *Service) GetSnapshot(_ context.Context, actor Actor, datasetCode string, version int64) (Publication, error) {
	if actor.TenantID == "" {
		return Publication{}, ErrUnauthorized
	}
	if !actor.HasCapability("mdm.read") {
		return Publication{}, ErrForbidden
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	ds, ok := s.datasets[actor.TenantID+"|"+datasetCode]
	if !ok {
		return Publication{}, ErrNotFound
	}
	if version <= 0 {
		version = ds.CurrentPublicationVersion
	}
	cacheKey := fmt.Sprintf("%s|%s|%d", actor.TenantID, datasetCode, version)
	if cached, ok := s.cache[cacheKey]; ok {
		cp := *cached
		cp.Records = cloneMap(cached.Records)
		return cp, nil
	}
	pub, ok := s.publications[ds.ID][version]
	if !ok {
		return Publication{}, ErrNotFound
	}
	cp := *pub
	cp.Records = cloneMap(pub.Records)
	return cp, nil
}

func (s *Service) ResolveBatch(ctx context.Context, actor Actor, req ResolveBatchRequest) (ResolveBatchResponse, error) {
	pub, err := s.GetSnapshot(ctx, actor, req.DatasetCode, req.Version)
	if err != nil {
		return ResolveBatchResponse{}, err
	}
	resolved := make(map[string]string, len(req.Keys))
	var missing []string
	for _, k := range req.Keys {
		if v, ok := pub.Records[k]; ok {
			resolved[k] = v
		} else {
			missing = append(missing, k)
		}
	}
	return ResolveBatchResponse{
		DatasetCode:        pub.DatasetCode,
		PublicationVersion: pub.PublicationVersion,
		ContentSHA256:      pub.ContentSHA256,
		Resolved:           resolved,
		Missing:            missing,
	}, nil
}

func (s *Service) AcknowledgeConsumer(_ context.Context, actor Actor, datasetCode, consumerID string, version int64) (ConsumerAck, error) {
	if actor.TenantID == "" {
		return ConsumerAck{}, ErrUnauthorized
	}
	if !actor.HasCapability("mdm.ack") && !actor.HasCapability("mdm.read") {
		return ConsumerAck{}, ErrForbidden
	}
	if strings.TrimSpace(consumerID) == "" || version <= 0 {
		return ConsumerAck{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, ok := s.datasets[actor.TenantID+"|"+datasetCode]
	if !ok {
		return ConsumerAck{}, ErrNotFound
	}
	if version > ds.CurrentPublicationVersion {
		return ConsumerAck{}, ErrInvalidInput
	}
	if s.acks[ds.ID] == nil {
		s.acks[ds.ID] = make(map[string]*ConsumerAck)
	}
	ack := &ConsumerAck{
		TenantID:            actor.TenantID,
		DatasetID:           ds.ID,
		ConsumerID:          consumerID,
		AcknowledgedVersion: version,
		CurrentVersion:      ds.CurrentPublicationVersion,
		Lag:                 ds.CurrentPublicationVersion - version,
		AckedAt:             s.now().UTC(),
	}
	s.acks[ds.ID][consumerID] = ack
	return *ack, nil
}

func (s *Service) ListDatasets(_ context.Context, actor Actor) ([]Dataset, error) {
	if actor.TenantID == "" {
		return nil, ErrUnauthorized
	}
	if !actor.HasCapability("mdm.read") {
		return nil, ErrForbidden
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Dataset, 0)
	for _, ds := range s.datasets {
		if ds.TenantID == actor.TenantID {
			out = append(out, *ds)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

func (s *Service) OutboxEvents() []OutboxEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]OutboxEvent, len(s.outbox))
	copy(cp, s.outbox)
	return cp
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
