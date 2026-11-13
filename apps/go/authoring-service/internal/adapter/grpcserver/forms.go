package grpcserver

import (
	"context"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
)

func (s *Server) ListForms(ctx context.Context, request *authoringv1.ListFormsRequest) (*authoringv1.ListFormsResponse, error) {
	items, next, err := s.service.ListForms(ctx, request.GetTenantId(), request.GetActorProof(), request.GetSearch(), request.GetPageToken(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &authoringv1.ListFormsResponse{Forms: make([]*authoringv1.FormDefinition, 0, len(items)), NextPageToken: next}
	for _, item := range items {
		response.Forms = append(response.Forms, toProtoForm(item))
	}
	return response, nil
}

func (s *Server) CreateForm(ctx context.Context, request *authoringv1.CreateFormRequest) (*authoringv1.CreateFormResponse, error) {
	form, draft, replayed, err := s.service.CreateForm(ctx, application.CreateFormRequest{TenantID: request.GetTenantId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(), FormKey: request.GetFormKey(), Name: request.GetName(), SchemaJSON: request.GetSchemaJson(), UISchemaJSON: request.GetUiSchemaJson()})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.CreateFormResponse{Form: toProtoForm(form), Draft: toProtoFormDraft(draft), Replayed: replayed}, nil
}

func (s *Server) GetFormByKey(ctx context.Context, request *authoringv1.GetFormByKeyRequest) (*authoringv1.GetFormByKeyResponse, error) {
	form, version, err := s.service.GetFormByKey(ctx, request.GetTenantId(), request.GetFormKey(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.GetFormByKeyResponse{Form: toProtoForm(form), PublishedVersion: toProtoFormVersion(version)}, nil
}

func (s *Server) GetForm(ctx context.Context, request *authoringv1.GetFormRequest) (*authoringv1.GetFormResponse, error) {
	form, draft, err := s.service.GetForm(ctx, request.GetTenantId(), request.GetFormId(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.GetFormResponse{Form: toProtoForm(form), Draft: toProtoFormDraft(draft)}, nil
}

func (s *Server) SaveFormDraft(ctx context.Context, request *authoringv1.SaveFormDraftRequest) (*authoringv1.SaveFormDraftResponse, error) {
	draft, replayed, err := s.service.SaveFormDraft(ctx, application.SaveFormDraftRequest{TenantID: request.GetTenantId(), FormID: request.GetFormId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(), ExpectedRevision: request.GetExpectedRevision(), Name: request.GetName(), SchemaJSON: request.GetSchemaJson(), UISchemaJSON: request.GetUiSchemaJson()})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.SaveFormDraftResponse{Draft: toProtoFormDraft(draft), Replayed: replayed}, nil
}

func (s *Server) ValidateFormDraft(ctx context.Context, request *authoringv1.ValidateFormDraftRequest) (*authoringv1.ValidateFormDraftResponse, error) {
	draft, _, err := s.service.ValidateFormDraft(ctx, request.GetTenantId(), request.GetFormId(), request.GetCommandId(), request.GetIdempotencyKey(), request.GetCorrelationId(), request.GetExpectedRevision(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.ValidateFormDraftResponse{FormId: request.GetFormId(), Revision: draft.Revision, Valid: draft.Validation == "VALID", Diagnostics: toProtoDiagnostics(draft.Diagnostics)}, nil
}

func (s *Server) PublishForm(ctx context.Context, request *authoringv1.PublishFormRequest) (*authoringv1.PublishFormResponse, error) {
	version, replayed, err := s.service.PublishForm(ctx, application.PublishFormRequest{TenantID: request.GetTenantId(), FormID: request.GetFormId(), CommandID: request.GetCommandId(), IdempotencyKey: request.GetIdempotencyKey(), CorrelationID: request.GetCorrelationId(), ActorProof: request.GetActorProof(), ExpectedRevision: request.GetExpectedRevision(), RevisionDigest: request.GetRevisionDigest(), VersionTag: request.GetVersionTag(), Summary: request.GetSummary()})
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.PublishFormResponse{Version: toProtoFormVersion(version), Replayed: replayed}, nil
}

func (s *Server) ListFormVersions(ctx context.Context, request *authoringv1.ListFormVersionsRequest) (*authoringv1.ListFormVersionsResponse, error) {
	items, next, err := s.service.ListFormVersions(ctx, request.GetTenantId(), request.GetFormId(), request.GetActorProof(), request.GetPageToken(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &authoringv1.ListFormVersionsResponse{Versions: make([]*authoringv1.FormVersionSummary, 0, len(items)), NextPageToken: next}
	for _, item := range items {
		response.Versions = append(response.Versions, toProtoFormVersionSummary(item))
	}
	return response, nil
}

func (s *Server) GetFormVersion(ctx context.Context, request *authoringv1.GetFormVersionRequest) (*authoringv1.GetFormVersionResponse, error) {
	version, err := s.service.GetFormVersion(ctx, request.GetTenantId(), request.GetFormId(), request.GetVersionId(), request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	return &authoringv1.GetFormVersionResponse{Version: toProtoFormVersion(version)}, nil
}

func toProtoForm(form application.FormDefinition) *authoringv1.FormDefinition {
	updated := uint64(0)
	if !form.UpdatedAt.IsZero() && form.UpdatedAt.UnixMilli() > 0 {
		updated = uint64(form.UpdatedAt.UnixMilli())
	}
	return &authoringv1.FormDefinition{Id: form.ID, TenantId: form.TenantID, FormKey: form.FormKey, Name: form.Name, Revision: form.Revision, Status: form.Status, LatestVersionId: form.LatestVersionID, UpdatedAtEpochMs: updated}
}
func toProtoFormDraft(draft application.FormDraft) *authoringv1.FormDraft {
	updated := uint64(0)
	if !draft.UpdatedAt.IsZero() && draft.UpdatedAt.UnixMilli() > 0 {
		updated = uint64(draft.UpdatedAt.UnixMilli())
	}
	return &authoringv1.FormDraft{FormId: draft.FormID, Revision: draft.Revision, SchemaJson: draft.SchemaJSON, UiSchemaJson: draft.UISchemaJSON, ValidationStatus: draft.Validation, Diagnostics: toProtoDiagnostics(draft.Diagnostics), UpdatedAtEpochMs: updated}
}
func toProtoFormVersion(version application.FormVersion) *authoringv1.FormVersion {
	created := uint64(0)
	if !version.CreatedAt.IsZero() && version.CreatedAt.UnixMilli() > 0 {
		created = uint64(version.CreatedAt.UnixMilli())
	}
	return &authoringv1.FormVersion{Id: version.ID, TenantId: version.TenantID, FormId: version.FormID, VersionNum: uint64(version.VersionNum), SourceRevision: version.SourceRevision, SchemaJson: version.SchemaJSON, UiSchemaJson: version.UISchemaJSON, ContentHash: version.ContentHash, CreatedBy: version.CreatedBy, CreatedAtEpochMs: created, VersionTag: version.VersionTag, Summary: version.Summary}
}
func toProtoFormVersionSummary(version application.FormVersionSummary) *authoringv1.FormVersionSummary {
	created := uint64(0)
	if !version.CreatedAt.IsZero() && version.CreatedAt.UnixMilli() > 0 {
		created = uint64(version.CreatedAt.UnixMilli())
	}
	return &authoringv1.FormVersionSummary{Id: version.ID, TenantId: version.TenantID, FormId: version.FormID, VersionNum: uint64(version.VersionNum), SourceRevision: version.SourceRevision, CreatedBy: version.CreatedBy, CreatedAtEpochMs: created, VersionTag: version.VersionTag, Summary: version.Summary}
}
