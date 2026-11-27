package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/provider"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

const maxJSONBodyBytes = 1 << 20

type Handler struct {
	service *application.Service
	proof   *actorcontext.Verifier
	now     func() time.Time
}

func NewHandler(service *application.Service, proofVerifier *actorcontext.Verifier) (*Handler, error) {
	if service == nil || proofVerifier == nil {
		return nil, errors.New("notification HTTP handler requires service and actor proof verifier")
	}
	return &Handler{
		service: service,
		proof:   proofVerifier,
		now:     time.Now,
	}, nil
}

func (h *Handler) SetClock(fn func() time.Time) {
	if fn != nil {
		h.now = fn
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Categories & Applications
	mux.HandleFunc("GET /v1/notifications/categories", h.listCategories)
	mux.HandleFunc("POST /v1/notifications/categories", h.createCategory)
	mux.HandleFunc("GET /v1/notifications/applications", h.listApplications)
	mux.HandleFunc("POST /v1/notifications/applications", h.createApplication)

	// Templates, Versions & Releases
	mux.HandleFunc("GET /v1/notifications/templates", h.listTemplates)
	mux.HandleFunc("POST /v1/notifications/templates", h.createTemplate)
	mux.HandleFunc("GET /v1/notifications/templates/tags", h.listTemplateTags)
	mux.HandleFunc("POST /v1/notifications/templates/validate", h.validateTemplateDraft)
	mux.HandleFunc("POST /v1/notifications/templates/batch-actions", h.batchTemplateActions)
	mux.HandleFunc("GET /v1/notifications/templates/{templateID}", h.getTemplate)
	mux.HandleFunc("POST /v1/notifications/templates/{templateID}/restore", h.restoreTemplate)
	mux.HandleFunc("GET /v1/notifications/templates/{templateID}/versions", h.listTemplateVersions)
	mux.HandleFunc("POST /v1/notifications/templates/{templateID}/versions", h.saveTemplateVersionDraft)
	mux.HandleFunc("GET /v1/notifications/templates/{templateID}/versions/{versionID}", h.getTemplateVersion)
	mux.HandleFunc("PUT /v1/notifications/templates/{templateID}/versions/{versionID}", h.saveTemplateVersionDraft)
	mux.HandleFunc("POST /v1/notifications/templates/{templateID}/versions/{versionID}/publish", h.publishTemplateVersion)
	mux.HandleFunc("GET /v1/notifications/templates/{templateID}/releases", h.listTemplateReleases)
	mux.HandleFunc("POST /v1/notifications/templates/{templateID}/releases", h.activateTemplateRelease)

	// Trigger Rules & Variable Mapping
	mux.HandleFunc("GET /v1/notifications/trigger-rules", h.listTriggerRules)
	mux.HandleFunc("POST /v1/notifications/trigger-rules", h.createTriggerRule)
	mux.HandleFunc("PUT /v1/notifications/trigger-rules/{ruleID}/revisions", h.saveTriggerRuleRevision)
	mux.HandleFunc("POST /v1/notifications/trigger-rules/{ruleID}/revisions/{revision}/activate", h.activateTriggerRuleRevision)
	mux.HandleFunc("POST /v1/notifications/trigger-rules/{ruleID}/simulate", h.simulateTriggerRule)
	mux.HandleFunc("GET /v1/notifications/variable-mapping", h.queryVariableMapping)

	// Preview & Test Send
	mux.HandleFunc("POST /v1/notifications/preview", h.renderPreview)
	mux.HandleFunc("POST /v1/notifications/test-send", h.sendTestNotification)

	// Providers & Circuit Breakers
	mux.HandleFunc("GET /v1/notifications/providers", h.listProviders)
	mux.HandleFunc("POST /v1/notifications/providers", h.createProvider)
	mux.HandleFunc("GET /v1/notifications/providers/health", h.listCircuitBreakers)
	mux.HandleFunc("GET /v1/notifications/providers/{providerID}/circuit-breaker/transitions", h.listBreakerTransitions)
	mux.HandleFunc("POST /v1/notifications/providers/{providerID}/circuit-breaker/probe", h.probeBreaker)
	mux.HandleFunc("POST /v1/notifications/providers/{providerID}/simulate-failure", h.simulateProviderFailure)
	mux.HandleFunc("GET /v1/notifications/circuit-breakers", h.listCircuitBreakers)
	mux.HandleFunc("POST /v1/notifications/circuit-breakers/{providerID}/override", h.setBreakerOverride)

	// Send, Deliveries, Resend & Audit
	mux.HandleFunc("POST /v1/notifications/send", h.submitNotification)
	mux.HandleFunc("GET /v1/notifications/requests/{requestID}", h.getNotificationRequest)
	mux.HandleFunc("GET /v1/notifications/deliveries", h.queryDeliveries)
	mux.HandleFunc("GET /v1/notifications/deliveries/{deliveryID}", h.getDelivery)
	mux.HandleFunc("POST /v1/notifications/deliveries/{deliveryID}/resend", h.resendDelivery)
	mux.HandleFunc("GET /v1/notifications/audit", h.listAudit)

	// In-App Inbox & Preferences
	mux.HandleFunc("GET /v1/notifications/inbox", h.listInbox)
	mux.HandleFunc("POST /v1/notifications/inbox/read", h.markInboxRead)
	mux.HandleFunc("GET /v1/notifications/preferences", h.getPreferences)
	mux.HandleFunc("PUT /v1/notifications/preferences", h.updatePreferences)
	mux.HandleFunc("POST /v1/notifications/preferences", h.updatePreferences)

	return mux
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request, mutating bool, allowedCaps ...string) (actorcontext.Identity, string, string, bool) {
	tenantID := strings.TrimSpace(r.Header.Get("X-BPMP-Tenant-ID"))
	encodedProof := strings.TrimSpace(r.Header.Get("X-BPMP-Signed-Actor-Context"))
	if tenantID == "" || !requestmeta.ValidID(tenantID) || encodedProof == "" {
		writeProblem(w, http.StatusUnauthorized, "actor_context_required")
		return actorcontext.Identity{}, "", "", false
	}
	proofBytes, err := base64.StdEncoding.Strict().DecodeString(encodedProof)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "actor_context_invalid")
		return actorcontext.Identity{}, "", "", false
	}

	commandID := strings.TrimSpace(r.Header.Get("X-Command-ID"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if mutating {
		if !requestmeta.ValidID(commandID) || !requestmeta.ValidID(idempotencyKey) {
			writeProblem(w, http.StatusBadRequest, "command_identity_required")
			return actorcontext.Identity{}, "", "", false
		}
	}

	verifyCmd := ""
	if mutating {
		verifyCmd = commandID
	}
	identity, err := h.proof.Verify(proofBytes, tenantID, verifyCmd, h.now().UTC())
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "actor_context_invalid")
		return actorcontext.Identity{}, "", "", false
	}
	if len(allowedCaps) > 0 {
		matched := false
		for _, want := range allowedCaps {
			for _, got := range identity.Capabilities {
				if got == want {
					matched = true
					break
				}
			}
		}
		if !matched {
			writeProblem(w, http.StatusForbidden, "capability_required")
			return actorcontext.Identity{}, "", "", false
		}
	}
	return identity, commandID, idempotencyKey, true
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	items, err := h.service.Store().ListCategories(r.Context(), id.TenantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": nonNilSlice(items)})
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body application.Category
	if !decodeJSON(w, r, &body) {
		return
	}
	body.TenantID = id.TenantID
	body.CreatedBy = id.ActorID
	created, err := h.service.CreateCategory(r.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) listApplications(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	items, err := h.service.Store().ListApplications(r.Context(), id.TenantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": nonNilSlice(items)})
}

func (h *Handler) createApplication(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body application.Application
	if !decodeJSON(w, r, &body) {
		return
	}
	body.TenantID = id.TenantID
	body.CreatedBy = id.ActorID
	created, err := h.service.CreateApplication(r.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	q := r.URL.Query()
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	items, next, err := h.service.ListTemplateSummaries(r.Context(), application.TemplateListFilter{
		TenantID:      id.TenantID,
		ApplicationID: q.Get("application_id"),
		Channel:       q.Get("channel"),
		Tag:           q.Get("tag"),
		Status:        q.Get("status"),
		Query:         q.Get("q"),
		PageSize:      pageSize,
		PageToken:     q.Get("page_token"),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": nonNilSlice(items), "next_page_token": next})
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	tmpl, err := h.service.Store().GetTemplate(r.Context(), id.TenantID, r.PathValue("templateID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tmpl)
}

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		TemplateKey   string `json:"template_key"`
		ApplicationID string `json:"application_id"`
		CategoryID    string `json:"category_id"`
		Name          string `json:"name"`
		DefaultLocale string `json:"default_locale"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	tmpl, replayed, err := h.service.CreateTemplate(r.Context(), application.CreateTemplateInput{
		TenantID:       id.TenantID,
		ActorID:        id.ActorID,
		CommandID:      cmdID,
		IdempotencyKey: idempKey,
		TemplateKey:    body.TemplateKey,
		ApplicationID:  body.ApplicationID,
		CategoryID:     body.CategoryID,
		Name:           body.Name,
		DefaultLocale:  body.DefaultLocale,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	code := http.StatusCreated
	if replayed {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]any{"template": tmpl, "replayed": replayed})
}

func (h *Handler) listTemplateVersions(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	items, err := h.service.ListTemplateVersionsEnriched(r.Context(), id.TenantID, r.PathValue("templateID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": nonNilSlice(items)})
}

func (h *Handler) getTemplateVersion(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	tv, err := h.service.Store().GetTemplateVersion(r.Context(), id.TenantID, r.PathValue("versionID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tv)
}

func (h *Handler) saveTemplateVersionDraft(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		VersionID          string          `json:"version_id"`
		SchemaMajor        uint32          `json:"schema_major"`
		Syntax             string          `json:"syntax"`
		LayoutVersionID    string          `json:"layout_version_id"`
		VariableSchema     json.RawMessage `json:"variable_schema"`
		Variants           []struct {
			ID                 string          `json:"id"`
			Channel            string          `json:"channel"`
			Locale             string          `json:"locale"`
			VariantKind        string          `json:"variant_kind"`
			SubjectTmpl        string          `json:"subject_tmpl"`
			HTMLTmpl           string          `json:"html_tmpl"`
			TextTmpl           string          `json:"text_tmpl"`
			MJMLSource         string          `json:"mjml_source"`
			ProviderTemplateID string          `json:"provider_template_id"`
			ParamMapping       json.RawMessage `json:"param_mapping"`
		} `json:"variants"`
		SourceCommit       string `json:"source_commit"`
		ChangeNote         string `json:"change_note"`
		ExpectedRowVersion uint64 `json:"expected_row_version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	verID := r.PathValue("versionID")
	if verID == "" {
		verID = body.VersionID
	}
	variants := make([]application.TemplateVariant, 0, len(body.Variants))
	for _, v := range body.Variants {
		variants = append(variants, application.TemplateVariant{
			ID:                 v.ID,
			Channel:            v.Channel,
			Locale:             v.Locale,
			VariantKind:        v.VariantKind,
			SubjectTmpl:        v.SubjectTmpl,
			HTMLTmpl:           v.HTMLTmpl,
			TextTmpl:           v.TextTmpl,
			MJMLSource:         v.MJMLSource,
			ProviderTemplateID: v.ProviderTemplateID,
			ParamMappingJSON:   v.ParamMapping,
		})
	}
	tv, err := h.service.SaveTemplateVersionDraft(r.Context(), application.SaveDraftInput{
		TenantID:           id.TenantID,
		ActorID:            id.ActorID,
		CommandID:          cmdID,
		IdempotencyKey:     idempKey,
		TemplateID:         r.PathValue("templateID"),
		VersionID:          verID,
		SchemaMajor:        body.SchemaMajor,
		Syntax:             body.Syntax,
		LayoutVersionID:    body.LayoutVersionID,
		VariableSchemaJSON: body.VariableSchema,
		Variants:           variants,
		SourceCommit:       body.SourceCommit,
		ChangeNote:         body.ChangeNote,
		ExpectedRowVersion: body.ExpectedRowVersion,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": tv})
}

func (h *Handler) publishTemplateVersion(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		ApprovalRef string `json:"approval_ref"`
	}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	tv, replayed, err := h.service.PublishTemplateVersion(r.Context(), application.PublishVersionInput{
		TenantID:       id.TenantID,
		ActorID:        id.ActorID,
		CommandID:      cmdID,
		IdempotencyKey: idempKey,
		TemplateID:     r.PathValue("templateID"),
		VersionID:      r.PathValue("versionID"),
		ApprovalRef:    body.ApprovalRef,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": tv, "replayed": replayed})
}

func (h *Handler) listTemplateReleases(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	items, err := h.service.Store().ListTemplateReleases(r.Context(), id.TenantID, r.PathValue("templateID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": nonNilSlice(items)})
}

func (h *Handler) activateTemplateRelease(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		SchemaMajor       uint32 `json:"schema_major"`
		TemplateVersionID string `json:"template_version_id"`
		ActivateAtEpochMS int64  `json:"activate_at_epoch_ms"`
		ApprovalRef       string `json:"approval_ref"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var actAt time.Time
	if body.ActivateAtEpochMS > 0 {
		actAt = time.UnixMilli(body.ActivateAtEpochMS).UTC()
	}
	rel, replayed, err := h.service.ActivateTemplateRelease(r.Context(), application.ActivateReleaseInput{
		TenantID:          id.TenantID,
		ActorID:           id.ActorID,
		CommandID:         cmdID,
		IdempotencyKey:    idempKey,
		TemplateID:        r.PathValue("templateID"),
		SchemaMajor:       body.SchemaMajor,
		TemplateVersionID: body.TemplateVersionID,
		ActivateAt:        actAt,
		ApprovalRef:       body.ApprovalRef,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"release": rel, "replayed": replayed})
}

func (h *Handler) renderPreview(w http.ResponseWriter, r *http.Request) {
	// Preview accepts both read-style calls and command-style calls from BFF.
	hasCmd := r.Header.Get("X-Command-ID") != "" && r.Header.Get("Idempotency-Key") != ""
	id, _, _, ok := h.authenticate(w, r, hasCmd, "notification.read", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		Mode                string                       `json:"mode"`
		Syntax              string                       `json:"syntax"`
		TemplateVersionID   string                       `json:"template_version_id"`
		DeliveryID          string                       `json:"delivery_id"`
		Channel             string                       `json:"channel"`
		Locale              string                       `json:"locale"`
		DraftVariableSchema json.RawMessage              `json:"draft_variable_schema"`
		DraftVariant        *application.TemplateVariant `json:"draft_variant"`
		SampleData          json.RawMessage              `json:"sample_data"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	res, err := h.service.RenderPreview(r.Context(), application.PreviewInput{
		TenantID:                id.TenantID,
		ActorID:                 id.ActorID,
		ActorCapabilities:       id.Capabilities,
		Mode:                    body.Mode,
		Syntax:                  body.Syntax,
		TemplateVersionID:       body.TemplateVersionID,
		DeliveryID:              body.DeliveryID,
		Channel:                 body.Channel,
		Locale:                  body.Locale,
		DraftVariableSchemaJSON: body.DraftVariableSchema,
		DraftVariant:            body.DraftVariant,
		SampleDataJSON:          body.SampleData,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channel":                   res.Channel,
		"locale":                    res.Locale,
		"subject":                   res.Subject,
		"html":                      res.HTML,
		"text":                      res.Text,
		"html_bytes":                res.HTMLBytes,
		"sms_encoding":              res.SMSEncoding,
		"sms_char_count":            res.SMSCharCount,
		"sms_segments":              res.SMSSegments,
		"stripped_ascii":            res.StrippedASCII,
		"rendered_with_sample_data": res.RenderedWithSampleData,
		"warnings":                  nonNilSlice(res.Warnings),
	})
}

func (h *Handler) sendTestNotification(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		TemplateVersionID        string          `json:"template_version_id"`
		Channel                  string          `json:"channel"`
		Locale                   string          `json:"locale"`
		VerifiedRecipientAddress string          `json:"verified_recipient_address"`
		SampleData               json.RawMessage `json:"sample_data"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	reqRec, del, err := h.service.SendTestNotification(r.Context(), application.TestSendInput{
		TenantID:                 id.TenantID,
		ActorID:                  id.ActorID,
		CommandID:                cmdID,
		IdempotencyKey:           idempKey,
		TemplateVersionID:        body.TemplateVersionID,
		Channel:                  body.Channel,
		Locale:                   body.Locale,
		VerifiedRecipientAddress: body.VerifiedRecipientAddress,
		SampleDataJSON:           body.SampleData,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"request_id":  reqRec.ID,
		"delivery_id": del.ID,
		"status":      del.Status,
	})
}

func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	items, err := h.service.Store().ListProviderProfiles(r.Context(), id.TenantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": nonNilSlice(items)})
}

func (h *Handler) createProvider(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		ProviderKey    string          `json:"provider_key"`
		Name           string          `json:"name"`
		Channel        string          `json:"channel"`
		DriverKind     string          `json:"driver_kind"`
		PresetKey      string          `json:"preset_key"`
		Config         json.RawMessage `json:"config"`
		CredentialsRef string          `json:"credentials_ref"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	prof, err := h.service.RegisterProvider(r.Context(), application.ProviderProfile{
		TenantID:    id.TenantID,
		ProviderKey: body.ProviderKey,
		Name:        body.Name,
		Channel:     body.Channel,
		DriverKind:  body.DriverKind,
		PresetKey:   body.PresetKey,
	}, body.Config, body.CredentialsRef)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, prof)
}

func (h *Handler) listCircuitBreakers(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	views, err := h.service.ListProviderHealth(r.Context(), id.TenantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	dtos := make([]map[string]any, 0, len(views))
	for _, v := range views {
		dtos = append(dtos, formatProviderHealthDTO(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": dtos})
}

func (h *Handler) setBreakerOverride(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		OverrideMode string `json:"override_mode"`
		Reason       string `json:"reason"`
		TTLMS        int64  `json:"ttl_ms"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	view, err := h.service.SetBreakerOverride(r.Context(), application.SetOverrideInput{
		TenantID:       id.TenantID,
		ActorID:        id.ActorID,
		CommandID:      cmdID,
		IdempotencyKey: idempKey,
		ProviderID:     r.PathValue("providerID"),
		Mode:           provider.OverrideMode(strings.ToUpper(body.OverrideMode)),
		Reason:         body.Reason,
		TTL:            time.Duration(body.TTLMS) * time.Millisecond,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) submitNotification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DryRun           bool            `json:"dry_run"`
		TemplateKey      string          `json:"template_key"`
		SchemaMajor      uint32          `json:"schema_major"`
		PinnedVersion    uint64          `json:"pinned_version"`
		CategoryKey      string          `json:"category_key"`
		LaneOverride     string          `json:"lane_override"`
		Priority         int             `json:"priority"`
		Locale           string          `json:"locale"`
		Data             json.RawMessage `json:"data"`
		SendAtEpochMS    int64           `json:"send_at_epoch_ms"`
		ExpiresAtEpochMS int64           `json:"expires_at_epoch_ms"`
		Recipients       []struct {
			SubjectRef      string `json:"subject_ref"`
			Channel         string `json:"channel"`
			ExternalAddress string `json:"external_address"`
			DisplayName     string `json:"display_name"`
			Locale          string `json:"locale"`
			Timezone        string `json:"timezone"`
		} `json:"recipients"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	recs := make([]application.RecipientInput, 0, len(body.Recipients))
	for _, rec := range body.Recipients {
		recs = append(recs, application.RecipientInput{
			SubjectRef:      rec.SubjectRef,
			Channel:         rec.Channel,
			ExternalAddress: rec.ExternalAddress,
			DisplayName:     rec.DisplayName,
			Locale:          rec.Locale,
			Timezone:        rec.Timezone,
		})
	}
	if body.DryRun {
		id, _, _, ok := h.authenticate(w, r, false, "notification.operations.plan", "notification.send", "notification.manage", "notification.read")
		if !ok {
			return
		}
		plan, err := h.service.PlanNotification(r.Context(), application.SubmitInput{
			TenantID:      id.TenantID,
			ActorID:       id.ActorID,
			TemplateKey:   body.TemplateKey,
			SchemaMajor:   body.SchemaMajor,
			PinnedVersion: body.PinnedVersion,
			CategoryKey:   body.CategoryKey,
			LaneOverride:  body.LaneOverride,
			Priority:      body.Priority,
			Locale:        body.Locale,
			DataJSON:      body.Data,
			Recipients:    recs,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "plan": plan})
		return
	}

	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.send", "notification.manage")
	if !ok {
		return
	}
	var sendAt, expiresAt time.Time
	if body.SendAtEpochMS > 0 {
		sendAt = time.UnixMilli(body.SendAtEpochMS).UTC()
	}
	if body.ExpiresAtEpochMS > 0 {
		expiresAt = time.UnixMilli(body.ExpiresAtEpochMS).UTC()
	}
	res, err := h.service.SubmitNotification(r.Context(), application.SubmitInput{
		TenantID:       id.TenantID,
		ActorID:        id.ActorID,
		CommandID:      cmdID,
		IdempotencyKey: idempKey,
		CorrelationID:  r.Header.Get("X-Correlation-ID"),
		TemplateKey:    body.TemplateKey,
		SchemaMajor:    body.SchemaMajor,
		PinnedVersion:  body.PinnedVersion,
		CategoryKey:    body.CategoryKey,
		LaneOverride:   body.LaneOverride,
		Priority:       body.Priority,
		Locale:         body.Locale,
		DataJSON:       body.Data,
		Recipients:     recs,
		SendAt:         sendAt,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"request_id": res.Request.ID,
		"replayed":   res.Replayed,
		"status":     res.Request.Status,
		"priority":   res.Request.Priority,
		"deliveries": nonNilSlice(res.Deliveries),
	})
}

func (h *Handler) getNotificationRequest(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.send", "notification.manage")
	if !ok {
		return
	}
	rec, dels, err := h.service.Store().GetNotificationRequest(r.Context(), id.TenantID, r.PathValue("requestID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": rec, "deliveries": nonNilSlice(dels)})
}

func (h *Handler) queryDeliveries(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	var beforeAt time.Time
	if rawMS := q.Get("before_created_at_epoch_ms"); rawMS != "" {
		ms, err := strconv.ParseInt(rawMS, 10, 64)
		if err != nil || q.Get("before_id") == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		beforeAt = time.UnixMilli(ms).UTC()
	} else if rawRFC := q.Get("before_created_at"); rawRFC != "" {
		parsed, err := time.Parse(time.RFC3339Nano, rawRFC)
		if err != nil || q.Get("before_id") == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		beforeAt = parsed.UTC()
	}
	items, nextAt, nextID, err := h.service.QueryDeliveries(r.Context(), application.DeliveryQuery{
		TenantID:            id.TenantID,
		ApplicationID:       q.Get("application_id"),
		TemplateID:          q.Get("template_id"),
		Channel:             strings.ToUpper(q.Get("channel")),
		Status:              strings.ToUpper(q.Get("status")),
		CorrelationID:       q.Get("correlation_id"),
		RecipientSubjectRef: q.Get("recipient_subject_ref"),
		Limit:               limit,
		BeforeCreatedAt:     beforeAt,
		BeforeID:            q.Get("before_id"),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var nextMS int64
	if !nextAt.IsZero() {
		nextMS = nextAt.UnixMilli()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deliveries":                    nonNilSlice(items),
		"next_before_created_at_epoch_ms": nextMS,
		"next_before_id":                nextID,
	})
}

func (h *Handler) getDelivery(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	detail, err := h.service.GetDeliveryDetail(r.Context(), id.TenantID, r.PathValue("deliveryID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) resendDelivery(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.send", "notification.manage")
	if !ok {
		return
	}
	del, replayed, err := h.service.ResendDelivery(r.Context(), application.ResendInput{
		TenantID:       id.TenantID,
		ActorID:        id.ActorID,
		CommandID:      cmdID,
		IdempotencyKey: idempKey,
		DeliveryID:     r.PathValue("deliveryID"),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery": del, "replayed": replayed})
}

func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := h.service.ListAuditFiltered(r.Context(), application.AuditFilter{
		TenantID:     id.TenantID,
		ResourceType: q.Get("resource_type"),
		Severity:     q.Get("severity"),
		Query:        q.Get("q"),
		Limit:        limit,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_records": nonNilSlice(items)})
}

func (h *Handler) listInbox(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.inbox", "notification.read")
	if !ok {
		return
	}
	q := r.URL.Query()
	unreadOnly := q.Get("unread_only") == "true" || q.Get("unread_only") == "1"
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	items, next, unread, err := h.service.ListInAppMessages(r.Context(), id.TenantID, id.ActorID, unreadOnly, pageSize, q.Get("page_token"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages":        nonNilSlice(items),
		"next_page_token": next,
		"unread_count":    unread,
	})
}

func (h *Handler) markInboxRead(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.inbox", "notification.read")
	if !ok {
		return
	}
	var body struct {
		MessageIDs []string `json:"message_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.service.MarkInAppMessagesRead(r.Context(), id.TenantID, id.ActorID, body.MessageIDs)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated_count": updated})
}

func (h *Handler) getPreferences(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.inbox", "notification.read")
	if !ok {
		return
	}
	prefs, err := h.service.GetMyPreferences(r.Context(), id.TenantID, id.ActorID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": nonNilSlice(prefs)})
}

func (h *Handler) updatePreferences(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.inbox", "notification.read")
	if !ok {
		return
	}
	var body struct {
		Preferences []application.Preference `json:"preferences"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	prefs, err := h.service.UpdateMyPreferences(r.Context(), id.TenantID, id.ActorID, body.Preferences)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": nonNilSlice(prefs)})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_body")
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	requestmeta.WriteProblemResponse(
		w, status, code, strings.ReplaceAll(code, "_", " "), "",
		status >= http.StatusInternalServerError || status == http.StatusTooManyRequests,
	)
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_notification_request")
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "notification_not_found")
	case errors.Is(err, application.ErrConflict):
		writeProblem(w, http.StatusConflict, "notification_conflict")
	case errors.Is(err, application.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "notification_forbidden")
	case errors.Is(err, application.ErrQuotaExceeded):
		writeProblem(w, http.StatusTooManyRequests, "notification_quota_exceeded")
	case errors.Is(err, application.ErrImmutable):
		writeProblem(w, http.StatusConflict, "notification_artifact_immutable")
	case errors.Is(err, application.ErrIncompatibleSchema):
		writeProblem(w, http.StatusUnprocessableEntity, "notification_schema_incompatible")
	case errors.Is(err, application.ErrSecretResendForbidden):
		writeProblem(w, http.StatusUnprocessableEntity, "notification_secret_resend_forbidden")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "notification_unavailable")
	}
}

func nonNilSlice[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}
