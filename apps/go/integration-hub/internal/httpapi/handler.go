package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/integration-hub/internal/application"
)

const maxBodyBytes = 1 << 20

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
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/integration/callbacks/"):
		h.handleCallback(w, r)
	case r.URL.Path == "/v1/admin/endpoints" && r.Method == http.MethodGet:
		h.handleListEndpoints(w, r)
	case r.URL.Path == "/v1/admin/endpoints" && r.Method == http.MethodPost:
		h.handleCreateEndpoint(w, r)
	case r.URL.Path == "/v1/admin/integration/deliveries" && r.Method == http.MethodGet:
		h.handleListDeliveries(w, r)
	case r.URL.Path == "/v1/admin/integration/deliveries" && r.Method == http.MethodPost:
		h.handleDispatch(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/admin/integration/deliveries/"):
		h.handleDeliveryItem(w, r)
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

func (h *Handler) handleCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req application.CreateEndpointRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ep, err := h.svc.CreateEndpoint(r.Context(), actorFromRequest(r), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ep)
}

func (h *Handler) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := h.svc.ListEndpoints(r.Context(), actorFromRequest(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": eps})
}

func (h *Handler) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	dels, err := h.svc.ListDeliveries(r.Context(), actorFromRequest(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": dels})
}

func (h *Handler) handleDispatch(w http.ResponseWriter, r *http.Request) {
	var req application.DispatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	del, err := h.svc.DispatchOperation(r.Context(), actorFromRequest(r), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, del)
}

func (h *Handler) handleDeliveryItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/admin/integration/deliveries/")
	parts := strings.Split(rest, "/")
	deliveryID := parts[0]
	actor := actorFromRequest(r)

	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		del, err := h.svc.GetDelivery(r.Context(), actor, deliveryID)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, del)
	case len(parts) == 2 && parts[1] == "retry" && r.Method == http.MethodPost:
		var req application.RetryDeliveryRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		req.DeliveryID = deliveryID
		req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if ifMatch := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), "\""); ifMatch != "" {
			if v, err := strconv.ParseInt(ifMatch, 10, 64); err == nil {
				req.ExpectedVersion = v
			}
		}
		del, dup, err := h.svc.RetryDelivery(r.Context(), actor, req)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"delivery": del, "duplicate": dup})
	case len(parts) == 2 && parts[1] == "compensate" && r.Method == http.MethodPost:
		var body struct {
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		del, err := h.svc.CompensateDelivery(r.Context(), actor, deliveryID, body.ExpectedVersion, body.Reason)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, del)
	default:
		writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported operation")
	}
}

func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/integration/callbacks/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		writeProblem(w, http.StatusNotFound, "not_found", "invalid callback path")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "callback payload too large")
		return
	}
	receipt, err := h.svc.ProcessCallback(
		r.Context(),
		parts[0],
		parts[1],
		r.Header.Get("X-Callback-Timestamp"),
		r.Header.Get("X-Callback-Signature"),
		r.Header.Get("X-External-Event-ID"),
		raw,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrUnauthorized), errors.Is(err, application.ErrInvalidSignature), errors.Is(err, application.ErrStaleTimestamp):
		writeProblem(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, application.ErrForbidden), errors.Is(err, application.ErrSSRFBlocked):
		writeProblem(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, application.ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, application.ErrCircuitOpen), errors.Is(err, application.ErrBulkheadFull):
		writeProblem(w, http.StatusServiceUnavailable, "service_unavailable", err.Error())
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
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "https://bpmp.internal/problems/" + code,
		"title":  code,
		"status": status,
		"detail": detail,
	})
}
