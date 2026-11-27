package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/master-data-service/internal/application"
)

type Handler struct {
	svc *application.Service
}

func NewHandler(svc *application.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case r.URL.Path == "/v1/mdm/datasets" && r.Method == http.MethodGet:
		ds, err := h.svc.ListDatasets(r.Context(), actorFromRequest(r))
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"datasets": ds})
	case r.URL.Path == "/v1/mdm/publications" && r.Method == http.MethodPost:
		var req application.PublishRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		pub, dup, err := h.svc.PublishDataset(r.Context(), actorFromRequest(r), req)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"publication": pub, "duplicate": dup})
	case r.URL.Path == "/v1/mdm/publications/rollback" && r.Method == http.MethodPost:
		var req application.RollbackRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		pub, err := h.svc.RollbackDataset(r.Context(), actorFromRequest(r), req)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"publication": pub})
	case r.URL.Path == "/v1/mdm/resolve-batch" && r.Method == http.MethodPost:
		var req application.ResolveBatchRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		res, err := h.svc.ResolveBatch(r.Context(), actorFromRequest(r), req)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	case strings.HasPrefix(r.URL.Path, "/v1/mdm/snapshots/") && r.Method == http.MethodGet:
		code := strings.TrimPrefix(r.URL.Path, "/v1/mdm/snapshots/")
		ver, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		pub, err := h.svc.GetSnapshot(r.Context(), actorFromRequest(r), code, ver)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, pub)
	case r.URL.Path == "/v1/mdm/consumers/ack" && r.Method == http.MethodPost:
		var body struct {
			DatasetCode string `json:"dataset_code"`
			ConsumerID  string `json:"consumer_id"`
			Version     int64  `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		ack, err := h.svc.AcknowledgeConsumer(r.Context(), actorFromRequest(r), body.DatasetCode, body.ConsumerID, body.Version)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ack)
	default:
		writeProblem(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func actorFromRequest(r *http.Request) application.Actor {
	rawCaps := strings.Split(r.Header.Get("X-BPMP-Capabilities"), ",")
	caps := make([]string, 0, len(rawCaps))
	for _, c := range rawCaps {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			caps = append(caps, trimmed)
		}
	}
	return application.Actor{
		TenantID:      strings.TrimSpace(r.Header.Get("X-BPMP-Tenant-ID")),
		ActorID:       strings.TrimSpace(r.Header.Get("X-BPMP-Actor-ID")),
		CorrelationID: strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		Capabilities:  caps,
	}
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrUnauthorized):
		writeProblem(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, application.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, application.ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": code, "status": status, "detail": detail})
}
