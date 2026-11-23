// case-local is a loopback-only development boundary for the Case MFE.
// Production traffic must pass the authenticated Portal BFF.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type handler struct {
	service *application.Service
	tenant  uuid.UUID
	actor   string
	origin  string
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8085", "loopback listen address")
	flag.Parse()
	if err := run(*listen); err != nil {
		slog.Error("Case local service stopped", "error", err)
		os.Exit(1)
	}
}

func run(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("Case local service must bind a numeric loopback address")
	}
	tenant, err := uuid.Parse(os.Getenv("CASE_LOCAL_TENANT_ID"))
	if err != nil || tenant == uuid.Nil {
		return errors.New("CASE_LOCAL_TENANT_ID must be a UUID")
	}
	actor, err := uuid.Parse(os.Getenv("CASE_LOCAL_ACTOR_ID"))
	if err != nil || actor == uuid.Nil {
		return errors.New("CASE_LOCAL_ACTOR_ID must be a UUID")
	}
	origin := os.Getenv("CASE_LOCAL_ALLOWED_ORIGIN")
	parsedOrigin, err := url.Parse(origin)
	if err != nil || parsedOrigin.Scheme != "http" || parsedOrigin.Host != "127.0.0.1:4300" || parsedOrigin.Path != "" {
		return errors.New("CASE_LOCAL_ALLOWED_ORIGIN must be http://127.0.0.1:4300")
	}
	dsn := os.Getenv("CASE_LOCAL_POSTGRES_DSN")
	if dsn == "" {
		return errors.New("CASE_LOCAL_POSTGRES_DSN is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return err
	}
	store, err := postgres.New(pool)
	if err != nil {
		return err
	}
	service, err := application.NewService(store, time.Now, uuid.New)
	if err != nil {
		return err
	}
	h := &handler{service: service, tenant: tenant, actor: actor.String(), origin: origin}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/customer-cases", h.search)
	mux.HandleFunc("POST /v1/customer-cases", h.create)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}", h.get)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}/timeline", h.timeline)
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("Case local service listening", "address", address)
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	requestOrigin := r.Header.Get("Origin")
	if (requestOrigin != h.origin && requestOrigin != "http://localhost:4300") || r.Header.Get("Idempotency-Key") == "" || r.Header.Get("X-Command-ID") == "" {
		writeProblem(w, http.StatusForbidden, "local_origin_or_command_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var body struct {
		CustomerRef     string `json:"customer_ref"`
		WorkflowType    string `json:"workflow_type"`
		WorkflowVersion string `json:"workflow_version"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_body")
		return
	}
	result, err := h.service.Create(r.Context(), application.CreateRequest{
		TenantID: h.tenant, ActorID: h.actor, IdempotencyKey: r.Header.Get("Idempotency-Key"),
		CorrelationID: r.Header.Get("X-Correlation-ID"), CustomerRef: body.CustomerRef,
		WorkflowType: body.WorkflowType, WorkflowVersion: body.WorkflowVersion,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("caseID"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_case_id")
		return
	}
	view, err := h.service.GetView(r.Context(), h.tenant, id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) timeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("caseID"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_case_id")
		return
	}
	after, err := strconv.ParseInt(defaultValue(r.URL.Query().Get("after_version"), "0"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_after_version")
		return
	}
	entries, err := h.service.Timeline(r.Context(), h.tenant, id, after, 50)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	var beforeAt time.Time
	var beforeID uuid.UUID
	var err error
	if raw := r.URL.Query().Get("before_created_at"); raw != "" {
		beforeAt, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		beforeID, err = uuid.Parse(r.URL.Query().Get("before_id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
	} else if r.URL.Query().Has("before_id") {
		writeProblem(w, http.StatusBadRequest, "invalid_cursor")
		return
	}
	limit, err := strconv.Atoi(defaultValue(r.URL.Query().Get("limit"), "25"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_limit")
		return
	}
	page, err := h.service.Search(r.Context(), h.tenant, r.URL.Query().Get("customer_ref"), beforeAt, beforeID, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": strings.ReplaceAll(code, "_", " "),
		"status": status, "code": code, "retryable": status >= 500})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_case_request")
	case errors.Is(err, application.ErrConflict):
		writeProblem(w, http.StatusConflict, "case_conflict")
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "case_not_found")
	default:
		slog.Error("Case local request failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "case_unavailable")
	}
}
