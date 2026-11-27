package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/audit-projection/internal/application"
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
	case r.URL.Path == "/v1/audit/entries" && r.Method == http.MethodGet:
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		res, err := h.svc.Search(r.Context(), actorFromRequest(r), application.SearchQuery{
			SourceService: q.Get("source_service"),
			AggregateType: q.Get("aggregate_type"),
			AggregateID:   q.Get("aggregate_id"),
			ActorID:       q.Get("actor_id"),
			Limit:         limit,
			AfterEventID:  q.Get("after_event_id"),
		})
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	case r.URL.Path == "/v1/audit/rebuild" && r.Method == http.MethodPost:
		actor := actorFromRequest(r)
		if !actor.HasCapability("audit.projection.rebuild") {
			writeDomainError(w, application.ErrForbidden)
			return
		}
		var body struct {
			Events []application.CommittedOwnerEvent `json:"events"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		gen, count, err := h.svc.Rebuild(r.Context(), body.Events)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"projection_generation": gen, "rebuilt_count": count})
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
		TenantID:     strings.TrimSpace(r.Header.Get("X-BPMP-Tenant-ID")),
		ActorID:      strings.TrimSpace(r.Header.Get("X-BPMP-Actor-ID")),
		Capabilities: caps,
	}
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrUnauthorized):
		writeProblem(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, application.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "forbidden", err.Error())
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
