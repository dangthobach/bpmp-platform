package humangrpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	humanv1.UnimplementedHumanRuntimeServiceServer
	service  *application.Service
	query    application.QueryPort
	verifier application.ActorVerifier
	now      func() time.Time
}

func New(service *application.Service, query application.QueryPort, verifier application.ActorVerifier, now func() time.Time) (*Server, error) {
	if service == nil || query == nil || verifier == nil || now == nil {
		return nil, errors.New("service, query, verifier, and clock are required")
	}
	return &Server{service: service, query: query, verifier: verifier, now: now}, nil
}

func (s *Server) GetWorkItem(ctx context.Context, r *humanv1.GetWorkItemRequest) (*humanv1.GetWorkItemResponse, error) {
	_, identity, err := s.verify(ctx, r.GetTenantId(), "", r.GetActorProof(), s.now())
	if err != nil {
		return nil, err
	}
	item, err := s.query.GetWorkItem(ctx, r.GetTenantId(), r.GetWorkItemId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "work item not found")
	}
	if !item.CanAct(identity.ActorID, identity.Groups) {
		return nil, status.Error(codes.PermissionDenied, "actor is not assigned to work item")
	}
	return &humanv1.GetWorkItemResponse{WorkItem: toProtoWorkItem(item)}, nil
}
func (s *Server) ListWorkItems(ctx context.Context, r *humanv1.ListWorkItemsRequest) (*humanv1.ListWorkItemsResponse, error) {
	_, identity, err := s.verify(ctx, r.GetTenantId(), "", r.GetActorProof(), s.now())
	if err != nil {
		return nil, err
	}
	cursor, err := decodeCursor(r.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid page token")
	}
	groups := make([]string, 0, len(identity.Groups))
	for group := range identity.Groups {
		groups = append(groups, group)
	}
	items, next, err := s.query.ListWorkItems(ctx, r.GetTenantId(), identity.ActorID, groups, int(r.GetPageSize()), cursor)
	if err != nil {
		return nil, status.Error(codes.Internal, "query work items")
	}
	out := make([]*humanv1.WorkItem, 0, len(items))
	for _, item := range items {
		out = append(out, toProtoWorkItem(item))
	}
	return &humanv1.ListWorkItemsResponse{WorkItems: out, NextPageToken: encodeCursor(next)}, nil
}
func (s *Server) CompleteWorkItem(ctx context.Context, r *humanv1.CompleteWorkItemRequest) (*humanv1.CompleteWorkItemResponse, error) {
	now := s.now()
	credential, identity, err := s.verify(ctx, r.GetTenantId(), r.GetCommandId(), r.GetActorProof(), now)
	if err != nil {
		return nil, err
	}
	idempotencyKey := r.GetIdempotencyKey()
	if idempotencyKey == "" {
		idempotencyKey = r.GetCommandId()
	}
	err = s.service.Complete(ctx, application.CompleteRequest{TenantID: r.GetTenantId(), WorkItemID: r.GetWorkItemId(), CommandID: r.GetCommandId(), IdempotencyKey: idempotencyKey, CorrelationID: r.GetCorrelationId(), Decision: r.GetDecision(), ExpectedVersion: r.GetExpectedVersion(), Actor: credential, ActorGroups: identity.Groups, OccurredAt: now})
	if err != nil {
		return nil, mapError(err)
	}
	return &humanv1.CompleteWorkItemResponse{CommandId: r.GetCommandId(), WorkItemVersion: r.GetExpectedVersion() + 1}, nil
}
func (s *Server) DelegateWorkItem(ctx context.Context, r *humanv1.DelegateWorkItemRequest) (*humanv1.DelegateWorkItemResponse, error) {
	now := s.now()
	credential, identity, err := s.verify(ctx, r.GetTenantId(), r.GetCommandId(), r.GetActorProof(), now)
	if err != nil {
		return nil, err
	}
	idempotencyKey := r.GetIdempotencyKey()
	if idempotencyKey == "" {
		idempotencyKey = r.GetCommandId()
	}
	err = s.service.Delegate(ctx, application.DelegateRequest{TenantID: r.GetTenantId(), WorkItemID: r.GetWorkItemId(), CommandID: r.GetCommandId(), IdempotencyKey: idempotencyKey, CorrelationID: r.GetCorrelationId(), ExpectedVersion: r.GetExpectedVersion(), Actor: credential, ActorGroups: identity.Groups, Assignment: domain.Assignment{AssigneeID: r.GetAssigneeId(), CandidateGroup: r.GetCandidateGroup()}, OccurredAt: now})
	if err != nil {
		return nil, mapError(err)
	}
	return &humanv1.DelegateWorkItemResponse{CommandId: r.GetCommandId(), WorkItemVersion: r.GetExpectedVersion() + 1}, nil
}
func (s *Server) GetCase(ctx context.Context, r *humanv1.GetCaseRequest) (*humanv1.GetCaseResponse, error) {
	if _, _, err := s.verify(ctx, r.GetTenantId(), "", r.GetActorProof(), s.now()); err != nil {
		return nil, err
	}
	view, err := s.query.GetCase(ctx, r.GetTenantId(), r.GetCaseId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "case not found")
	}
	items := make([]*humanv1.CasePlanItem, 0, len(view.Case.Stages)+len(view.Case.Milestones))
	for id, state := range view.Case.Stages {
		items = append(items, &humanv1.CasePlanItem{PlanItemId: id, Kind: "STAGE", Status: string(state)})
	}
	for id, state := range view.Case.Milestones {
		items = append(items, &humanv1.CasePlanItem{PlanItemId: id, Kind: "MILESTONE", Status: string(state)})
	}
	return &humanv1.GetCaseResponse{Case: &humanv1.CaseView{TenantId: view.Case.TenantID, CaseId: view.Case.ID, CaseType: view.Case.CaseType, Status: string(view.Case.Status), PlanItems: items, Version: view.Case.Version}}, nil
}

func (s *Server) ListAuditRecords(ctx context.Context, r *humanv1.ListAuditRecordsRequest) (*humanv1.ListAuditRecordsResponse, error) {
	_, identity, err := s.verify(ctx, r.GetTenantId(), "", r.GetActorProof(), s.now())
	if err != nil {
		return nil, err
	}
	if _, allowed := identity.Capabilities["audit.read"]; !allowed {
		return nil, status.Error(codes.PermissionDenied, "actor lacks audit.read capability")
	}
	cursor, err := decodeAuditCursor(r.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid audit page token")
	}
	records, next, err := s.query.ListAuditRecords(ctx, r.GetTenantId(), r.GetWorkItemId(), r.GetCaseId(), int(r.GetPageSize()), cursor)
	if err != nil {
		return nil, status.Error(codes.Internal, "query audit records")
	}
	out := make([]*humanv1.AuditRecord, 0, len(records))
	for _, record := range records {
		out = append(out, &humanv1.AuditRecord{
			AuditId: record.AuditID, WorkItemId: record.WorkItemID, CaseId: record.CaseID,
			ActorId: record.ActorID, Action: record.Action, OccurredAtEpochMs: uint64(record.OccurredAt.UnixMilli()),
			CommandId: record.CommandID, CorrelationId: record.CorrelationID, FromVersion: record.FromVersion,
			ToVersion: record.ToVersion, DetailsJson: append([]byte(nil), record.DetailsJSON...),
		})
	}
	return &humanv1.ListAuditRecordsResponse{Records: out, NextPageToken: encodeAuditCursor(next)}, nil
}

func (s *Server) verify(ctx context.Context, tenantID, commandID string, proof *authv1.ActorProof, evaluatedAt time.Time) (application.ActorCredential, application.ActorIdentity, error) {
	credential, err := credentialFromProof(proof)
	if err != nil {
		return application.ActorCredential{}, application.ActorIdentity{}, status.Error(codes.Unauthenticated, err.Error())
	}
	identity, err := s.verifier.VerifyActor(ctx, application.ActorVerificationRequest{TenantID: tenantID, CommandID: commandID, EvaluatedAt: evaluatedAt, Credential: credential})
	if err != nil {
		return application.ActorCredential{}, application.ActorIdentity{}, status.Error(codes.Unauthenticated, "actor proof verification failed")
	}
	credential.ActorID = identity.ActorID
	return credential, identity, nil
}

func credentialFromProof(proof *authv1.ActorProof) (application.ActorCredential, error) {
	if proof == nil || len(proof.GetSignedProof()) == 0 {
		return application.ActorCredential{}, application.ErrActorProof
	}
	switch proof.GetType() {
	case authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT:
		return application.ActorCredential{OriginalSignedToken: append([]byte(nil), proof.GetSignedProof()...)}, nil
	case authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT:
		return application.ActorCredential{SignedActorContext: append([]byte(nil), proof.GetSignedProof()...)}, nil
	default:
		return application.ActorCredential{}, application.ErrActorProof
	}
}
func toProtoWorkItem(w domain.WorkItem) *humanv1.WorkItem {
	deadline := uint64(0)
	if w.SLADeadline != nil {
		deadline = uint64(w.SLADeadline.UnixMilli())
	}
	return &humanv1.WorkItem{TenantId: w.TenantID, WorkItemId: w.ID, InstanceId: w.InstanceID, WorkflowType: w.WorkflowType, WorkflowVersion: w.WorkflowVersion, NodeId: w.NodeID, TaskType: w.TaskType, AssigneeId: w.Assignment.AssigneeID, CandidateGroup: w.Assignment.CandidateGroup, FormKey: w.FormKey, Status: string(w.Status), Decision: w.Decision, SlaDeadlineEpochMs: deadline, Version: w.Version}
}
func mapError(err error) error {
	switch {
	case errors.Is(err, application.ErrActorProof):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, application.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, application.ErrVersionConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, application.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
}
func encodeCursor(c *application.PageCursor) string {
	if c == nil {
		return ""
	}
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCursor(value string) (*application.PageCursor, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var c application.PageCursor
	if err = json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.WorkItemID == "" || c.UpdatedAt.IsZero() {
		return nil, errors.New("empty cursor")
	}
	return &c, nil
}

func encodeAuditCursor(cursor *application.AuditCursor) string {
	if cursor == nil {
		return ""
	}
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeAuditCursor(value string) (*application.AuditCursor, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var cursor application.AuditCursor
	if err = json.Unmarshal(raw, &cursor); err != nil {
		return nil, err
	}
	if cursor.AuditID == "" || cursor.OccurredAt.IsZero() {
		return nil, errors.New("empty audit cursor")
	}
	return &cursor, nil
}

var _ humanv1.HumanRuntimeServiceServer = (*Server)(nil)
