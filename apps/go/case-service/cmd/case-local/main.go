// case-local is a loopback-only development boundary for the Case MFE.
// Production traffic must pass the authenticated Portal BFF.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/postgres"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type handler struct {
	service *application.Service
	proof   *actorcontext.Verifier
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
	if err != nil || net.ParseIP(host) == nil || net.ParseIP(host).IsLoopback() || !net.ParseIP(host).IsUnspecified() {
		return errors.New("authenticated Case API must bind a Docker-network interface")
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
	keyID, workloadID := os.Getenv("CASE_ACTOR_PROOF_KEY_ID"), os.Getenv("CASE_ACTOR_WORKLOAD_ID")
	proofBytes, err := os.ReadFile(os.Getenv("CASE_ACTOR_PROOF_PUBLIC_KEY"))
	if err != nil || len(proofBytes) != ed25519.PublicKeySize || keyID == "" || workloadID != "case-service" {
		return errors.New("Case actor proof trust configuration is invalid")
	}
	proofVerifier, err := actorcontext.New(actorcontext.Config{WorkloadID: workloadID, MaxBytes: 1 << 20, MaxRoles: 256, MaxCaps: 512,
		ClockSkew: 30 * time.Second, Keys: map[string]ed25519.PublicKey{keyID: ed25519.PublicKey(proofBytes)}})
	if err != nil {
		return err
	}
	h := &handler{service: service, proof: proofVerifier}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/customer-cases", h.search)
	mux.HandleFunc("POST /v1/customer-cases", h.create)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}", h.get)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}/timeline", h.timeline)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}/process-events", h.processEvents)
	certificate, err := tls.LoadX509KeyPair(os.Getenv("CASE_SERVER_CERTIFICATE"), os.Getenv("CASE_SERVER_PRIVATE_KEY"))
	if err != nil {
		return errors.New("Case API TLS identity is invalid")
	}
	caBytes, err := os.ReadFile(os.Getenv("CASE_CLIENT_CA"))
	if err != nil {
		return errors.New("Case API client CA is unavailable")
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(caBytes) {
		return errors.New("Case API client CA is invalid")
	}
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}}
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
	slog.Info("authenticated Case API listening", "address", address)
	err = server.ServeTLS(listener, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (h *handler) processEvents(w http.ResponseWriter, r *http.Request) {
	tenant, _, ok := h.authenticateProof(w, r, "", "case.read")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("caseID"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_case_id")
		return
	}
	after, err := strconv.ParseInt(defaultValue(r.URL.Query().Get("after_sequence"), "0"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_after_sequence")
		return
	}
	limit, err := strconv.Atoi(defaultValue(r.URL.Query().Get("limit"), "50"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_limit")
		return
	}
	page, err := h.service.ProcessEvents(r.Context(), tenant, id, after, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	tenant, actor, ok := h.authenticateProof(w, r, r.Header.Get("X-Command-ID"), "case.create")
	if !ok {
		return
	}
	if r.Header.Get("Idempotency-Key") == "" || r.Header.Get("X-Command-ID") == "" {
		writeProblem(w, http.StatusBadRequest, "command_identity_required")
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
		TenantID: tenant, ActorID: actor, IdempotencyKey: r.Header.Get("Idempotency-Key"),
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
	tenant, _, ok := h.authenticateProof(w, r, "", "case.read")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("caseID"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_case_id")
		return
	}
	view, err := h.service.GetView(r.Context(), tenant, id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) timeline(w http.ResponseWriter, r *http.Request) {
	tenant, _, ok := h.authenticateProof(w, r, "", "case.read")
	if !ok {
		return
	}
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
	entries, err := h.service.Timeline(r.Context(), tenant, id, after, 50)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	tenant, _, ok := h.authenticateProof(w, r, "", "case.read")
	if !ok {
		return
	}
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
	page, err := h.service.Search(r.Context(), tenant, r.URL.Query().Get("customer_ref"), beforeAt, beforeID, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handler) authenticateProof(w http.ResponseWriter, r *http.Request, commandID, capability string) (uuid.UUID, string, bool) {
	tenantText := r.Header.Get("X-BPMP-Tenant-ID")
	tenant, err := uuid.Parse(tenantText)
	proofBytes, decodeErr := base64.StdEncoding.Strict().DecodeString(r.Header.Get("X-BPMP-Signed-Actor-Context"))
	if err != nil || tenant == uuid.Nil || decodeErr != nil {
		writeProblem(w, http.StatusUnauthorized, "actor_context_required")
		return uuid.Nil, "", false
	}
	identity, err := h.proof.Verify(proofBytes, tenantText, commandID, time.Now().UTC())
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "actor_context_invalid")
		return uuid.Nil, "", false
	}
	if !contains(identity.Capabilities, capability) {
		writeProblem(w, http.StatusForbidden, "capability_required")
		return uuid.Nil, "", false
	}
	return tenant, identity.ActorID, true
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
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
