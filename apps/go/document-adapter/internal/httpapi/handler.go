package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/document-adapter/internal/application"
)

const maxJSONBodyBytes = 1 << 20 // 1 MiB for metadata/callback JSON (bytes stream directly to CSP)

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
		return
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/documents/scan-callbacks/"):
		h.handleScanCallback(w, r)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/documents/uploads":
		h.handleCreateUpload(w, r)
		return
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/documents/uploads/") && strings.HasSuffix(r.URL.Path, "/finalize"):
		h.handleFinalizeUpload(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/v1/documents":
		h.handleListDocuments(w, r)
		return
	case strings.HasPrefix(r.URL.Path, "/v1/documents/"):
		h.handleDocumentItem(w, r)
		return
	default:
		writeProblem(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func actorFromRequest(r *http.Request) application.Actor {
	caps := strings.Split(r.Header.Get("X-BPMP-Capabilities"), ",")
	cleaned := make([]string, 0, len(caps))
	for _, c := range caps {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return application.Actor{
		TenantID:      strings.TrimSpace(r.Header.Get("X-BPMP-Tenant-ID")),
		ActorID:       strings.TrimSpace(r.Header.Get("X-BPMP-Actor-ID")),
		CorrelationID: strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		Capabilities:  cleaned,
	}
}

func (h *Handler) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	var req application.CreateUploadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	session, dup, err := h.svc.CreateUploadSession(r.Context(), actorFromRequest(r), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := http.StatusCreated
	if dup {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"upload_session": session, "duplicate": dup})
}

func (h *Handler) handleFinalizeUpload(w http.ResponseWriter, r *http.Request) {
	uploadID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/documents/uploads/"), "/finalize")
	var req application.FinalizeUploadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.UploadID = uploadID
	req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if ifMatch := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), "\""); ifMatch != "" {
		if v, err := strconv.ParseInt(ifMatch, 10, 64); err == nil {
			req.ExpectedVersion = v
		}
	}
	doc, dup, err := h.svc.FinalizeUpload(r.Context(), actorFromRequest(r), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"document": doc, "duplicate": dup})
}

func (h *Handler) handleScanCallback(w http.ResponseWriter, r *http.Request) {
	providerKey := strings.TrimPrefix(r.URL.Path, "/v1/documents/scan-callbacks/")
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request body too large or unreadable")
		return
	}
	receipt, err := h.svc.ProcessScanCallback(
		r.Context(),
		providerKey,
		r.Header.Get("X-Scan-Timestamp"),
		r.Header.Get("X-Scan-Signature"),
		raw,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (h *Handler) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	resp, err := h.svc.ListDocuments(r.Context(), actorFromRequest(r), application.ListDocumentsFilter{
		OwnerRefType: q.Get("owner_ref_type"),
		OwnerRefID:   q.Get("owner_ref_id"),
		Status:       application.Status(q.Get("status")),
		Limit:        limit,
		AfterID:      q.Get("after_id"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleDocumentItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/documents/")
	parts := strings.Split(rest, "/")
	docID := parts[0]
	if docID == "" {
		writeProblem(w, http.StatusNotFound, "not_found", "document id required")
		return
	}
	actor := actorFromRequest(r)

	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		doc, err := h.svc.GetDocument(r.Context(), actor, docID)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		expectedVer, _ := strconv.ParseInt(strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), "\""), 10, 64)
		doc, err := h.svc.DeleteDocument(r.Context(), actor, docID, expectedVer)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	case len(parts) == 2 && parts[1] == "download-url" && r.Method == http.MethodPost:
		resp, err := h.svc.IssueDownloadURL(r.Context(), actor, docID)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case len(parts) == 2 && parts[1] == "legal-hold" && r.Method == http.MethodPost:
		var body struct {
			Hold            bool  `json:"hold"`
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		doc, err := h.svc.SetLegalHold(r.Context(), actor, docID, body.Hold, body.ExpectedVersion)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	case len(parts) == 2 && parts[1] == "ocr" && r.Method == http.MethodPost:
		doc, err := h.svc.TriggerOCR(r.Context(), actor, docID)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, doc)
	default:
		writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported operation")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrUnauthorized), errors.Is(err, application.ErrInvalidSignature), errors.Is(err, application.ErrStaleTimestamp):
		writeProblem(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, application.ErrForbidden), errors.Is(err, application.ErrQuarantined), errors.Is(err, application.ErrLegalHoldActive):
		writeProblem(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, application.ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, application.ErrSizeLimitExceeded):
		writeProblem(w, http.StatusRequestEntityTooLarge, "payload_too_large", err.Error())
	case errors.Is(err, application.ErrUnsupportedMIME):
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", err.Error())
	case errors.Is(err, application.ErrExpired):
		writeProblem(w, http.StatusGone, "session_expired", err.Error())
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
