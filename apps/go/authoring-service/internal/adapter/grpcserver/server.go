package grpcserver

import (
	"context"
	"errors"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	authoringv1.UnimplementedAuthoringServiceServer
	service *application.Service
}

func New(service *application.Service) (*Server, error) {
	if service == nil {
		return nil, errors.New("authoring application service is required")
	}
	return &Server{service: service}, nil
}

func (s *Server) ListProcessModels(ctx context.Context, request *authoringv1.ListProcessModelsRequest) (*authoringv1.ListProcessModelsResponse, error) {
	models, next, err := s.service.ListModels(ctx, request.GetTenantId(), request.GetActorProof(), request.GetSearch(), request.GetPageToken(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &authoringv1.ListProcessModelsResponse{Models: make([]*authoringv1.ProcessModel, 0, len(models)), NextPageToken: next}
	for _, model := range models {
		response.Models = append(response.Models, toProtoModel(model))
	}
	return response, nil
}

func (s *Server) CreateProcessModel(ctx context.Context, request *authoringv1.CreateProcessModelRequest) (*authoringv1.CreateProcessModelResponse, error) {
	model, draft, _, err := s.service.CreateModel(ctx, application.CreateModelRequest{
		TenantID: request.GetTenantId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(),
		ActorProof: request.GetActorProof(), ModelKey: request.GetModelKey(), Name: request.GetName(), Category: request.GetCategory(), Description: request.GetDescription(), BPMNXML: request.GetInitialBpmnXml(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.CreateProcessModelResponse{Model: toProtoModel(model), Draft: toProtoDraft(draft)}, nil
}

func (s *Server) GetProcessModel(ctx context.Context, request *authoringv1.GetProcessModelRequest) (*authoringv1.GetProcessModelResponse, error) {
	model, draft, err := s.service.GetModel(ctx, request.GetTenantId(), request.GetProcessModelId(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.GetProcessModelResponse{Model: toProtoModel(model), Draft: toProtoDraft(draft)}, nil
}

func (s *Server) SaveProcessModelDraft(ctx context.Context, request *authoringv1.SaveProcessModelDraftRequest) (*authoringv1.SaveProcessModelDraftResponse, error) {
	draft, replayed, err := s.service.SaveDraft(ctx, application.SaveDraftRequest{
		TenantID: request.GetTenantId(), ModelID: request.GetProcessModelId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(),
		ActorProof: request.GetActorProof(), ExpectedRevision: request.GetExpectedRevision(), BaseVersionID: request.GetBaseVersionId(), BPMNXML: request.GetBpmnXml(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.SaveProcessModelDraftResponse{Draft: toProtoDraft(draft), Replayed: replayed}, nil
}

func (s *Server) ValidateProcessModelDraft(ctx context.Context, request *authoringv1.ValidateProcessModelDraftRequest) (*authoringv1.ValidateProcessModelDraftResponse, error) {
	draft, err := s.service.ValidateDraft(ctx, request.GetTenantId(), request.GetProcessModelId(), request.GetCommandId(), request.GetIdempotencyKey(), request.GetCorrelationId(), request.GetExpectedRevision(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.ValidateProcessModelDraftResponse{ProcessModelId: request.GetProcessModelId(), Revision: draft.Revision, Valid: draft.Validation == "VALID", Diagnostics: toProtoDiagnostics(draft.Diagnostics)}, nil
}

func (s *Server) CheckinProcessModel(ctx context.Context, request *authoringv1.CheckinProcessModelRequest) (*authoringv1.CheckinProcessModelResponse, error) {
	version, replayed, err := s.service.CheckinProcessModel(ctx, application.CheckinProcessModelRequest{
		TenantID: request.GetTenantId(), ModelID: request.GetProcessModelId(), CommandID: request.GetCommandId(),
		IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(),
		ExpectedRevision: request.GetExpectedRevision(), RevisionDigest: request.GetRevisionDigest(), VersionTag: request.GetVersionTag(), Summary: request.GetSummary(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.CheckinProcessModelResponse{Version: toProtoProcessModelVersion(version), Replayed: replayed}, nil
}

func toProtoProcessModelVersion(version application.ProcessModelVersion) *authoringv1.ProcessModelVersion {
	created := uint64(0)
	if !version.CreatedAt.IsZero() && version.CreatedAt.UnixMilli() > 0 {
		created = uint64(version.CreatedAt.UnixMilli())
	}
	return &authoringv1.ProcessModelVersion{Id: version.ID, TenantId: version.TenantID, ProcessModelId: version.ProcessModelID, VersionNum: uint64(version.VersionNum), SourceRevision: version.SourceRevision, BpmnXml: version.BPMNXML, ContentHash: version.ContentHash, CreatedBy: version.CreatedBy, CreatedAtEpochMs: created, VersionTag: version.VersionTag, Summary: version.Summary}
}

func (s *Server) ListProcessModelVersions(ctx context.Context, request *authoringv1.ListProcessModelVersionsRequest) (*authoringv1.ListProcessModelVersionsResponse, error) {
	items, next, err := s.service.ListProcessModelVersions(ctx, request.GetTenantId(), request.GetProcessModelId(), request.GetActorProof(), request.GetPageToken(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &authoringv1.ListProcessModelVersionsResponse{Versions: make([]*authoringv1.ProcessModelVersionSummary, 0, len(items)), NextPageToken: next}
	for _, item := range items {
		created := uint64(0)
		if !item.CreatedAt.IsZero() && item.CreatedAt.UnixMilli() > 0 {
			created = uint64(item.CreatedAt.UnixMilli())
		}
		response.Versions = append(response.Versions, &authoringv1.ProcessModelVersionSummary{Id: item.ID, TenantId: item.TenantID, ProcessModelId: item.ModelID, VersionNum: uint64(item.VersionNum), SourceRevision: item.SourceRevision, CreatedBy: item.CreatedBy, CreatedAtEpochMs: created, VersionTag: item.VersionTag, Summary: item.Summary})
	}
	return response, nil
}

func (s *Server) GetProcessModelVersion(ctx context.Context, request *authoringv1.GetProcessModelVersionRequest) (*authoringv1.GetProcessModelVersionResponse, error) {
	v, err := s.service.GetProcessModelVersion(ctx, request.GetTenantId(), request.GetProcessModelId(), request.GetVersionId(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.GetProcessModelVersionResponse{Version: toProtoProcessModelVersion(v)}, nil
}

func (s *Server) CheckinDecisionModel(ctx context.Context, request *authoringv1.CheckinDecisionModelRequest) (*authoringv1.CheckinDecisionModelResponse, error) {
	version, replayed, err := s.service.CheckinDecisionModel(ctx, application.CheckinDecisionModelRequest{
		TenantID: request.GetTenantId(), ModelID: request.GetDecisionModelId(), CommandID: request.GetCommandId(),
		IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(),
		ExpectedRevision: request.GetExpectedRevision(), RevisionDigest: request.GetRevisionDigest(), VersionTag: request.GetVersionTag(), Summary: request.GetSummary(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	created := uint64(0)
	if !version.CreatedAt.IsZero() && version.CreatedAt.UnixMilli() > 0 {
		created = uint64(version.CreatedAt.UnixMilli())
	}
	return &authoringv1.CheckinDecisionModelResponse{Version: &authoringv1.DecisionModelVersion{Id: version.ID, TenantId: version.TenantID, DecisionModelId: version.DecisionModelID, VersionNum: uint64(version.VersionNum), SourceRevision: version.SourceRevision, DmnXml: version.DMNXML, ContentHash: version.ContentHash, CreatedBy: version.CreatedBy, CreatedAtEpochMs: created, VersionTag: version.VersionTag, Summary: version.Summary}, Replayed: replayed}, nil
}

func (s *Server) ListDecisionModelVersions(ctx context.Context, request *authoringv1.ListDecisionModelVersionsRequest) (*authoringv1.ListDecisionModelVersionsResponse, error) {
	items, next, err := s.service.ListDecisionModelVersions(ctx, request.GetTenantId(), request.GetDecisionModelId(), request.GetActorProof(), request.GetPageToken(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &authoringv1.ListDecisionModelVersionsResponse{Versions: make([]*authoringv1.DecisionModelVersionSummary, 0, len(items)), NextPageToken: next}
	for _, item := range items {
		created := uint64(0)
		if !item.CreatedAt.IsZero() && item.CreatedAt.UnixMilli() > 0 {
			created = uint64(item.CreatedAt.UnixMilli())
		}
		response.Versions = append(response.Versions, &authoringv1.DecisionModelVersionSummary{Id: item.ID, TenantId: item.TenantID, DecisionModelId: item.ModelID, VersionNum: uint64(item.VersionNum), SourceRevision: item.SourceRevision, CreatedBy: item.CreatedBy, CreatedAtEpochMs: created, VersionTag: item.VersionTag, Summary: item.Summary})
	}
	return response, nil
}

func (s *Server) GetDecisionModelVersion(ctx context.Context, request *authoringv1.GetDecisionModelVersionRequest) (*authoringv1.GetDecisionModelVersionResponse, error) {
	v, err := s.service.GetDecisionModelVersion(ctx, request.GetTenantId(), request.GetDecisionModelId(), request.GetVersionId(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	created := uint64(0)
	if !v.CreatedAt.IsZero() && v.CreatedAt.UnixMilli() > 0 {
		created = uint64(v.CreatedAt.UnixMilli())
	}
	return &authoringv1.GetDecisionModelVersionResponse{Version: &authoringv1.DecisionModelVersion{Id: v.ID, TenantId: v.TenantID, DecisionModelId: v.DecisionModelID, VersionNum: uint64(v.VersionNum), SourceRevision: v.SourceRevision, DmnXml: v.DMNXML, ContentHash: v.ContentHash, CreatedBy: v.CreatedBy, CreatedAtEpochMs: created, VersionTag: v.VersionTag, Summary: v.Summary}}, nil
}

func (s *Server) ListDecisionModels(ctx context.Context, request *authoringv1.ListDecisionModelsRequest) (*authoringv1.ListDecisionModelsResponse, error) {
	models, next, err := s.service.ListDecisionModels(ctx, request.GetTenantId(), request.GetActorProof(), request.GetSearch(), request.GetPageToken(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &authoringv1.ListDecisionModelsResponse{Models: make([]*authoringv1.DecisionModel, 0, len(models)), NextPageToken: next}
	for _, model := range models {
		response.Models = append(response.Models, toProtoDecisionModel(model))
	}
	return response, nil
}

func (s *Server) CreateDecisionModel(ctx context.Context, request *authoringv1.CreateDecisionModelRequest) (*authoringv1.CreateDecisionModelResponse, error) {
	model, draft, _, err := s.service.CreateDecisionModel(ctx, application.CreateDecisionModelRequest{TenantID: request.GetTenantId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(), ModelKey: request.GetModelKey(), Name: request.GetName(), Description: request.GetDescription(), DMNXML: request.GetInitialDmnXml()})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.CreateDecisionModelResponse{Model: toProtoDecisionModel(model), Draft: toProtoDecisionDraft(draft)}, nil
}

func (s *Server) GetDecisionModel(ctx context.Context, request *authoringv1.GetDecisionModelRequest) (*authoringv1.GetDecisionModelResponse, error) {
	model, draft, err := s.service.GetDecisionModel(ctx, request.GetTenantId(), request.GetDecisionModelId(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.GetDecisionModelResponse{Model: toProtoDecisionModel(model), Draft: toProtoDecisionDraft(draft)}, nil
}

func (s *Server) SaveDecisionModelDraft(ctx context.Context, request *authoringv1.SaveDecisionModelDraftRequest) (*authoringv1.SaveDecisionModelDraftResponse, error) {
	draft, replayed, err := s.service.SaveDecisionDraft(ctx, application.SaveDecisionDraftRequest{TenantID: request.GetTenantId(), ModelID: request.GetDecisionModelId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(), ExpectedRevision: request.GetExpectedRevision(), DMNXML: request.GetDmnXml()})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.SaveDecisionModelDraftResponse{Draft: toProtoDecisionDraft(draft), Replayed: replayed}, nil
}

func (s *Server) ValidateDecisionModelDraft(ctx context.Context, request *authoringv1.ValidateDecisionModelDraftRequest) (*authoringv1.ValidateDecisionModelDraftResponse, error) {
	draft, err := s.service.ValidateDecisionDraft(ctx, request.GetTenantId(), request.GetDecisionModelId(), request.GetCommandId(), request.GetIdempotencyKey(), request.GetCorrelationId(), request.GetExpectedRevision(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.ValidateDecisionModelDraftResponse{DecisionModelId: request.GetDecisionModelId(), Revision: draft.Revision, Valid: draft.Validation == "VALID", Diagnostics: toProtoDiagnostics(draft.Diagnostics)}, nil
}

func toProtoDecisionModel(model application.DecisionModel) *authoringv1.DecisionModel {
	created, updated := uint64(0), uint64(0)
	if !model.CreatedAt.IsZero() && model.CreatedAt.UnixMilli() > 0 {
		created = uint64(model.CreatedAt.UnixMilli())
	}
	if !model.UpdatedAt.IsZero() && model.UpdatedAt.UnixMilli() > 0 {
		updated = uint64(model.UpdatedAt.UnixMilli())
	}
	return &authoringv1.DecisionModel{Id: model.ID, TenantId: model.TenantID, ModelKey: model.ModelKey, Name: model.Name, Description: model.Description, Status: model.Status, Revision: model.Revision, CreatedAtEpochMs: created, UpdatedAtEpochMs: updated, LatestVersionSourceRevision: model.LatestVersionSourceRevision}
}
func toProtoDecisionDraft(draft application.DecisionDraft) *authoringv1.DecisionModelDraft {
	updated := uint64(0)
	if !draft.UpdatedAt.IsZero() && draft.UpdatedAt.UnixMilli() > 0 {
		updated = uint64(draft.UpdatedAt.UnixMilli())
	}
	return &authoringv1.DecisionModelDraft{DecisionModelId: draft.DecisionModelID, Revision: draft.Revision, DmnXml: draft.DMNXML, ValidationStatus: draft.Validation, Diagnostics: toProtoDiagnostics(draft.Diagnostics), UpdatedAtEpochMs: updated}
}

func toProtoModel(model application.Model) *authoringv1.ProcessModel {
	created, updated := uint64(0), uint64(0)
	if !model.CreatedAt.IsZero() && model.CreatedAt.UnixMilli() > 0 {
		created = uint64(model.CreatedAt.UnixMilli())
	}
	if !model.UpdatedAt.IsZero() && model.UpdatedAt.UnixMilli() > 0 {
		updated = uint64(model.UpdatedAt.UnixMilli())
	}
	return &authoringv1.ProcessModel{Id: model.ID, TenantId: model.TenantID, ModelKey: model.ModelKey, Name: model.Name, Category: model.Category, Description: model.Description, Status: model.Status, Revision: model.Revision, LatestVersionId: model.LatestVersionID, CreatedAtEpochMs: created, UpdatedAtEpochMs: updated, IsDeleted: model.IsDeleted, LatestVersionSourceRevision: model.LatestVersionSourceRevision}
}

func toProtoDraft(draft application.Draft) *authoringv1.ProcessModelDraft {
	updated := uint64(0)
	if !draft.UpdatedAt.IsZero() && draft.UpdatedAt.UnixMilli() > 0 {
		updated = uint64(draft.UpdatedAt.UnixMilli())
	}
	return &authoringv1.ProcessModelDraft{ProcessModelId: draft.ProcessModelID, Revision: draft.Revision, BaseVersionId: draft.BaseVersionID, BpmnXml: draft.BPMNXML, ValidationStatus: draft.Validation, Diagnostics: toProtoDiagnostics(draft.Diagnostics), UpdatedAtEpochMs: updated}
}

func toProtoDiagnostics(diagnostics []application.Diagnostic) []*authoringv1.CompilerDiagnostic {
	result := make([]*authoringv1.CompilerDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, &authoringv1.CompilerDiagnostic{Code: diagnostic.Code, Severity: diagnostic.Severity, Message: diagnostic.Message, ElementId: diagnostic.ElementID, Line: diagnostic.Line, Column: diagnostic.Column})
	}
	return result
}

func mapError(err error) error {
	switch {
	case errors.Is(err, application.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, application.ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, application.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, application.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, application.ErrConflict), errors.Is(err, application.ErrIdempotencyConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, application.ErrDraftNotValidated):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, application.ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return status.Error(codes.Internal, "authoring operation failed")
	}
}

var _ authoringv1.AuthoringServiceServer = (*Server)(nil)
