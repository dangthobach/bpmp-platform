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
	AsOf             time.Time `json:"as_of"`
}

type TimelineEntry struct {
	EventID          uuid.UUID  `json:"event_id"`
	AggregateVersion int64      `json:"aggregate_version"`
	EventType        string     `json:"event_type"`
	OccurredAt       time.Time  `json:"occurred_at"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
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

func (s *Service) Search(ctx context.Context, tenantID uuid.UUID, customerRef string, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) (SearchPage, error) {
	customerRef = strings.TrimSpace(customerRef)
	if tenantID == uuid.Nil || len(customerRef) > 256 || limit < 1 || limit > 100 ||
		(beforeCreatedAt.IsZero() != (beforeID == uuid.Nil)) {
		return SearchPage{}, ErrInvalid
	}
	return s.repo.Search(ctx, tenantID, customerRef, beforeCreatedAt, beforeID, limit)
}
