package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
)

const MaxFormSchemaBytes = 256 << 10

type FormDefinition struct {
	ID, TenantID, FormKey, Name, Status, LatestVersionID string
	Revision                                             int64
	UpdatedAt                                            time.Time
}

type FormDraft struct {
	FormID                   string
	Revision                 int64
	SchemaJSON, UISchemaJSON string
	Validation               string
	Diagnostics              []Diagnostic
	UpdatedAt                time.Time
}

type FormVersion struct {
	ID, TenantID, FormID, SchemaJSON, UISchemaJSON string
	VersionNum, SourceRevision                     int64
	ContentHash                                    []byte
	CreatedBy, VersionTag, Summary                 string
	CreatedAt                                      time.Time
}

type FormVersionSummary struct {
	ID, TenantID, FormID, CreatedBy, VersionTag, Summary string
	VersionNum, SourceRevision                           int64
	CreatedAt                                            time.Time
}

type formRepository interface {
	ListForms(context.Context, string, string, string, int) ([]FormDefinition, string, error)
	GetForm(context.Context, string, string) (FormDefinition, FormDraft, error)
	GetFormByKey(context.Context, string, string) (FormDefinition, FormVersion, error)
	CreateForm(context.Context, FormDefinition, FormDraft, string, string, string, string, []byte) (FormDefinition, FormDraft, bool, error)
	SaveFormDraft(context.Context, string, FormDraft, string, int64, string, string, string, string, []byte) (FormDraft, bool, error)
	SaveFormValidation(context.Context, string, string, int64, string, []Diagnostic, string, string, string, string, []byte, time.Time) (bool, error)
	PublishForm(context.Context, string, string, int64, []byte, string, string, string, string, string, string, []byte, time.Time) (FormVersion, bool, error)
	ListFormVersions(context.Context, string, string, string, int) ([]FormVersionSummary, string, error)
	GetFormVersion(context.Context, string, string, string) (FormVersion, error)
}

type CreateFormRequest struct {
	TenantID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                         *authv1.ActorProof
	FormKey, Name, SchemaJSON, UISchemaJSON            string
}

type SaveFormDraftRequest struct {
	TenantID, FormID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                 *authv1.ActorProof
	ExpectedRevision                                           int64
	Name, SchemaJSON, UISchemaJSON                             string
}

type PublishFormRequest struct {
	TenantID, FormID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                 *authv1.ActorProof
	ExpectedRevision                                           int64
	RevisionDigest, VersionTag, Summary                        string
}

func validateFormDocument(schemaJSON, uiSchemaJSON string) []Diagnostic {
	issues := make([]Diagnostic, 0)
	var schema map[string]json.RawMessage
	if len(schemaJSON) == 0 || len(schemaJSON) > MaxFormSchemaBytes || json.Unmarshal([]byte(schemaJSON), &schema) != nil || schema == nil {
		return []Diagnostic{{Code: "FORM_SCHEMA_INVALID_JSON", Severity: "ERROR", Message: "Schema must be a JSON object no larger than 256 KiB."}}
	}
	var schemaType string
	if err := json.Unmarshal(schema["type"], &schemaType); err != nil || schemaType != "object" {
		issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_ROOT_TYPE", Severity: "ERROR", Message: "Form schema root type must be object."})
	}
	var properties map[string]map[string]json.RawMessage
	if err := json.Unmarshal(schema["properties"], &properties); err != nil || len(properties) == 0 || len(properties) > 128 {
		issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_PROPERTIES", Severity: "ERROR", Message: "Schema must declare between 1 and 128 field properties."})
	} else {
		for name, property := range properties {
			if strings.TrimSpace(name) == "" || len(name) > 128 {
				issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_FIELD_NAME", Severity: "ERROR", Message: "Field names must be 1 to 128 characters."})
				continue
			}
			var fieldType string
			if err := json.Unmarshal(property["type"], &fieldType); err != nil || (fieldType != "string" && fieldType != "integer" && fieldType != "boolean" && fieldType != "array") {
				issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_FIELD_TYPE", Severity: "ERROR", Message: fmt.Sprintf("Field %q must use string, integer, boolean or array type.", name), ElementID: name})
			}
			if fieldType == "array" {
				var item map[string]json.RawMessage
				var itemType string
				if json.Unmarshal(property["items"], &item) != nil || json.Unmarshal(item["type"], &itemType) != nil || (itemType != "string" && itemType != "integer" && itemType != "boolean") {
					issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_ARRAY_ITEMS", Severity: "ERROR", Message: fmt.Sprintf("Array field %q must declare a primitive string, integer or boolean item type.", name), ElementID: name})
				}
			}
		}
	}
	var required []string
	if raw := schema["required"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &required); err != nil {
			issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_REQUIRED", Severity: "ERROR", Message: "required must be an array of property names."})
		} else {
			for _, name := range required {
				if _, ok := properties[name]; !ok {
					issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_REQUIRED_UNKNOWN", Severity: "ERROR", Message: fmt.Sprintf("Required field %q is not declared in properties.", name), ElementID: name})
				}
			}
		}
	}
	var additionalProperties bool
	additional := schema["additionalProperties"]
	if len(additional) == 0 || json.Unmarshal(additional, &additionalProperties) != nil || additionalProperties {
		issues = append(issues, Diagnostic{Code: "FORM_SCHEMA_ADDITIONAL_PROPERTIES", Severity: "ERROR", Message: "additionalProperties must be false to prevent undeclared workflow outputs."})
	}
	issues = append(issues, validateFormUISchema(uiSchemaJSON, properties)...)
	return issues
}

type formUIField struct {
	Widget      string `json:"widget"`
	Width       string `json:"width"`
	Placeholder string `json:"placeholder"`
	HelpText    string `json:"help_text"`
}

type formUISchema struct {
	Order  []string               `json:"order"`
	Fields map[string]formUIField `json:"fields"`
}

// The runtime UI schema is deliberately a small, versioned presentation profile.
// It can reorder declared fields and select a textarea without accepting HTML,
// scripts, arbitrary component names, or behavior that could bypass schema validation.
func validateFormUISchema(raw string, properties map[string]map[string]json.RawMessage) []Diagnostic {
	issue := func(code, message string, field string) []Diagnostic {
		return []Diagnostic{{Code: code, Severity: "ERROR", Message: message, ElementID: field}}
	}
	if len(raw) == 0 || len(raw) > MaxFormSchemaBytes {
		return issue("FORM_UI_SCHEMA_INVALID_JSON", "UI schema must be a JSON object no larger than 256 KiB.", "")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil || root == nil {
		return issue("FORM_UI_SCHEMA_INVALID", "UI schema must be a JSON object.", "")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var ui formUISchema
	if err := decoder.Decode(&ui); err != nil {
		return issue("FORM_UI_SCHEMA_INVALID", "UI schema supports only optional order and fields properties.", "")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return issue("FORM_UI_SCHEMA_INVALID", "UI schema must contain exactly one JSON object.", "")
	}
	issues := make([]Diagnostic, 0)
	seen := make(map[string]struct{}, len(ui.Order))
	for _, name := range ui.Order {
		if _, ok := properties[name]; !ok {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_UNKNOWN_FIELD", Severity: "ERROR", Message: fmt.Sprintf("UI order references undeclared field %q.", name), ElementID: name})
		}
		if _, duplicate := seen[name]; duplicate {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_DUPLICATE_ORDER", Severity: "ERROR", Message: fmt.Sprintf("UI order contains field %q more than once.", name), ElementID: name})
		}
		seen[name] = struct{}{}
	}
	for name, config := range ui.Fields {
		property, ok := properties[name]
		if !ok {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_UNKNOWN_FIELD", Severity: "ERROR", Message: fmt.Sprintf("UI configuration references undeclared field %q.", name), ElementID: name})
			continue
		}
		var fieldType string
		_ = json.Unmarshal(property["type"], &fieldType)
		if config.Widget != "" && config.Widget != "default" && config.Widget != "textarea" {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_WIDGET", Severity: "ERROR", Message: "Widget must be default or textarea.", ElementID: name})
		}
		if config.Widget == "textarea" && (fieldType != "string" || len(property["enum"]) != 0) {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_WIDGET_TYPE", Severity: "ERROR", Message: "Textarea is supported only for string fields without enum values.", ElementID: name})
		}
		if config.Width != "" && config.Width != "full" && config.Width != "half" {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_WIDTH", Severity: "ERROR", Message: "Field width must be full or half.", ElementID: name})
		}
		if len(config.Placeholder) > 256 || len(config.HelpText) > 512 {
			issues = append(issues, Diagnostic{Code: "FORM_UI_SCHEMA_TEXT_LIMIT", Severity: "ERROR", Message: "Placeholder is limited to 256 characters and help_text to 512 characters.", ElementID: name})
		}
	}
	return issues
}

func (s *Service) ListForms(ctx context.Context, tenantID string, proof *authv1.ActorProof, search, pageToken string, pageSize int) ([]FormDefinition, string, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return nil, "", errors.New("form repository unavailable")
	}
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
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listForm", "", AuthorizationRequirements{ReadFields: []string{"id", "form_key", "name", "status", "revision", "latest_version_id", "updated_at"}}); err != nil {
		return nil, "", ErrForbidden
	}
	return repository.ListForms(ctx, tenantID, search, pageToken, pageSize)
}

func (s *Service) GetFormByKey(ctx context.Context, tenantID, formKey string, proof *authv1.ActorProof) (FormDefinition, FormVersion, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormDefinition{}, FormVersion{}, errors.New("form repository unavailable")
	}
	if tenantID == "" || !validText(formKey, 256) {
		return FormDefinition{}, FormVersion{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return FormDefinition{}, FormVersion{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getPublishedForm", formKey, AuthorizationRequirements{ReadFields: []string{"form_key", "name", "schema_json", "ui_schema_json", "content_hash", "version_num"}}); err != nil {
		return FormDefinition{}, FormVersion{}, ErrNotFound
	}
	return repository.GetFormByKey(ctx, tenantID, strings.TrimSpace(formKey))
}

func (s *Service) CreateForm(ctx context.Context, request CreateFormRequest) (FormDefinition, FormDraft, bool, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormDefinition{}, FormDraft{}, false, errors.New("form repository unavailable")
	}
	if request.TenantID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" || !validText(request.FormKey, 256) || !validText(request.Name, 512) {
		return FormDefinition{}, FormDraft{}, false, ErrInvalidArgument
	}
	if request.UISchemaJSON == "" {
		request.UISchemaJSON = "{}"
	}
	issues := validateFormDocument(request.SchemaJSON, request.UISchemaJSON)
	if hasErrors(issues) {
		return FormDefinition{}, FormDraft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return FormDefinition{}, FormDraft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.createForm", request.FormKey, AuthorizationRequirements{WriteFields: []string{"form_key", "name", "schema_json", "ui_schema_json"}}); err != nil {
		return FormDefinition{}, FormDraft{}, false, ErrForbidden
	}
	now := s.now().UTC()
	form := FormDefinition{TenantID: request.TenantID, FormKey: strings.TrimSpace(request.FormKey), Name: strings.TrimSpace(request.Name), Status: "DRAFT", Revision: 1, UpdatedAt: now}
	draft := FormDraft{Revision: 1, SchemaJSON: request.SchemaJSON, UISchemaJSON: request.UISchemaJSON, Validation: "VALID", UpdatedAt: now}
	digest, err := digestIntent(struct{ Operation, TenantID, FormKey, Name, SchemaJSON, UISchemaJSON string }{"create_form", request.TenantID, form.FormKey, form.Name, request.SchemaJSON, request.UISchemaJSON})
	if err != nil {
		return FormDefinition{}, FormDraft{}, false, err
	}
	return repository.CreateForm(ctx, form, draft, actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, digest)
}

func (s *Service) GetForm(ctx context.Context, tenantID, formID string, proof *authv1.ActorProof) (FormDefinition, FormDraft, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormDefinition{}, FormDraft{}, ErrUnavailable
	}
	if tenantID == "" || formID == "" {
		return FormDefinition{}, FormDraft{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return FormDefinition{}, FormDraft{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getForm", formID, AuthorizationRequirements{ReadFields: []string{"id", "form_key", "name", "status", "revision", "schema_json", "ui_schema_json", "validation_status", "diagnostics"}}); err != nil {
		return FormDefinition{}, FormDraft{}, ErrNotFound
	}
	return repository.GetForm(ctx, tenantID, formID)
}

func (s *Service) SaveFormDraft(ctx context.Context, request SaveFormDraftRequest) (FormDraft, bool, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormDraft{}, false, ErrUnavailable
	}
	if request.TenantID == "" || request.FormID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" || request.ExpectedRevision < 1 || !validText(request.Name, 512) {
		return FormDraft{}, false, ErrInvalidArgument
	}
	if request.UISchemaJSON == "" {
		request.UISchemaJSON = "{}"
	}
	if hasErrors(validateFormDocument(request.SchemaJSON, request.UISchemaJSON)) {
		return FormDraft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return FormDraft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.saveFormDraft", request.FormID, AuthorizationRequirements{WriteFields: []string{"name", "schema_json", "ui_schema_json"}}); err != nil {
		return FormDraft{}, false, ErrNotFound
	}
	draft := FormDraft{FormID: request.FormID, SchemaJSON: request.SchemaJSON, UISchemaJSON: request.UISchemaJSON, Validation: "STALE", UpdatedAt: s.now().UTC()}
	digest, err := digestIntent(struct {
		Operation, TenantID, FormID, Name, SchemaJSON, UISchemaJSON string
		ExpectedRevision                                            int64
	}{"save_form_draft", request.TenantID, request.FormID, request.Name, request.SchemaJSON, request.UISchemaJSON, request.ExpectedRevision})
	if err != nil {
		return FormDraft{}, false, err
	}
	return repository.SaveFormDraft(ctx, request.TenantID, draft, strings.TrimSpace(request.Name), request.ExpectedRevision, actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, digest)
}

func (s *Service) ValidateFormDraft(ctx context.Context, tenantID, formID, commandID, idempotencyKey, correlationID string, revision int64, proof *authv1.ActorProof) (FormDraft, bool, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormDraft{}, false, ErrUnavailable
	}
	if tenantID == "" || formID == "" || commandID == "" || idempotencyKey == "" || correlationID == "" || revision < 1 {
		return FormDraft{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, commandID, proof)
	if err != nil {
		return FormDraft{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.validateFormDraft", formID, AuthorizationRequirements{ReadFields: []string{"revision", "schema_json", "ui_schema_json"}, WriteFields: []string{"validation_status", "diagnostics"}}); err != nil {
		return FormDraft{}, false, ErrNotFound
	}
	form, draft, err := repository.GetForm(ctx, tenantID, formID)
	if err != nil {
		return FormDraft{}, false, err
	}
	if form.Revision != revision || draft.Revision != revision {
		return FormDraft{}, false, ErrConflict
	}
	diagnostics := validateFormDocument(draft.SchemaJSON, draft.UISchemaJSON)
	draft.Diagnostics = diagnostics
	draft.Validation = "VALID"
	if hasErrors(diagnostics) {
		draft.Validation = "INVALID"
	}
	digest, err := digestIntent(struct {
		Operation, TenantID, FormID string
		Revision                    int64
	}{"validate_form_draft", tenantID, formID, revision})
	if err != nil {
		return FormDraft{}, false, err
	}
	replayed, err := repository.SaveFormValidation(ctx, tenantID, formID, revision, draft.Validation, diagnostics, actor.ID, commandID, idempotencyKey, correlationID, digest, s.now().UTC())
	if err != nil {
		return FormDraft{}, false, err
	}
	return draft, replayed, nil
}

func (s *Service) PublishForm(ctx context.Context, request PublishFormRequest) (FormVersion, bool, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormVersion{}, false, ErrUnavailable
	}
	if request.TenantID == "" || request.FormID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" || request.ExpectedRevision < 1 || len(request.RevisionDigest) != 64 || !validText(request.VersionTag, 128) || !validText(request.Summary, 1000) {
		return FormVersion{}, false, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return FormVersion{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.publishForm", request.FormID, AuthorizationRequirements{ReadFields: []string{"revision", "schema_json", "ui_schema_json", "validation_status"}, WriteFields: []string{"latest_version_id", "version_tag", "summary", "status"}}); err != nil {
		return FormVersion{}, false, ErrNotFound
	}
	form, draft, err := repository.GetForm(ctx, request.TenantID, request.FormID)
	if err != nil {
		return FormVersion{}, false, err
	}
	if form.Revision != request.ExpectedRevision || draft.Revision != request.ExpectedRevision {
		return FormVersion{}, false, ErrConflict
	}
	if draft.Validation != "VALID" {
		return FormVersion{}, false, ErrDraftNotValidated
	}
	digestSum := sha256.Sum256([]byte(draft.SchemaJSON + "\n" + draft.UISchemaJSON))
	digest := digestSum[:]
	if !strings.EqualFold(fmt.Sprintf("%x", digest), request.RevisionDigest) {
		return FormVersion{}, false, ErrConflict
	}
	intent, err := digestIntent(struct {
		Operation, TenantID, FormID, VersionTag, Summary string
		ExpectedRevision                                 int64
		Digest                                           string
	}{"publish_form", request.TenantID, request.FormID, request.VersionTag, request.Summary, request.ExpectedRevision, request.RevisionDigest})
	if err != nil {
		return FormVersion{}, false, err
	}
	return repository.PublishForm(ctx, request.TenantID, request.FormID, request.ExpectedRevision, digest, strings.TrimSpace(request.VersionTag), strings.TrimSpace(request.Summary), actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, intent, s.now().UTC())
}

func (s *Service) ListFormVersions(ctx context.Context, tenantID, formID string, proof *authv1.ActorProof, pageToken string, pageSize int) ([]FormVersionSummary, string, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return nil, "", ErrUnavailable
	}
	if pageSize == 0 {
		pageSize = 50
	}
	if tenantID == "" || formID == "" || pageSize < 1 || pageSize > 100 || len(pageToken) > 4096 {
		return nil, "", ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return nil, "", err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listFormVersions", formID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "created_by", "created_at", "version_tag", "summary"}}); err != nil {
		return nil, "", ErrNotFound
	}
	return repository.ListFormVersions(ctx, tenantID, formID, pageToken, pageSize)
}

func (s *Service) GetFormVersion(ctx context.Context, tenantID, formID, versionID string, proof *authv1.ActorProof) (FormVersion, error) {
	repository, ok := s.repository.(formRepository)
	if !ok {
		return FormVersion{}, ErrUnavailable
	}
	if tenantID == "" || formID == "" || versionID == "" {
		return FormVersion{}, ErrInvalidArgument
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return FormVersion{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getFormVersion", formID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "schema_json", "ui_schema_json", "content_hash", "created_by", "created_at", "version_tag", "summary"}}); err != nil {
		return FormVersion{}, ErrNotFound
	}
	return repository.GetFormVersion(ctx, tenantID, formID, versionID)
}
