package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalid  = errors.New("invalid case command")
	ErrConflict = errors.New("idempotency key was used with another case intent")
	ErrNotFound = errors.New("case not found")
)

type Case struct {
	TenantID            uuid.UUID `json:"tenant_id"`
	ID                  uuid.UUID `json:"id"`
	CustomerRef         string    `json:"customer_ref"`
	WorkflowType        string    `json:"workflow_type"`
	WorkflowVersion     string    `json:"workflow_version"`
	BusinessStatus      string    `json:"business_status"`
	OrchestrationStatus string    `json:"orchestration_status"`
	WorkflowInstanceID  string    `json:"workflow_instance_id,omitempty"`
	Version             int64     `json:"version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type CreateRequest struct {
	TenantID        uuid.UUID
	ActorID         string
	IdempotencyKey  string
	CorrelationID   string
	CustomerRef     string
	WorkflowType    string
	WorkflowVersion string
}

type CreateResult struct {
	Case      Case `json:"case"`
	Duplicate bool `json:"duplicate"`
}

type CreateCommand struct {
	Request CreateRequest
	CaseID  uuid.UUID
	EventID uuid.UUID
	Digest  [32]byte
	Now     time.Time
}

type CaseView struct {
	Case             Case      `json:"case"`
	StartStatus      string    `json:"start_status"`
	StartAttempts    int       `json:"start_attempts"`
	PublishedVersion int64     `json:"published_version"`
	EngineSequence   int64     `json:"engine_sequence"`
	AsOf             time.Time `json:"as_of"`
}

type EngineEvent struct {
	TenantID        uuid.UUID
	InstanceID      string
	EventID         string
	WorkflowType    string
	WorkflowVersion string
	Sequence        int64
	EventType       string
	NodeID          string
	OccurredAt      time.Time
	PayloadDigest   [32]byte
}

type EngineEventRepository interface {
	ApplyEngineEvent(context.Context, EngineEvent) (bool, error)
}

type EngineEventService struct{ repo EngineEventRepository }

func NewEngineEventService(repo EngineEventRepository) (*EngineEventService, error) {
	if repo == nil {
		return nil, ErrInvalid
	}
	return &EngineEventService{repo: repo}, nil
}

func (s *EngineEventService) Apply(ctx context.Context, event EngineEvent) (bool, error) {
	if event.TenantID == uuid.Nil || event.InstanceID == "" || len(event.InstanceID) > 256 || event.EventID == "" || len(event.EventID) > 256 ||
		event.WorkflowType == "" || len(event.WorkflowType) > 128 || event.WorkflowVersion == "" || len(event.WorkflowVersion) > 128 ||
		event.Sequence < 1 || event.EventType == "" || len(event.EventType) > 128 ||
		event.OccurredAt.IsZero() || event.PayloadDigest == ([32]byte{}) || len(event.NodeID) > 256 {
		return false, ErrInvalid
	}
	return s.repo.ApplyEngineEvent(ctx, event)
}

type TimelineEntry struct {
	EventID          uuid.UUID  `json:"event_id"`
	AggregateVersion int64      `json:"aggregate_version"`
	EventType        string     `json:"event_type"`
	OccurredAt       time.Time  `json:"occurred_at"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
}

type ProcessEvent struct {
	EventID    string    `json:"event_id"`
	Sequence   int64     `json:"sequence"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
}

type ProcessEventPage struct {
	Events            []ProcessEvent `json:"events"`
	NextAfterSequence int64          `json:"next_after_sequence,omitempty"`
}

type SearchPage struct {
	Cases         []Case     `json:"cases"`
	NextCreatedAt *time.Time `json:"next_created_at,omitempty"`
	NextID        uuid.UUID  `json:"next_id,omitempty"`
}

type Repository interface {
	Create(context.Context, CreateCommand) (CreateResult, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Case, error)
	GetView(context.Context, uuid.UUID, uuid.UUID) (CaseView, error)
	Timeline(context.Context, uuid.UUID, uuid.UUID, int64, int) ([]TimelineEntry, error)
	ProcessEvents(context.Context, uuid.UUID, uuid.UUID, int64, int) (ProcessEventPage, error)
	Search(context.Context, uuid.UUID, string, time.Time, uuid.UUID, int) (SearchPage, error)
}

type Service struct {
	repo  Repository
	now   func() time.Time
	newID func() uuid.UUID
}

func NewService(repo Repository, now func() time.Time, newID func() uuid.UUID) (*Service, error) {
	if repo == nil || now == nil || newID == nil {
		return nil, ErrInvalid
	}
	return &Service{repo: repo, now: now, newID: newID}, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (CreateResult, error) {
	request.ActorID = strings.TrimSpace(request.ActorID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	request.CustomerRef = strings.TrimSpace(request.CustomerRef)
	request.WorkflowType = strings.TrimSpace(request.WorkflowType)
	request.WorkflowVersion = strings.TrimSpace(request.WorkflowVersion)
	if request.TenantID == uuid.Nil || request.ActorID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" ||
		request.CustomerRef == "" || request.WorkflowType == "" || request.WorkflowVersion == "" || len(request.ActorID) > 256 || len(request.IdempotencyKey) > 256 ||
		len(request.CorrelationID) > 256 || len(request.CustomerRef) > 256 || len(request.WorkflowType) > 128 || len(request.WorkflowVersion) > 128 {
		return CreateResult{}, ErrInvalid
	}
	intent, _ := json.Marshal(struct {
		CustomerRef     string `json:"customer_ref"`
		WorkflowType    string `json:"workflow_type"`
		WorkflowVersion string `json:"workflow_version"`
	}{request.CustomerRef, request.WorkflowType, request.WorkflowVersion})
	command := CreateCommand{Request: request, CaseID: s.newID(), EventID: s.newID(), Digest: sha256.Sum256(intent), Now: s.now().UTC()}
	if command.CaseID == uuid.Nil || command.EventID == uuid.Nil || command.CaseID == command.EventID || command.Now.IsZero() {
		return CreateResult{}, ErrInvalid
	}
	return s.repo.Create(ctx, command)
}

func (s *Service) Get(ctx context.Context, tenantID, caseID uuid.UUID) (Case, error) {
	if tenantID == uuid.Nil || caseID == uuid.Nil {
		return Case{}, ErrInvalid
	}
	return s.repo.Get(ctx, tenantID, caseID)
}

func (s *Service) GetView(ctx context.Context, tenantID, caseID uuid.UUID) (CaseView, error) {
	if tenantID == uuid.Nil || caseID == uuid.Nil {
		return CaseView{}, ErrInvalid
	}
	return s.repo.GetView(ctx, tenantID, caseID)
}

func (s *Service) Timeline(ctx context.Context, tenantID, caseID uuid.UUID, afterVersion int64, limit int) ([]TimelineEntry, error) {
	if tenantID == uuid.Nil || caseID == uuid.Nil || afterVersion < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return s.repo.Timeline(ctx, tenantID, caseID, afterVersion, limit)
}

func (s *Service) ProcessEvents(ctx context.Context, tenantID, caseID uuid.UUID, afterSequence int64, limit int) (ProcessEventPage, error) {
	if tenantID == uuid.Nil || caseID == uuid.Nil || afterSequence < 0 || limit < 1 || limit > 100 {
		return ProcessEventPage{}, ErrInvalid
	}
	return s.repo.ProcessEvents(ctx, tenantID, caseID, afterSequence, limit)
}

func (s *Service) Search(ctx context.Context, tenantID uuid.UUID, customerRef string, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) (SearchPage, error) {
	customerRef = strings.TrimSpace(customerRef)
	if tenantID == uuid.Nil || len(customerRef) > 256 || limit < 1 || limit > 100 ||
		(beforeCreatedAt.IsZero() != (beforeID == uuid.Nil)) {
		return SearchPage{}, ErrInvalid
	}
	return s.repo.Search(ctx, tenantID, customerRef, beforeCreatedAt, beforeID, limit)
}
