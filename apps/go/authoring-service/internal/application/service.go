package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
)

const MaxBPMNBytes = 1 << 20

var (
	ErrInvalidArgument     = errors.New("invalid authoring request")
	ErrUnauthenticated     = errors.New("actor proof rejected")
	ErrForbidden           = errors.New("authoring action denied")
	ErrNotFound            = errors.New("authoring resource not found")
	ErrConflict            = errors.New("authoring state conflict")
	ErrDraftNotValidated   = errors.New("model draft must be valid before check-in")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different intent")
	ErrUnavailable         = errors.New("authoring dependency unavailable")
)

type Model struct {
	ID                          string
	TenantID                    string
	ModelKey                    string
	Name                        string
	Category                    string
	Description                 string
	Status                      string
	Revision                    int64
	LatestVersionID             string
	LatestVersionSourceRevision int64
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
	IsDeleted                   bool
}

type Draft struct {
	ProcessModelID string
	Revision       int64
	BaseVersionID  string
	BPMNXML        string
	Validation     string
	Diagnostics    []Diagnostic
	UpdatedAt      time.Time
}

type ProcessModelVersion struct {
	ID, TenantID, ProcessModelID   string
	VersionNum, SourceRevision     int64
	BPMNXML                        string
	ContentHash                    []byte
	CreatedBy, VersionTag, Summary string
	CreatedAt                      time.Time
}

type Diagnostic struct {
	Code      string
	Severity  string
	Message   string
	ElementID string
	Line      uint64
	Column    uint64
}

type DecisionModel struct {
	ID, TenantID, ModelKey, Name, Description, Status string
	Revision                                          int64
	LatestVersionSourceRevision                       int64
	CreatedAt, UpdatedAt                              time.Time
}

type DecisionModelVersion struct {
	ID, TenantID, DecisionModelID  string
	VersionNum, SourceRevision     int64
	DMNXML                         string
	ContentHash                    []byte
	CreatedBy, VersionTag, Summary string
	CreatedAt                      time.Time
}

type ModelVersionSummary struct {
	ID, TenantID, ModelID          string
	VersionNum, SourceRevision     int64
	CreatedBy, VersionTag, Summary string
	CreatedAt                      time.Time
}

type DecisionDraft struct {
	DecisionModelID    string
	Revision           int64
	DMNXML, Validation string
	Diagnostics        []Diagnostic
	UpdatedAt          time.Time
}

type Actor struct {
	ID           string
	Roles        []string
	Capabilities []string
	RevokeEpoch  uint64
}

type ActorVerifier interface {
	Verify(context.Context, string, string, *authv1.ActorProof, time.Time) (Actor, error)
}

type Authorizer interface {
	Authorize(context.Context, string, Actor, string, string, AuthorizationRequirements) error
}

type AuthorizationRequirements struct{ ReadFields, WriteFields []string }

type Compiler interface {
	ValidateBPMN(context.Context, string, string, int64, string) ([]Diagnostic, error)
	ValidateDMN(context.Context, string, string, int64, string) ([]Diagnostic, error)
}

type Repository interface {
	ListModels(context.Context, string, string, string, int) ([]Model, string, error)
	GetModel(context.Context, string, string) (Model, Draft, error)
	CreateModel(context.Context, Model, Draft, string, string, string, string, []byte) (Model, Draft, bool, error)
	SaveDraft(context.Context, string, Draft, int64, string, string, string, string, []byte) (Draft, bool, error)
	SaveValidation(context.Context, string, string, int64, string, []Diagnostic, string, string, string, string, []byte, time.Time) error
	CheckinProcessModel(context.Context, string, string, int64, []byte, string, string, string, string, string, string, []byte, time.Time) (ProcessModelVersion, bool, error)
	ListProcessModelVersions(context.Context, string, string, string, int) ([]ModelVersionSummary, string, error)
	GetProcessModelVersion(context.Context, string, string, string) (ProcessModelVersion, error)
	CheckinDecisionModel(context.Context, string, string, int64, []byte, string, string, string, string, string, string, []byte, time.Time) (DecisionModelVersion, bool, error)
	ListDecisionModelVersions(context.Context, string, string, string, int) ([]ModelVersionSummary, string, error)
	GetDecisionModelVersion(context.Context, string, string, string) (DecisionModelVersion, error)
	ListDecisionModels(context.Context, string, string, string, int) ([]DecisionModel, string, error)
	GetDecisionModel(context.Context, string, string) (DecisionModel, DecisionDraft, error)
	CreateDecisionModel(context.Context, DecisionModel, DecisionDraft, string, string, string, string, []byte) (DecisionModel, DecisionDraft, bool, error)
	SaveDecisionDraft(context.Context, string, DecisionDraft, int64, string, string, string, string, []byte) (DecisionDraft, bool, error)
	SaveDecisionValidation(context.Context, string, string, int64, string, []Diagnostic, string, string, string, string, []byte, time.Time) error
}

type Service struct {
	repository Repository
	actors     ActorVerifier
	authorizer Authorizer
	compiler   Compiler
	now        func() time.Time
}

func New(repository Repository, actors ActorVerifier, authorizer Authorizer, compiler Compiler, now func() time.Time) (*Service, error) {
	if repository == nil || actors == nil || authorizer == nil || compiler == nil || now == nil {
		return nil, errors.New("authoring service requires repository, actor verifier, authorizer, compiler and clock")
	}
	return &Service{repository: repository, actors: actors, authorizer: authorizer, compiler: compiler, now: now}, nil
}

func (s *Service) ListModels(ctx context.Context, tenantID string, proof *authv1.ActorProof, search, pageToken string, pageSize int) ([]Model, string, error) {
	if pageSize == 0 {
		pageSize = 50
	}
	if tenantID == "" || pageSize < 1 || pageSize > 100 || len(search) > 4096 || len(pageToken) > 4096 {
		return nil, "", ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return nil, "", err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listProcessModel", "", AuthorizationRequirements{ReadFields: []string{"id", "model_key", "name", "category", "description", "status", "revision", "latest_version_id", "created_at", "updated_at"}}); err != nil {
		return nil, "", ErrForbidden
	}
	models, next, err := s.repository.ListModels(ctx, tenantID, search, pageToken, pageSize)
	if err != nil {
		return nil, "", err
	}
	return models, next, nil
}

func (s *Service) GetModel(ctx context.Context, tenantID, modelID string, proof *authv1.ActorProof) (Model, Draft, error) {
	if tenantID == "" || modelID == "" {
		return Model{}, Draft{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return Model{}, Draft{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getProcessModel", modelID, AuthorizationRequirements{ReadFields: []string{"id", "model_key", "name", "category", "description", "status", "revision", "latest_version_id", "created_at", "updated_at"}}); err != nil {
		return Model{}, Draft{}, ErrNotFound
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getProcessModelDraft", modelID, AuthorizationRequirements{ReadFields: []string{"revision", "base_version_id", "bpmn_xml", "validation_status", "diagnostics", "updated_at"}}); err != nil {
		return Model{}, Draft{}, ErrNotFound
	}
	return s.repository.GetModel(ctx, tenantID, modelID)
}

type CreateModelRequest struct {
	TenantID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                         *authv1.ActorProof
	ModelKey, Name, Category, Description, BPMNXML     string
}

func (s *Service) CreateModel(ctx context.Context, request CreateModelRequest) (Model, Draft, bool, error) {
	if request.TenantID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" ||
		!validText(request.ModelKey, 256) || !validText(request.Name, 512) || len(request.Category) > 256 || len(request.Description) > 4096 || !validBPMN(request.BPMNXML) {
		return Model{}, Draft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return Model{}, Draft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.createProcessModel", request.ModelKey, AuthorizationRequirements{WriteFields: []string{"model_key", "name", "category", "description", "initial_bpmn_xml"}}); err != nil {
		return Model{}, Draft{}, false, ErrForbidden
	}
	now := s.now().UTC()
	model := Model{TenantID: request.TenantID, ModelKey: strings.TrimSpace(request.ModelKey), Name: strings.TrimSpace(request.Name), Category: strings.TrimSpace(request.Category), Description: request.Description, Status: "DRAFT", Revision: 1, CreatedAt: now, UpdatedAt: now}
	draft := Draft{Revision: 1, BPMNXML: request.BPMNXML, Validation: "NOT_VALIDATED", UpdatedAt: now}
	digest, err := digestIntent(struct {
		Operation   string `json:"operation"`
		TenantID    string `json:"tenant_id"`
		ModelKey    string `json:"model_key"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
		BPMNXML     string `json:"bpmn_xml"`
	}{"create_process_model", request.TenantID, model.ModelKey, model.Name, model.Category, model.Description, request.BPMNXML})
	if err != nil {
		return Model{}, Draft{}, false, err
	}
	return s.repository.CreateModel(ctx, model, draft, actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, digest)
}

type SaveDraftRequest struct {
	TenantID, ModelID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                  *authv1.ActorProof
	ExpectedRevision                                            int64
	BaseVersionID, BPMNXML                                      string
}

func (s *Service) SaveDraft(ctx context.Context, request SaveDraftRequest) (Draft, bool, error) {
	if request.TenantID == "" || request.ModelID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" || request.ExpectedRevision < 1 || len(request.BaseVersionID) > 128 || !validBPMN(request.BPMNXML) {
		return Draft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return Draft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.saveProcessModelDraft", request.ModelID, AuthorizationRequirements{WriteFields: []string{"base_version_id", "bpmn_xml"}}); err != nil {
		return Draft{}, false, ErrNotFound
	}
	draft := Draft{ProcessModelID: request.ModelID, BaseVersionID: request.BaseVersionID, BPMNXML: request.BPMNXML, Validation: "STALE", UpdatedAt: s.now().UTC()}
	digest, err := digestIntent(struct {
		Operation        string `json:"operation"`
		TenantID         string `json:"tenant_id"`
		ModelID          string `json:"model_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		BaseVersionID    string `json:"base_version_id"`
		BPMNXML          string `json:"bpmn_xml"`
	}{"save_process_model_draft", request.TenantID, request.ModelID, request.ExpectedRevision, request.BaseVersionID, request.BPMNXML})
	if err != nil {
		return Draft{}, false, err
	}
	return s.repository.SaveDraft(ctx, request.TenantID, draft, request.ExpectedRevision, actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, digest)
}

func (s *Service) ValidateDraft(ctx context.Context, tenantID, modelID, commandID, idempotencyKey, correlationID string, expectedRevision int64, proof *authv1.ActorProof) (Draft, error) {
	if tenantID == "" || modelID == "" || commandID == "" || idempotencyKey == "" || correlationID == "" || expectedRevision < 1 {
		return Draft{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, commandID, proof)
	if err != nil {
		return Draft{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.validateProcessModel", modelID, AuthorizationRequirements{ReadFields: []string{"revision", "bpmn_xml"}, WriteFields: []string{"validation_status", "diagnostics"}}); err != nil {
		return Draft{}, ErrNotFound
	}
	_, draft, err := s.repository.GetModel(ctx, tenantID, modelID)
	if err != nil {
		return Draft{}, err
	}
	if draft.Revision != expectedRevision {
		return Draft{}, ErrConflict
	}
	diagnostics, compileErr := s.compiler.ValidateBPMN(ctx, tenantID, modelID, expectedRevision, draft.BPMNXML)
	if compileErr != nil {
		return Draft{}, ErrUnavailable
	}
	status := "VALID"
	if hasErrors(diagnostics) {
		status = "INVALID"
	}
	validatedAt := s.now().UTC()
	digest, err := digestIntent(struct {
		Operation        string       `json:"operation"`
		TenantID         string       `json:"tenant_id"`
		ModelID          string       `json:"model_id"`
		ExpectedRevision int64        `json:"expected_revision"`
		Status           string       `json:"status"`
		Diagnostics      []Diagnostic `json:"diagnostics"`
	}{"validate_process_model_draft", tenantID, modelID, expectedRevision, status, diagnostics})
	if err != nil {
		return Draft{}, err
	}
	if err = s.repository.SaveValidation(ctx, tenantID, modelID, expectedRevision, status, diagnostics, actor.ID, commandID, idempotencyKey, correlationID, digest, validatedAt); err != nil {
		return Draft{}, err
	}
	draft.Validation, draft.Diagnostics, draft.UpdatedAt = status, diagnostics, validatedAt
	return draft, nil
}

type CheckinProcessModelRequest struct {
	TenantID, ModelID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                  *authv1.ActorProof
	ExpectedRevision                                            int64
	RevisionDigest, VersionTag, Summary                         string
}

func (s *Service) CheckinProcessModel(ctx context.Context, request CheckinProcessModelRequest) (ProcessModelVersion, bool, error) {
	if request.TenantID == "" || request.ModelID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" || request.ExpectedRevision < 1 || !validText(request.VersionTag, 128) || !validText(request.Summary, 1000) || len(request.RevisionDigest) != 64 {
		return ProcessModelVersion{}, false, ErrInvalidArgument
	}
	requestedHash, err := hex.DecodeString(request.RevisionDigest)
	if err != nil || len(requestedHash) != sha256.Size {
		return ProcessModelVersion{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return ProcessModelVersion{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.checkinProcessModel", request.ModelID, AuthorizationRequirements{ReadFields: []string{"revision", "bpmn_xml", "validation_status"}, WriteFields: []string{"latest_version_id", "version_tag", "summary"}}); err != nil {
		return ProcessModelVersion{}, false, ErrNotFound
	}
	_, draft, err := s.repository.GetModel(ctx, request.TenantID, request.ModelID)
	if err != nil {
		return ProcessModelVersion{}, false, err
	}
	if draft.Revision != request.ExpectedRevision {
		return ProcessModelVersion{}, false, ErrConflict
	}
	if draft.Validation != "VALID" {
		return ProcessModelVersion{}, false, ErrDraftNotValidated
	}
	digest, err := digestIntent(struct {
		Operation        string `json:"operation"`
		TenantID         string `json:"tenant_id"`
		ModelID          string `json:"model_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		RevisionDigest   string `json:"revision_digest"`
		VersionTag       string `json:"version_tag"`
		Summary          string `json:"summary"`
	}{"checkin_process_model", request.TenantID, request.ModelID, request.ExpectedRevision, strings.ToLower(request.RevisionDigest), strings.TrimSpace(request.VersionTag), strings.TrimSpace(request.Summary)})
	if err != nil {
		return ProcessModelVersion{}, false, err
	}
	return s.repository.CheckinProcessModel(ctx, request.TenantID, request.ModelID, request.ExpectedRevision, requestedHash, strings.TrimSpace(request.VersionTag), strings.TrimSpace(request.Summary), actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, digest, s.now().UTC())
}

type CheckinDecisionModelRequest struct {
	TenantID, ModelID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                  *authv1.ActorProof
	ExpectedRevision                                            int64
	RevisionDigest, VersionTag, Summary                         string
}

func (s *Service) CheckinDecisionModel(ctx context.Context, request CheckinDecisionModelRequest) (DecisionModelVersion, bool, error) {
	if request.TenantID == "" || request.ModelID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" || request.ExpectedRevision < 1 || !validText(request.VersionTag, 128) || !validText(request.Summary, 1000) || len(request.RevisionDigest) != 64 {
		return DecisionModelVersion{}, false, ErrInvalidArgument
	}
	requestedHash, err := hex.DecodeString(request.RevisionDigest)
	if err != nil || len(requestedHash) != sha256.Size {
		return DecisionModelVersion{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return DecisionModelVersion{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.checkinDecisionModel", request.ModelID, AuthorizationRequirements{ReadFields: []string{"revision", "dmn_xml", "validation_status"}, WriteFields: []string{"latest_version_id", "version_tag", "summary"}}); err != nil {
		return DecisionModelVersion{}, false, ErrNotFound
	}
	_, draft, err := s.repository.GetDecisionModel(ctx, request.TenantID, request.ModelID)
	if err != nil {
		return DecisionModelVersion{}, false, err
	}
	if draft.Revision != request.ExpectedRevision {
		return DecisionModelVersion{}, false, ErrConflict
	}
	if draft.Validation != "VALID" {
		return DecisionModelVersion{}, false, ErrDraftNotValidated
	}
	digest, err := digestIntent(struct {
		Operation        string `json:"operation"`
		TenantID         string `json:"tenant_id"`
		ModelID          string `json:"model_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		RevisionDigest   string `json:"revision_digest"`
		VersionTag       string `json:"version_tag"`
		Summary          string `json:"summary"`
	}{"checkin_decision_model", request.TenantID, request.ModelID, request.ExpectedRevision, strings.ToLower(request.RevisionDigest), strings.TrimSpace(request.VersionTag), strings.TrimSpace(request.Summary)})
	if err != nil {
		return DecisionModelVersion{}, false, err
	}
	return s.repository.CheckinDecisionModel(ctx, request.TenantID, request.ModelID, request.ExpectedRevision, requestedHash, strings.TrimSpace(request.VersionTag), strings.TrimSpace(request.Summary), actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, digest, s.now().UTC())
}

func (s *Service) ListProcessModelVersions(ctx context.Context, tenantID, modelID string, proof *authv1.ActorProof, pageToken string, pageSize int) ([]ModelVersionSummary, string, error) {
	if pageSize == 0 {
		pageSize = 50
	}
	if tenantID == "" || modelID == "" || pageSize < 1 || pageSize > 100 || len(pageToken) > 4096 {
		return nil, "", ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return nil, "", err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listProcessModelVersions", modelID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "created_by", "created_at", "version_tag", "summary"}}); err != nil {
		return nil, "", ErrNotFound
	}
	return s.repository.ListProcessModelVersions(ctx, tenantID, modelID, pageToken, pageSize)
}

func (s *Service) GetProcessModelVersion(ctx context.Context, tenantID, modelID, versionID string, proof *authv1.ActorProof) (ProcessModelVersion, error) {
	if tenantID == "" || modelID == "" || versionID == "" {
		return ProcessModelVersion{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return ProcessModelVersion{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getProcessModelVersion", modelID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "bpmn_xml", "content_hash", "created_by", "created_at", "version_tag", "summary"}}); err != nil {
		return ProcessModelVersion{}, ErrNotFound
	}
	return s.repository.GetProcessModelVersion(ctx, tenantID, modelID, versionID)
}

func (s *Service) ListDecisionModelVersions(ctx context.Context, tenantID, modelID string, proof *authv1.ActorProof, pageToken string, pageSize int) ([]ModelVersionSummary, string, error) {
	if pageSize == 0 {
		pageSize = 50
	}
	if tenantID == "" || modelID == "" || pageSize < 1 || pageSize > 100 || len(pageToken) > 4096 {
		return nil, "", ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return nil, "", err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listDecisionModelVersions", modelID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "created_by", "created_at", "version_tag", "summary"}}); err != nil {
		return nil, "", ErrNotFound
	}
	return s.repository.ListDecisionModelVersions(ctx, tenantID, modelID, pageToken, pageSize)
}

func (s *Service) GetDecisionModelVersion(ctx context.Context, tenantID, modelID, versionID string, proof *authv1.ActorProof) (DecisionModelVersion, error) {
	if tenantID == "" || modelID == "" || versionID == "" {
		return DecisionModelVersion{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return DecisionModelVersion{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getDecisionModelVersion", modelID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "dmn_xml", "content_hash", "created_by", "created_at", "version_tag", "summary"}}); err != nil {
		return DecisionModelVersion{}, ErrNotFound
	}
	return s.repository.GetDecisionModelVersion(ctx, tenantID, modelID, versionID)
}

func (s *Service) ListDecisionModels(ctx context.Context, tenantID string, proof *authv1.ActorProof, search, pageToken string, pageSize int) ([]DecisionModel, string, error) {
	if pageSize == 0 {
		pageSize = 50
	}
	if tenantID == "" || pageSize < 1 || pageSize > 100 || len(search) > 4096 || len(pageToken) > 4096 {
		return nil, "", ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return nil, "", err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listDecisionModel", "", AuthorizationRequirements{ReadFields: []string{"id", "model_key", "name", "description", "status", "revision", "latest_version_source_revision", "created_at", "updated_at"}}); err != nil {
		return nil, "", ErrForbidden
	}
	return s.repository.ListDecisionModels(ctx, tenantID, search, pageToken, pageSize)
}

func (s *Service) GetDecisionModel(ctx context.Context, tenantID, modelID string, proof *authv1.ActorProof) (DecisionModel, DecisionDraft, error) {
	if tenantID == "" || modelID == "" {
		return DecisionModel{}, DecisionDraft{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return DecisionModel{}, DecisionDraft{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getDecisionModel", modelID, AuthorizationRequirements{ReadFields: []string{"id", "model_key", "name", "description", "status", "revision", "latest_version_source_revision"}}); err != nil {
		return DecisionModel{}, DecisionDraft{}, ErrNotFound
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getDecisionModelDraft", modelID, AuthorizationRequirements{ReadFields: []string{"revision", "dmn_xml", "validation_status", "diagnostics", "updated_at"}}); err != nil {
		return DecisionModel{}, DecisionDraft{}, ErrNotFound
	}
	return s.repository.GetDecisionModel(ctx, tenantID, modelID)
}

type CreateDecisionModelRequest struct {
	TenantID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                         *authv1.ActorProof
	ModelKey, Name, Description, DMNXML                string
}

func (s *Service) CreateDecisionModel(ctx context.Context, r CreateDecisionModelRequest) (DecisionModel, DecisionDraft, bool, error) {
	if r.TenantID == "" || r.CommandID == "" || r.IdempotencyKey == "" || r.CorrelationID == "" || !validText(r.ModelKey, 256) || !validText(r.Name, 512) || len(r.Description) > 4096 || !validBPMN(r.DMNXML) {
		return DecisionModel{}, DecisionDraft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, r.TenantID, r.CommandID, r.ActorProof)
	if err != nil {
		return DecisionModel{}, DecisionDraft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, r.TenantID, actor, "authoring.createDecisionModel", r.ModelKey, AuthorizationRequirements{WriteFields: []string{"model_key", "name", "description", "initial_dmn_xml"}}); err != nil {
		return DecisionModel{}, DecisionDraft{}, false, ErrForbidden
	}
	now := s.now().UTC()
	model := DecisionModel{TenantID: r.TenantID, ModelKey: strings.TrimSpace(r.ModelKey), Name: strings.TrimSpace(r.Name), Description: r.Description, Status: "DRAFT", Revision: 1, CreatedAt: now, UpdatedAt: now}
	draft := DecisionDraft{Revision: 1, DMNXML: r.DMNXML, Validation: "NOT_VALIDATED", UpdatedAt: now}
	digest, err := digestIntent(struct {
		Operation   string `json:"operation"`
		TenantID    string `json:"tenant_id"`
		ModelKey    string `json:"model_key"`
		Name        string `json:"name"`
		Description string `json:"description"`
		DMNXML      string `json:"dmn_xml"`
	}{"create_decision_model", r.TenantID, model.ModelKey, model.Name, model.Description, r.DMNXML})
	if err != nil {
		return DecisionModel{}, DecisionDraft{}, false, err
	}
	return s.repository.CreateDecisionModel(ctx, model, draft, actor.ID, r.CommandID, r.IdempotencyKey, r.CorrelationID, digest)
}

type SaveDecisionDraftRequest struct {
	TenantID, ModelID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                  *authv1.ActorProof
	ExpectedRevision                                            int64
	DMNXML                                                      string
}

func (s *Service) SaveDecisionDraft(ctx context.Context, r SaveDecisionDraftRequest) (DecisionDraft, bool, error) {
	if r.TenantID == "" || r.ModelID == "" || r.CommandID == "" || r.IdempotencyKey == "" || r.CorrelationID == "" || r.ExpectedRevision < 1 || !validBPMN(r.DMNXML) {
		return DecisionDraft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, r.TenantID, r.CommandID, r.ActorProof)
	if err != nil {
		return DecisionDraft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, r.TenantID, actor, "authoring.saveDecisionModelDraft", r.ModelID, AuthorizationRequirements{WriteFields: []string{"dmn_xml"}}); err != nil {
		return DecisionDraft{}, false, ErrNotFound
	}
	draft := DecisionDraft{DecisionModelID: r.ModelID, DMNXML: r.DMNXML, Validation: "STALE", UpdatedAt: s.now().UTC()}
	digest, err := digestIntent(struct {
		Operation        string `json:"operation"`
		TenantID         string `json:"tenant_id"`
		ModelID          string `json:"model_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		DMNXML           string `json:"dmn_xml"`
	}{"save_decision_model_draft", r.TenantID, r.ModelID, r.ExpectedRevision, r.DMNXML})
	if err != nil {
		return DecisionDraft{}, false, err
	}
	return s.repository.SaveDecisionDraft(ctx, r.TenantID, draft, r.ExpectedRevision, actor.ID, r.CommandID, r.IdempotencyKey, r.CorrelationID, digest)
}

func (s *Service) ValidateDecisionDraft(ctx context.Context, tenantID, modelID, commandID, idempotencyKey, correlationID string, expectedRevision int64, proof *authv1.ActorProof) (DecisionDraft, error) {
	if tenantID == "" || modelID == "" || commandID == "" || idempotencyKey == "" || correlationID == "" || expectedRevision < 1 {
		return DecisionDraft{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, commandID, proof)
	if err != nil {
		return DecisionDraft{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.validateDecisionModel", modelID, AuthorizationRequirements{ReadFields: []string{"revision", "dmn_xml"}, WriteFields: []string{"validation_status", "diagnostics"}}); err != nil {
		return DecisionDraft{}, ErrNotFound
	}
	_, draft, err := s.repository.GetDecisionModel(ctx, tenantID, modelID)
	if err != nil {
		return DecisionDraft{}, err
	}
	if draft.Revision != expectedRevision {
		return DecisionDraft{}, ErrConflict
	}
	diagnostics, err := s.compiler.ValidateDMN(ctx, tenantID, modelID, expectedRevision, draft.DMNXML)
	if err != nil {
		return DecisionDraft{}, ErrUnavailable
	}
	validation := "VALID"
	if hasErrors(diagnostics) {
		validation = "INVALID"
	}
	at := s.now().UTC()
	digest, err := digestIntent(struct {
		Operation        string       `json:"operation"`
		TenantID         string       `json:"tenant_id"`
		ModelID          string       `json:"model_id"`
		ExpectedRevision int64        `json:"expected_revision"`
		Status           string       `json:"status"`
		Diagnostics      []Diagnostic `json:"diagnostics"`
	}{"validate_decision_model_draft", tenantID, modelID, expectedRevision, validation, diagnostics})
	if err != nil {
		return DecisionDraft{}, err
	}
	if err = s.repository.SaveDecisionValidation(ctx, tenantID, modelID, expectedRevision, validation, diagnostics, actor.ID, commandID, idempotencyKey, correlationID, digest, at); err != nil {
		return DecisionDraft{}, err
	}
	draft.Validation, draft.Diagnostics, draft.UpdatedAt = validation, diagnostics, at
	return draft, nil
}

func (s *Service) actor(ctx context.Context, tenantID, commandID string, proof *authv1.ActorProof) (Actor, error) {
	actor, err := s.actors.Verify(ctx, tenantID, commandID, proof, s.now().UTC())
	if err != nil || actor.ID == "" {
		return Actor{}, ErrUnauthenticated
	}
	return actor, nil
}

func validText(value string, limit int) bool {
	value = strings.TrimSpace(value)
	return len(value) > 0 && len(value) <= limit
}
func validBPMN(value string) bool { return len(value) > 0 && len(value) <= MaxBPMNBytes }
func hasErrors(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if strings.EqualFold(diagnostic.Severity, "ERROR") {
			return true
		}
	}
	return false
}
func digestIntent(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}
