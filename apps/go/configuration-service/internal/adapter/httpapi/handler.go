package httpapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type HandlerConfig struct {
	MaxBodyBytes int64
}

type Handler struct {
	service  *application.Service
	verifier *Verifier
	config   HandlerConfig
	now      func() time.Time
}

func NewHandler(service *application.Service, verifier *Verifier, config HandlerConfig) (*Handler, error) {
	if service == nil || verifier == nil || config.MaxBodyBytes <= 0 {
		return nil, errors.New("configuration HTTP dependencies are invalid")
	}
	return &Handler{service: service, verifier: verifier, config: config, now: time.Now}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/configuration/profiles", h.list)
	mux.HandleFunc("POST /v1/configuration/profiles", h.create)
	mux.HandleFunc("GET /v1/configuration/profiles/{profileID}", h.get)
	mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions", h.addDraft)
	mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions/{versionID}/publish", h.publish)
	mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions/{versionID}/rollback", h.rollback)
	mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions/{versionID}/restore", h.restore)
	mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/retire", h.retire)
	mux.HandleFunc("GET /v1/configuration/profiles/{profileID}/diff", h.diff)
	return mux
}

type createRequest struct {
	Name          string          `json:"name"`
	Owner         domain.Owner    `json:"owner"`
	Scope         domain.Scope    `json:"scope"`
	SchemaVersion uint32          `json:"schema_version"`
	PolicyVersion string          `json:"policy_version"`
	Reason        string          `json:"reason"`
	Values        json.RawMessage `json:"values"`
}

type draftRequest struct {
	ExpectedVersion int64           `json:"expected_version"`
	SchemaVersion   uint32          `json:"schema_version"`
	PolicyVersion   string          `json:"policy_version"`
	Reason          string          `json:"reason"`
	Values          json.RawMessage `json:"values"`
}

type transitionRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	PolicyVersion   string `json:"policy_version,omitempty"`
	Reason          string `json:"reason"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	pageSize := 0
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil {
			writeError(w, domain.ErrInvalid)
			return
		}
	}
	page, err := h.service.List(r.Context(), actor, pageSize, r.URL.Query().Get("page_token"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	profile, versions, err := h.service.Get(r.Context(), actor, r.PathValue("profileID"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profile": profile, "versions": versionViews(versions),
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body createRequest
	if err = decodeBody(w, r, &body, h.config.MaxBodyBytes); err != nil {
		writeError(w, domain.ErrInvalid)
		return
	}
	profile, err := h.service.Create(r.Context(), actor, application.CreateInput{
		Name: body.Name, Owner: body.Owner, Scope: body.Scope, SchemaVersion: body.SchemaVersion,
		PolicyVersion: body.PolicyVersion, Reason: body.Reason, Values: body.Values,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (h *Handler) addDraft(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body draftRequest
	if err = decodeBody(w, r, &body, h.config.MaxBodyBytes); err != nil {
		writeError(w, domain.ErrInvalid)
		return
	}
	profile, err := h.service.AddDraft(r.Context(), actor, r.PathValue("profileID"), application.DraftInput{
		ExpectedVersion: body.ExpectedVersion, SchemaVersion: body.SchemaVersion,
		PolicyVersion: body.PolicyVersion, Reason: body.Reason, Values: body.Values,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body transitionRequest
	if err = decodeBody(w, r, &body, h.config.MaxBodyBytes); err != nil {
		writeError(w, domain.ErrInvalid)
		return
	}
	profile, err := h.service.Publish(
		r.Context(), actor, r.PathValue("profileID"), r.PathValue("versionID"),
		body.ExpectedVersion, body.Reason,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body transitionRequest
	if err = decodeBody(w, r, &body, h.config.MaxBodyBytes); err != nil {
		writeError(w, domain.ErrInvalid)
		return
	}
	profile, err := h.service.Rollback(
		r.Context(), actor, r.PathValue("profileID"), r.PathValue("versionID"),
		body.ExpectedVersion, body.PolicyVersion, body.Reason,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) restore(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body transitionRequest
	if err = decodeBody(w, r, &body, h.config.MaxBodyBytes); err != nil {
		writeError(w, domain.ErrInvalid)
		return
	}
	profile, err := h.service.Restore(
		r.Context(), actor, r.PathValue("profileID"), r.PathValue("versionID"),
		body.ExpectedVersion, body.PolicyVersion, body.Reason,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) retire(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body transitionRequest
	if err = decodeBody(w, r, &body, h.config.MaxBodyBytes); err != nil {
		writeError(w, domain.ErrInvalid)
		return
	}
	profile, err := h.service.Retire(
		r.Context(),
		actor,
		r.PathValue("profileID"),
		body.ExpectedVersion,
		body.Reason,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) diff(w http.ResponseWriter, r *http.Request) {
	actor, err := h.actor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := h.service.Diff(
		r.Context(),
		actor,
		r.PathValue("profileID"),
		r.URL.Query().Get("from_version"),
		r.URL.Query().Get("to_version"),
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) actor(r *http.Request) (domain.Actor, error) {
	tenantID := r.Header.Get("X-BPMP-Tenant-ID")
	correlationID := r.Header.Get("X-Correlation-ID")
	authorization := r.Header.Get("Authorization")
	if !requestmeta.ValidID(tenantID) || !requestmeta.ValidID(correlationID) ||
		!strings.HasPrefix(authorization, "Bearer ") {
		return domain.Actor{}, domain.ErrUnauthorized
	}
	identity, err := h.verifier.Verify(strings.TrimPrefix(authorization, "Bearer "), tenantID, h.now().UTC())
	if err != nil {
		return domain.Actor{}, domain.ErrUnauthorized
	}
	actor := domain.Actor{
		TenantID: identity.TenantID, ActorID: identity.ActorID,
		CorrelationID: correlationID, Capabilities: identity.Capabilities,
	}
	if transport, ok := requestmeta.FromContext(r.Context()); ok {
		actor.RequestID = transport.RequestID
		actor.TraceParent = transport.TraceParent
		actor.TraceState = transport.TraceState
	}
	if r.Method != http.MethodGet {
		actor.CommandID = r.Header.Get("X-Command-ID")
		actor.IdempotencyKey = r.Header.Get("Idempotency-Key")
		if !requestmeta.ValidID(actor.CommandID) || actor.IdempotencyKey == "" ||
			len(actor.IdempotencyKey) > 256 {
			return domain.Actor{}, domain.ErrInvalid
		}
	}
	requestmeta.Enrich(r.Context(), requestmeta.Values{
		TenantID: actor.TenantID, CommandID: actor.CommandID, ActorID: actor.ActorID,
	})
	return actor, nil
}

func versionViews(versions []domain.Version) []map[string]any {
	result := make([]map[string]any, 0, len(versions))
	for _, version := range versions {
		var values any
		if err := json.Unmarshal(version.ValuesJSON, &values); err != nil {
			values = nil
		}
		result = append(result, map[string]any{
			"id": version.ID, "ordinal": version.Ordinal, "config_version": version.ConfigVersion,
			"policy_version": version.PolicyVersion, "schema_version": version.SchemaVersion,
			"status": version.Status, "values": values,
			"content_hash": hex.EncodeToString(version.ContentHash[:]), "reason": version.Reason,
			"created_at": version.CreatedAt, "created_by": version.CreatedBy,
			"published_at": version.PublishedAt, "published_by": version.PublishedBy,
		})
	}
	return result
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code, message := "internal_error", "Internal server error"
	retryable := false
	switch {
	case errors.Is(err, domain.ErrInvalid):
		status, code, message = http.StatusBadRequest, "invalid_configuration", "Invalid configuration"
	case errors.Is(err, domain.ErrUnauthorized):
		status, code, message = http.StatusForbidden, "forbidden", "Forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, code, message = http.StatusNotFound, "configuration_not_found", "Configuration not found"
	case errors.Is(err, domain.ErrConflict):
		status, code, message = http.StatusConflict, "configuration_version_conflict", "Configuration version conflict"
	default:
		retryable = true
	}
	requestmeta.WriteProblemResponse(w, status, code, message, "", retryable)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		slog.Error("encode configuration HTTP response", "error", err)
		requestmeta.WriteProblemResponse(w, http.StatusInternalServerError, "response_encoding_failed", "Response encoding failed", "response could not be encoded", false)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Warn("write configuration HTTP response", "error", err)
	}
}
