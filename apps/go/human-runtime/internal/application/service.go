package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	ErrForbidden               = errors.New("actor is not assigned to the work item")
	ErrActorProof              = errors.New("exactly one original token or signed actor context is required")
	ErrVersionConflict         = errors.New("work item version conflict")
	ErrIdempotencyConflict     = errors.New("command id was already used with different completion data")
	ErrInvalidCompletionOutput = errors.New("completion outputs must have unique non-empty names")
	ErrProjectionDependency    = errors.New("committed event projection dependency is missing")
	ErrPolicyLimit             = errors.New("human runtime policy limit exceeded")
	ErrAssignmentPolicy        = errors.New("assignment policy is invalid")
	ErrBulkRequest             = errors.New("bulk mutation request is invalid or unsupported")
	ErrBulkUnavailable         = errors.New("durable bulk receipt store is unavailable")
)

type RuntimePolicy struct {
	ProjectionBatchSize     int
	EscalationBatchSize     int
	EscalationLease         time.Duration
	EscalationRetry         time.Duration
	EscalationPoll          time.Duration
	EngineCommandTimeout    time.Duration
	MaxAssignmentCandidates uint32
	MaxDelegationDepth      uint32
	QueryDefaultPageSize    uint32
	QueryMaxPageSize        uint32
	EngineRetry             RetryPolicy
	EngineCircuitThreshold  uint32
	EngineCircuitOpen       time.Duration
	EngineRetryableCodes    []string
}

type RetryPolicy struct {
	MaxAttempts      uint32
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	MultiplierMillis uint32
}

type RuntimePolicyProvider interface {
	Policy(tenantID string) (RuntimePolicy, error)
}

type RuntimePolicyProviderFunc func(string) (RuntimePolicy, error)

func (function RuntimePolicyProviderFunc) Policy(tenantID string) (RuntimePolicy, error) {
	return function(tenantID)
}

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
	IdempotencyKey  string
	CorrelationID   string
	Decision        string
	FormVersionID   string
	Outputs         []*enginev1.WorkflowVariable
	ExpectedVersion int64
	Actor           ActorCredential
	ActorGroups     map[string]struct{}
	OccurredAt      time.Time
}

type DelegateRequest struct {
	TenantID        string
	WorkItemID      string
	CommandID       string
	IdempotencyKey  string
	CorrelationID   string
	ExpectedVersion int64
	Actor           ActorCredential
	ActorGroups     map[string]struct{}
	Assignment      domain.Assignment
	Reason          string
	OccurredAt      time.Time
}

type BulkAction string

const (
	BulkClaim    BulkAction = "CLAIM"
	BulkUnclaim  BulkAction = "UNCLAIM"
	BulkComplete BulkAction = "COMPLETE"
	BulkDelegate BulkAction = "DELEGATE"
)

const MaxInlineBulkItems = 20

type BulkItem struct {
	WorkItemID, Decision, FormVersionID, Reason string
	ExpectedVersion                             int64
	Outputs                                     []*enginev1.WorkflowVariable
	Assignment                                  domain.Assignment
}

type BulkItemReceipt struct {
	WorkItemID, CommandID, Status, ErrorCode string
	Retryable                                bool
	ResultingVersion                         int64
}

type BulkOperation struct {
	TenantID, ActorID, OperationID, IdempotencyKey, Action, Mode, CorrelationID string
	RequestDigest                                                               []byte
	OccurredAt                                                                  time.Time
}

type BulkReceiptStore interface {
	BeginBulkOperation(context.Context, BulkOperation, []BulkItemReceipt) (string, []BulkItemReceipt, bool, error)
	SaveBulkItemReceipt(context.Context, string, string, string, BulkItemReceipt, time.Time) error
	FindBulkCommandAudit(context.Context, string, string) (action string, version int64, found bool, err error)
}

type UpsertAssignmentPolicyRequest struct {
	TenantID, CommandID, IdempotencyKey, CorrelationID, ActorID string
	Policy                                                      domain.AssignmentPolicy
	ExpectedVersion                                             int64
	OccurredAt                                                  time.Time
}

type EngineCompleteCommand struct {
	TenantID           string
	InstanceID         string
	NodeID             string
	CommandID          string
	IdempotencyKey     string
	CorrelationID      string
	Decision           string
	FormVersionID      string
	Outputs            []*enginev1.WorkflowVariable
	WorkflowType       string
	WorkflowVersion    string
	OriginalToken      []byte
	SignedActorContext []byte
	ActorID            string
	OccurredAt         time.Time
	RuntimePolicy      RuntimePolicy
}

type CommittedCompletion struct {
	TenantID      string
	EventID       string
	Sequence      uint64
	InstanceID    string
	NodeID        string
	Decision      string
	CommandID     string
	CorrelationID string
	OccurredAt    time.Time
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

type CommittedCaseCompletion struct {
	TenantID   string
	EventID    string
	Sequence   uint64
	CaseID     string
	OccurredAt time.Time
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
	Delegate(context.Context, domain.WorkItem, string, string, string, string) error
	ProjectCase(context.Context, CommittedCase) (bool, error)
	CommitCaseTransition(context.Context, CommittedCaseTransition) error
	CommitCaseCompletion(context.Context, CommittedCaseCompletion) error
	TransitionCaseStage(context.Context, string, string, string, domain.PlanItemStatus, string, time.Time) error
	AchieveCaseMilestone(context.Context, string, string, string, string, time.Time) error
}

type assignmentPolicyWriter interface {
	UpsertAssignmentPolicy(context.Context, domain.AssignmentPolicy, int64, string, string, string, string, time.Time, []byte) (domain.AssignmentPolicy, bool, error)
}

type Service struct {
	store  Store
	engine EnginePort
	policy RuntimePolicyProvider
}

func NewService(store Store, engine EnginePort, policy RuntimePolicyProvider) (*Service, error) {
	if store == nil || engine == nil || policy == nil {
		return nil, errors.New("store, engine, and runtime policy ports are required")
	}
	return &Service{store: store, engine: engine, policy: policy}, nil
}

func (s *Service) ProjectActivation(ctx context.Context, activation domain.Activation) (domain.WorkItem, bool, error) {
	return s.store.ProjectActivation(ctx, activation)
}

func (s *Service) UpsertAssignmentPolicy(ctx context.Context, request UpsertAssignmentPolicyRequest) (domain.AssignmentPolicy, bool, error) {
	p := request.Policy
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ActorID) == "" ||
		strings.TrimSpace(request.CommandID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" ||
		strings.TrimSpace(p.Reference) == "" || strings.TrimSpace(p.WorkflowType) == "" ||
		strings.TrimSpace(p.WorkflowVersion) == "" || strings.TrimSpace(p.NodeID) == "" ||
		strings.TrimSpace(p.ConfigVersion) == "" || request.ExpectedVersion < 0 || request.OccurredAt.IsZero() {
		return domain.AssignmentPolicy{}, false, ErrAssignmentPolicy
	}
	if p.TenantID != "" && p.TenantID != request.TenantID {
		return domain.AssignmentPolicy{}, false, ErrAssignmentPolicy
	}
	if err := p.Assignment.Validate(); err != nil {
		return domain.AssignmentPolicy{}, false, ErrAssignmentPolicy
	}
	if p.SLADuration < 0 || p.SLADuration > time.Duration(math.MaxInt64)/time.Millisecond*time.Millisecond {
		return domain.AssignmentPolicy{}, false, ErrAssignmentPolicy
	}
	p.TenantID = request.TenantID
	p.Reference = strings.TrimSpace(p.Reference)
	p.WorkflowType = strings.TrimSpace(p.WorkflowType)
	p.WorkflowVersion = strings.TrimSpace(p.WorkflowVersion)
	p.NodeID = strings.TrimSpace(p.NodeID)
	p.Assignment.AssigneeID = strings.TrimSpace(p.Assignment.AssigneeID)
	p.Assignment.CandidateGroup = strings.TrimSpace(p.Assignment.CandidateGroup)
	p.EscalationPolicyRef = strings.TrimSpace(p.EscalationPolicyRef)
	p.ConfigVersion = strings.TrimSpace(p.ConfigVersion)
	digestInput := struct {
		TenantID, ActorID string
		Policy            domain.AssignmentPolicy
		ExpectedVersion   int64
	}{request.TenantID, request.ActorID, p, request.ExpectedVersion}
	encoded, err := json.Marshal(digestInput)
	if err != nil {
		return domain.AssignmentPolicy{}, false, err
	}
	digest := sha256.Sum256(encoded)
	writer, ok := s.store.(assignmentPolicyWriter)
	if !ok {
		return domain.AssignmentPolicy{}, false, errors.New("assignment policy administration is unavailable")
	}
	return writer.UpsertAssignmentPolicy(ctx, p, request.ExpectedVersion, request.CommandID,
		request.IdempotencyKey, request.CorrelationID, request.ActorID, request.OccurredAt, digest[:])
}

func (s *Service) Complete(ctx context.Context, request CompleteRequest) error {
	if err := request.Actor.Validate(); err != nil {
		return err
	}
	policy, err := s.policy.Policy(request.TenantID)
	if err != nil {
		return fmt.Errorf("load human runtime policy: %w", err)
	}
	if uint32(len(request.ActorGroups)) > policy.MaxAssignmentCandidates {
		return ErrPolicyLimit
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
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = request.CommandID
	}
	completionDigest, err := completionPayloadDigest(request.Decision, request.FormVersionID, request.Outputs)
	if err != nil {
		return err
	}
	if item.Status == domain.WorkItemCompletionRequested {
		if item.CompletionCommandID != request.CommandID || item.Decision != request.Decision || item.CompletionPayloadDigest != completionDigest {
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
		pending.CompletionPayloadDigest = completionDigest
		if err := s.store.RequestCompletion(ctx, pending, request.CommandID, request.CorrelationID, request.Actor.ActorID); err != nil {
			return err
		}
	}
	command := EngineCompleteCommand{
		TenantID: request.TenantID, InstanceID: item.InstanceID, NodeID: item.NodeID,
		CommandID: request.CommandID, IdempotencyKey: request.IdempotencyKey, CorrelationID: request.CorrelationID,
		Decision: request.Decision, FormVersionID: request.FormVersionID, ActorID: request.Actor.ActorID,
		Outputs:       request.Outputs,
		OccurredAt:    request.OccurredAt,
		RuntimePolicy: policy,
		WorkflowType:  item.WorkflowType, WorkflowVersion: item.WorkflowVersion,
		OriginalToken:      append([]byte(nil), request.Actor.OriginalSignedToken...),
		SignedActorContext: append([]byte(nil), request.Actor.SignedActorContext...),
	}
	if err := s.engine.CompleteUserTask(ctx, command); err != nil {
		return fmt.Errorf("forward complete user task: %w", err)
	}
	return nil
}

func completionPayloadDigest(decision, formVersionID string, outputs []*enginev1.WorkflowVariable) (string, error) {
	ordered := append([]*enginev1.WorkflowVariable(nil), outputs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].GetName() < ordered[j].GetName() })
	for i, output := range ordered {
		if output == nil || strings.TrimSpace(output.GetName()) == "" || (i > 0 && ordered[i-1].GetName() == output.GetName()) {
			return "", ErrInvalidCompletionOutput
		}
	}
	payload, err := protojson.Marshal(&enginev1.CompleteUserTask{Decision: decision, Outputs: ordered, FormVersionId: formVersionID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) Delegate(ctx context.Context, request DelegateRequest) error {
	if err := request.Actor.Validate(); err != nil {
		return err
	}
	if request.CommandID == "" {
		return errors.New("command id must not be empty")
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = request.CommandID
	}
	policy, err := s.policy.Policy(request.TenantID)
	if err != nil {
		return fmt.Errorf("load human runtime policy: %w", err)
	}
	if uint32(len(request.ActorGroups)) > policy.MaxAssignmentCandidates {
		return ErrPolicyLimit
	}
	item, err := s.store.GetWorkItem(ctx, request.TenantID, request.WorkItemID)
	if err != nil {
		return err
	}
	if request.ExpectedVersion != item.Version {
		return ErrVersionConflict
	}
	if !item.CanAct(request.Actor.ActorID, request.ActorGroups) {
		// A concurrent claimant may have changed the assignment after this
		// request loaded its expected version. Report that as a stale write,
		// not an authorization denial, so clients can resolve the race.
		latest, latestErr := s.store.GetWorkItem(ctx, request.TenantID, request.WorkItemID)
		if latestErr == nil && latest.Version != item.Version {
			return ErrVersionConflict
		}
		return ErrForbidden
	}
	if item.DelegationDepth >= policy.MaxDelegationDepth {
		return ErrPolicyLimit
	}
	delegated, err := domain.Delegate(item, request.Actor.ActorID, request.Assignment, request.OccurredAt)
	if err != nil {
		return err
	}
	return s.store.Delegate(ctx, delegated, request.CommandID, request.CorrelationID, request.Actor.ActorID, request.Reason)
}

func (s *Service) BulkMutate(ctx context.Context, operation BulkOperation, items []BulkItem, correlationID string, actor ActorCredential, actorGroups map[string]struct{}) (string, []BulkItemReceipt, bool, error) {
	if err := actor.Validate(); err != nil {
		return "", nil, false, err
	}
	if operation.TenantID == "" || operation.ActorID != actor.ActorID || operation.OperationID == "" || operation.IdempotencyKey == "" || operation.Mode != "PARTIAL" || len(operation.RequestDigest) != sha256.Size || len(items) == 0 || len(items) > MaxInlineBulkItems {
		return "", nil, false, ErrBulkRequest
	}
	if operation.OccurredAt.IsZero() || (operation.Action != string(BulkClaim) && operation.Action != string(BulkUnclaim) && operation.Action != string(BulkComplete) && operation.Action != string(BulkDelegate)) {
		return "", nil, false, ErrBulkRequest
	}
	journal, ok := s.store.(BulkReceiptStore)
	if !ok {
		return "", nil, false, ErrBulkUnavailable
	}
	initial := make([]BulkItemReceipt, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.WorkItemID == "" || item.ExpectedVersion < 1 {
			return "", nil, false, ErrBulkRequest
		}
		if _, exists := seen[item.WorkItemID]; exists {
			return "", nil, false, ErrBulkRequest
		}
		seen[item.WorkItemID] = struct{}{}
		switch BulkAction(operation.Action) {
		case BulkComplete:
			if strings.TrimSpace(item.Decision) == "" {
				return "", nil, false, ErrBulkRequest
			}
		case BulkDelegate:
			if err := item.Assignment.Validate(); err != nil || strings.TrimSpace(item.Reason) == "" {
				return "", nil, false, ErrBulkRequest
			}
		case BulkUnclaim:
			if strings.TrimSpace(item.Assignment.CandidateGroup) == "" || item.Assignment.AssigneeID != "" {
				return "", nil, false, ErrBulkRequest
			}
		}
		initial = append(initial, BulkItemReceipt{WorkItemID: item.WorkItemID, CommandID: bulkChildCommand(operation, item.WorkItemID), Status: "PENDING"})
	}
	operationID, stored, duplicate, err := journal.BeginBulkOperation(ctx, operation, initial)
	if err != nil {
		return "", nil, false, err
	}
	byItem := make(map[string]BulkItemReceipt, len(stored))
	for _, receipt := range stored {
		byItem[receipt.WorkItemID] = receipt
	}
	out := make([]BulkItemReceipt, 0, len(items))
	for _, item := range items {
		receipt := byItem[item.WorkItemID]
		if receipt.Status == "SUCCEEDED" || (receipt.Status == "FAILED" && !receipt.Retryable) {
			out = append(out, receipt)
			continue
		}
		if action, version, found, auditErr := journal.FindBulkCommandAudit(ctx, operation.TenantID, receipt.CommandID); auditErr != nil {
			receipt = failBulkItem(receipt, auditErr, true)
		} else if found && ((operation.Action != string(BulkComplete) && action == "DELEGATED") || (operation.Action == string(BulkComplete) && action == "COMPLETED")) {
			receipt.Status, receipt.ErrorCode, receipt.Retryable, receipt.ResultingVersion = "SUCCEEDED", "", false, version
		} else {
			err = s.runBulkItem(ctx, operation, item, receipt.CommandID, correlationID, actor, actorGroups)
			if err != nil {
				receipt = failBulkItem(receipt, err, isRetryableBulkError(err))
			} else {
				receipt.Status, receipt.ErrorCode, receipt.Retryable = "SUCCEEDED", "", false
				receipt.ResultingVersion = item.ExpectedVersion + 1
			}
		}
		if saveErr := journal.SaveBulkItemReceipt(ctx, operation.TenantID, operationID, item.WorkItemID, receipt, operation.OccurredAt); saveErr != nil {
			return operationID, out, duplicate, fmt.Errorf("persist bulk item receipt: %w", saveErr)
		}
		out = append(out, receipt)
	}
	return operationID, out, duplicate, nil
}

func (s *Service) runBulkItem(ctx context.Context, operation BulkOperation, item BulkItem, commandID, correlationID string, actor ActorCredential, actorGroups map[string]struct{}) error {
	switch BulkAction(operation.Action) {
	case BulkComplete:
		return s.Complete(ctx, CompleteRequest{
			TenantID: operation.TenantID, WorkItemID: item.WorkItemID, CommandID: commandID,
			IdempotencyKey: commandID, CorrelationID: correlationID, Decision: item.Decision,
			FormVersionID: item.FormVersionID, Outputs: item.Outputs, ExpectedVersion: item.ExpectedVersion,
			Actor: actor, ActorGroups: actorGroups, OccurredAt: operation.OccurredAt,
		})
	case BulkClaim, BulkUnclaim, BulkDelegate:
		assignment := item.Assignment
		if BulkAction(operation.Action) == BulkClaim {
			assignment = domain.Assignment{AssigneeID: actor.ActorID}
		}
		return s.Delegate(ctx, DelegateRequest{
			TenantID: operation.TenantID, WorkItemID: item.WorkItemID, CommandID: commandID,
			IdempotencyKey: commandID, CorrelationID: correlationID, ExpectedVersion: item.ExpectedVersion,
			Actor: actor, ActorGroups: actorGroups, Assignment: assignment, OccurredAt: operation.OccurredAt,
			Reason: item.Reason,
		})
	default:
		return ErrBulkRequest
	}
}

func bulkChildCommand(operation BulkOperation, itemID string) string {
	digest := sha256.Sum256([]byte(operation.TenantID + "\x00" + operation.OperationID + "\x00" + operation.Action + "\x00" + itemID))
	return "bulk-" + hex.EncodeToString(digest[:])
}

func failBulkItem(receipt BulkItemReceipt, err error, retryable bool) BulkItemReceipt {
	receipt.Status, receipt.ErrorCode, receipt.Retryable = "FAILED", errorCode(err), retryable
	return receipt
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrVersionConflict):
		return "VERSION_CONFLICT"
	case errors.Is(err, ErrForbidden):
		return "FORBIDDEN"
	case errors.Is(err, ErrIdempotencyConflict):
		return "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, ErrInvalidCompletionOutput):
		return "INVALID_OUTPUT"
	case errors.Is(err, ErrPolicyLimit):
		return "POLICY_LIMIT"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEPENDENCY_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return "REQUEST_CANCELLED"
	default:
		return "ITEM_FAILED"
	}
}

func isRetryableBulkError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	return !errors.Is(err, ErrVersionConflict) && !errors.Is(err, ErrForbidden) &&
		!errors.Is(err, ErrIdempotencyConflict) && !errors.Is(err, ErrInvalidCompletionOutput) &&
		!errors.Is(err, ErrPolicyLimit) && !errors.Is(err, ErrAssignmentPolicy) && !errors.Is(err, ErrBulkRequest)
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

func (s *Service) ProjectCommittedCaseCompletion(ctx context.Context, event CommittedCaseCompletion) error {
	return s.store.CommitCaseCompletion(ctx, event)
}
