package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

var (
	ErrForbidden            = errors.New("actor is not assigned to the work item")
	ErrActorProof           = errors.New("exactly one original token or signed actor context is required")
	ErrVersionConflict      = errors.New("work item version conflict")
	ErrIdempotencyConflict  = errors.New("command id was already used with different completion data")
	ErrProjectionDependency = errors.New("committed event projection dependency is missing")
)

type ActorCredential struct {
	ActorID             string
	OriginalSignedToken []byte
	SignedActorContext  []byte
}

func (c ActorCredential) Validate() error {
	if c.ActorID == "" || (len(c.OriginalSignedToken) == 0) == (len(c.SignedActorContext) == 0) {
		return ErrActorProof
	}
	return nil
}

type CompleteRequest struct {
	TenantID        string
	WorkItemID      string
	CommandID       string
	CorrelationID   string
	Decision        string
	ExpectedVersion int64
	Actor           ActorCredential
	ActorGroups     map[string]struct{}
	OccurredAt      time.Time
}

type DelegateRequest struct {
	TenantID        string
	WorkItemID      string
	CommandID       string
	CorrelationID   string
	ExpectedVersion int64
	Actor           ActorCredential
	ActorGroups     map[string]struct{}
	Assignment      domain.Assignment
	OccurredAt      time.Time
}

type EngineCompleteCommand struct {
	TenantID           string
	InstanceID         string
	NodeID             string
	CommandID          string
	CorrelationID      string
	Decision           string
	WorkflowType       string
	WorkflowVersion    string
	OriginalToken      []byte
	SignedActorContext []byte
	ActorID            string
	OccurredAt         time.Time
}

type CommittedCompletion struct {
	TenantID   string
	EventID    string
	Sequence   uint64
	InstanceID string
	NodeID     string
	Decision   string
	OccurredAt time.Time
}

type CommittedCase struct {
	EventID  string
	Sequence uint64
	Case     domain.Case
}

type CommittedCaseTransition struct {
	TenantID           string
	EventID            string
	Sequence           uint64
	CaseID             string
	PlanItemID         string
	PlanItemKind       string
	Status             domain.PlanItemStatus
	SatisfiedSentryIDs []string
	OccurredAt         time.Time
}

type CommittedCancellation struct {
	TenantID   string
	EventID    string
	Sequence   uint64
	InstanceID string
	NodeID     string
	Reason     string
	OccurredAt time.Time
}

type EnginePort interface {
	CompleteUserTask(context.Context, EngineCompleteCommand) error
}

type Store interface {
	GetWorkItem(context.Context, string, string) (domain.WorkItem, error)
	ProjectActivation(context.Context, domain.Activation) (domain.WorkItem, bool, error)
	RequestCompletion(context.Context, domain.WorkItem, string, string, string) error
	CommitCompletion(context.Context, CommittedCompletion) error
	CommitCancellation(context.Context, CommittedCancellation) error
	Delegate(context.Context, domain.WorkItem, string, string, string) error
	ProjectCase(context.Context, CommittedCase) (bool, error)
	CommitCaseTransition(context.Context, CommittedCaseTransition) error
	TransitionCaseStage(context.Context, string, string, string, domain.PlanItemStatus, string, time.Time) error
	AchieveCaseMilestone(context.Context, string, string, string, string, time.Time) error
}

type Service struct {
	store  Store
	engine EnginePort
}

func NewService(store Store, engine EnginePort) (*Service, error) {
	if store == nil || engine == nil {
		return nil, errors.New("store and engine ports are required")
	}
	return &Service{store: store, engine: engine}, nil
}

func (s *Service) ProjectActivation(ctx context.Context, activation domain.Activation) (domain.WorkItem, bool, error) {
	return s.store.ProjectActivation(ctx, activation)
}

func (s *Service) Complete(ctx context.Context, request CompleteRequest) error {
	if err := request.Actor.Validate(); err != nil {
		return err
	}
	item, err := s.store.GetWorkItem(ctx, request.TenantID, request.WorkItemID)
	if err != nil {
		return err
	}
	if !item.CanAct(request.Actor.ActorID, request.ActorGroups) {
		return ErrForbidden
	}
	if request.CommandID == "" {
		return errors.New("command id must not be empty")
	}
	if item.Status == domain.WorkItemCompletionRequested {
		if item.CompletionCommandID != request.CommandID || item.Decision != request.Decision {
			return ErrIdempotencyConflict
		}
	} else {
		if request.ExpectedVersion != item.Version {
			return ErrVersionConflict
		}
		pending, completionErr := domain.RequestCompletion(item, request.Actor.ActorID, request.Decision, request.OccurredAt)
		if completionErr != nil {
			return completionErr
		}
		pending.CompletionCommandID = request.CommandID
		if err := s.store.RequestCompletion(ctx, pending, request.CommandID, request.CorrelationID, request.Actor.ActorID); err != nil {
			return err
		}
	}
	command := EngineCompleteCommand{
		TenantID: request.TenantID, InstanceID: item.InstanceID, NodeID: item.NodeID,
		CommandID: request.CommandID, CorrelationID: request.CorrelationID,
		Decision: request.Decision, ActorID: request.Actor.ActorID,
		OccurredAt:   request.OccurredAt,
		WorkflowType: item.WorkflowType, WorkflowVersion: item.WorkflowVersion,
		OriginalToken:      append([]byte(nil), request.Actor.OriginalSignedToken...),
		SignedActorContext: append([]byte(nil), request.Actor.SignedActorContext...),
	}
	if err := s.engine.CompleteUserTask(ctx, command); err != nil {
		return fmt.Errorf("forward complete user task: %w", err)
	}
	return nil
}

func (s *Service) Delegate(ctx context.Context, request DelegateRequest) error {
	if err := request.Actor.Validate(); err != nil {
		return err
	}
	item, err := s.store.GetWorkItem(ctx, request.TenantID, request.WorkItemID)
	if err != nil {
		return err
	}
	if request.ExpectedVersion != item.Version {
		return ErrVersionConflict
	}
	if !item.CanAct(request.Actor.ActorID, request.ActorGroups) {
		return ErrForbidden
	}
	delegated, err := domain.Delegate(item, request.Actor.ActorID, request.Assignment, request.OccurredAt)
	if err != nil {
		return err
	}
	return s.store.Delegate(ctx, delegated, request.CommandID, request.CorrelationID, request.Actor.ActorID)
}

func (s *Service) ProjectCommittedCompletion(ctx context.Context, event CommittedCompletion) error {
	return s.store.CommitCompletion(ctx, event)
}

func (s *Service) ProjectCommittedCancellation(ctx context.Context, event CommittedCancellation) error {
	return s.store.CommitCancellation(ctx, event)
}

func (s *Service) ProjectCase(ctx context.Context, event CommittedCase) (bool, error) {
	return s.store.ProjectCase(ctx, event)
}

func (s *Service) ProjectCommittedCaseTransition(ctx context.Context, event CommittedCaseTransition) error {
	return s.store.CommitCaseTransition(ctx, event)
}
