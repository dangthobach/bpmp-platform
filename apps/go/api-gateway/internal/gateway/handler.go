package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type Handler struct {
	engine             ports.Engine
	human              ports.HumanRuntime
	verifier           *verifier
	workload           *workloadSigner
	limiter            ports.RateLimiter
	keyScopes          map[string]string
	maxBody            int64
	now                func() time.Time
	configurationProxy *configurationProxy
}

func New(
	engine ports.Engine,
	human ports.HumanRuntime,
	limiter ports.RateLimiter,
	configurationClient httpDoer,
	value config.Config,
) (*Handler, error) {
	identity, err := newVerifier(value.Identity)
	if err != nil {
		return nil, err
	}
	workload, err := newWorkloadSigner(value.Workload)
	if err != nil {
		return nil, err
	}
	handler, err := NewHandler(
		engine,
		human,
		identity,
		workload,
		limiter,
		value.TenantKeyScopes,
		value.HTTP.MaxBodyBytes,
	)
	if err != nil {
		return nil, err
	}
	handler.configurationProxy, err = newConfigurationProxy(
		configurationClient,
		value.ConfigurationURL,
		value.HTTP.MaxBodyBytes,
		value.HTTP.MaxUpstreamResponseBytes,
	)
	if err != nil {
		return nil, err
	}
	return handler, nil
}

func NewHandler(engine ports.Engine, human ports.HumanRuntime, verifier *verifier, workload *workloadSigner, limiter ports.RateLimiter, keyScopes map[string]string, maxBody int64) (*Handler, error) {
	if engine == nil || human == nil || verifier == nil || workload == nil || limiter == nil || len(keyScopes) == 0 || maxBody <= 0 {
		return nil, errors.New("gateway dependencies are incomplete")
	}
	scopes := make(map[string]string, len(keyScopes))
	for tenant, scope := range keyScopes {
		if !validID(tenant) || strings.TrimSpace(scope) == "" {
			return nil, errors.New("tenant key scope is invalid")
		}
		scopes[tenant] = scope
	}
	return &Handler{engine: engine, human: human, verifier: verifier, workload: workload, limiter: limiter, keyScopes: scopes, maxBody: maxBody, now: time.Now}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/workflows/{workflowType}/instances", h.startWorkflow)
	mux.HandleFunc("GET /v1/work-items", h.listWorkItems)
	mux.HandleFunc("GET /v1/work-items/{workItemID}", h.getWorkItem)
	mux.HandleFunc("POST /v1/work-items/{workItemID}/complete", h.completeWorkItem)
	mux.HandleFunc("POST /v1/work-items/{workItemID}/delegate", h.delegateWorkItem)
	mux.HandleFunc("GET /v1/cases/{caseID}", h.getCase)
	mux.HandleFunc("GET /v1/audit-records", h.listAuditRecords)
	if h.configurationProxy != nil {
		mux.HandleFunc("GET /v1/configuration/profiles", h.configuration)
		mux.HandleFunc("POST /v1/configuration/profiles", h.configuration)
		mux.HandleFunc("GET /v1/configuration/profiles/{profileID}", h.configuration)
		mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions", h.configuration)
		mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions/{versionID}/publish", h.configuration)
		mux.HandleFunc("POST /v1/configuration/profiles/{profileID}/versions/{versionID}/rollback", h.configuration)
	}
	return mux
}

type requestScope struct {
	tenantID, commandID, idempotencyKey, correlationID, rawToken, actorID string
	traceParent, traceState                                               string
	occurredAt                                                            time.Time
}

func (h *Handler) authenticate(r *http.Request) (requestScope, error) {
	return h.authenticateRequest(r, true)
}

func (h *Handler) authenticateQuery(r *http.Request) (requestScope, error) {
	return h.authenticateRequest(r, false)
}

func (h *Handler) authenticateRequest(r *http.Request, commandRequired bool) (requestScope, error) {
	tenant := r.Header.Get("X-BPMP-Tenant-ID")
	command := r.Header.Get("X-Command-ID")
	idempotency := r.Header.Get("Idempotency-Key")
	correlation := r.Header.Get("X-Correlation-ID")
	traceParent := r.Header.Get("traceparent")
	traceState := r.Header.Get("tracestate")
	if !validID(tenant) ||
		!validID(correlation) ||
		(commandRequired && (!validID(command) || !validID(idempotency))) {
		return requestScope{}, errors.New("request scope headers are invalid")
	}
	if len(traceParent) > 512 || len(traceState) > 512 || strings.ContainsAny(traceParent+traceState, "\r\n") {
		return requestScope{}, errors.New("trace propagation headers are invalid")
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		return requestScope{}, errors.New("bearer token is required")
	}
	raw := strings.TrimPrefix(authorization, "Bearer ")
	now := h.now().UTC()
	actor, err := h.verifier.verify(raw, tenant, now)
	if err != nil {
		return requestScope{}, err
	}
	allowed, err := h.limiter.Allow(r.Context(), tenant+"\x00"+actor.ID)
	if err != nil {
		return requestScope{}, errUpstream
	}
	if !allowed {
		return requestScope{}, errRateLimited
	}
	return requestScope{tenantID: tenant, commandID: command, idempotencyKey: idempotency, correlationID: correlation, rawToken: raw, actorID: actor.ID, traceParent: traceParent, traceState: traceState, occurredAt: now}, nil
}

func actorProof(scope requestScope) *authv1.ActorProof {
	return &authv1.ActorProof{
		Type:        authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT,
		SignedProof: []byte(scope.rawToken),
	}
}

func (h *Handler) listWorkItems(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil {
		writeError(w, errInvalid)
		return
	}
	response, err := h.human.ListWorkItems(
		upstreamContext(r, scope),
		&humanv1.ListWorkItemsRequest{
			TenantId:   scope.tenantID,
			PageSize:   pageSize,
			PageToken:  r.URL.Query().Get("page_token"),
			ActorProof: actorProof(scope),
		},
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getWorkItem(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("workItemID")
	if !validID(id) {
		writeError(w, errInvalid)
		return
	}
	response, err := h.human.GetWorkItem(
		upstreamContext(r, scope),
		&humanv1.GetWorkItemRequest{
			TenantId:   scope.tenantID,
			WorkItemId: id,
			ActorProof: actorProof(scope),
		},
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getCase(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("caseID")
	if !validID(id) {
		writeError(w, errInvalid)
		return
	}
	response, err := h.human.GetCase(
		upstreamContext(r, scope),
		&humanv1.GetCaseRequest{
			TenantId:   scope.tenantID,
			CaseId:     id,
			ActorProof: actorProof(scope),
		},
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listAuditRecords(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil {
		writeError(w, errInvalid)
		return
	}
	workItemID := r.URL.Query().Get("work_item_id")
	caseID := r.URL.Query().Get("case_id")
	if (workItemID != "" && !validID(workItemID)) || (caseID != "" && !validID(caseID)) {
		writeError(w, errInvalid)
		return
	}
	response, err := h.human.ListAuditRecords(
		upstreamContext(r, scope),
		&humanv1.ListAuditRecordsRequest{
			TenantId:   scope.tenantID,
			WorkItemId: workItemID,
			CaseId:     caseID,
			PageSize:   pageSize,
			PageToken:  r.URL.Query().Get("page_token"),
			ActorProof: actorProof(scope),
		},
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func positiveUint32Query(r *http.Request, name string) (uint32, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 {
		return 0, errInvalid
	}
	return uint32(parsed), nil
}

type startRequest struct {
	InstanceID      string `json:"instance_id"`
	WorkflowVersion string `json:"workflow_version"`
	StartNodeID     string `json:"start_node_id"`
}

func (h *Handler) startWorkflow(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body startRequest
	if err = decodeBody(w, r, &body, h.maxBody); err != nil {
		writeError(w, err)
		return
	}
	workflowType := r.PathValue("workflowType")
	if !validID(workflowType) || !validID(body.InstanceID) || !validID(body.WorkflowVersion) || !validID(body.StartNodeID) {
		writeError(w, errInvalid)
		return
	}
	keyScope, ok := h.keyScopes[scope.tenantID]
	if !ok {
		writeError(w, errForbidden)
		return
	}
	workload, err := h.workload.sign(scope.tenantID, scope.commandID, scope.occurredAt)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	envelope := &enginev1.CommandEnvelope{TenantId: scope.tenantID, InstanceId: body.InstanceID, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ActorId: scope.actorID, WorkflowType: workflowType, WorkflowVersion: body.WorkflowVersion, OccurredAtEpochMs: uint64(scope.occurredAt.UnixMilli()), EncryptionKeyScope: keyScope, Command: &enginev1.CommandEnvelope_StartWorkflow{StartWorkflow: &enginev1.StartWorkflow{}}, AuthorizationContext: &authv1.AuthorizationContext{TenantId: scope.tenantID, CommandId: scope.commandID, CorrelationId: scope.correlationID, EvaluatedAtEpochMs: uint64(scope.occurredAt.UnixMilli()), ActorProof: actorProof(scope), WorkloadProof: &authv1.WorkloadProof{SignedProof: workload}, Resource: &authv1.TransitionResource{WorkflowType: workflowType, WorkflowVersion: body.WorkflowVersion, InstanceId: body.InstanceID, ActiveNodeId: body.StartNodeID, Action: "START"}}}
	receipt, err := h.engine.HandleCommand(upstreamContext(r, scope), envelope)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

type completeRequest struct {
	Decision        string `json:"decision"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (h *Handler) completeWorkItem(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("workItemID")
	var body completeRequest
	if err = decodeBody(w, r, &body, h.maxBody); err != nil || !validID(id) || strings.TrimSpace(body.Decision) == "" {
		writeError(w, errInvalid)
		return
	}
	response, err := h.human.CompleteWorkItem(upstreamContext(r, scope), &humanv1.CompleteWorkItemRequest{TenantId: scope.tenantID, WorkItemId: id, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, Decision: body.Decision, ExpectedVersion: body.ExpectedVersion, ActorProof: actorProof(scope)})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

type delegateRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	AssigneeID      string `json:"assignee_id"`
	CandidateGroup  string `json:"candidate_group"`
}

func (h *Handler) delegateWorkItem(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("workItemID")
	var body delegateRequest
	if err = decodeBody(w, r, &body, h.maxBody); err != nil || !validID(id) || (body.AssigneeID == "") == (body.CandidateGroup == "") {
		writeError(w, errInvalid)
		return
	}
	response, err := h.human.DelegateWorkItem(upstreamContext(r, scope), &humanv1.DelegateWorkItemRequest{TenantId: scope.tenantID, WorkItemId: id, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ExpectedVersion: body.ExpectedVersion, AssigneeId: body.AssigneeID, CandidateGroup: body.CandidateGroup, ActorProof: actorProof(scope)})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
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
func validID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}

func upstreamContext(r *http.Request, scope requestScope) context.Context {
	return requestmeta.OutgoingContext(r.Context(), requestmeta.Values{
		CorrelationID: scope.correlationID,
		TenantID:      scope.tenantID,
		CommandID:     scope.commandID,
		TraceParent:   scope.traceParent,
		TraceState:    scope.traceState,
	})
}

var (
	errInvalid     = errors.New("invalid request")
	errForbidden   = errors.New("tenant is not configured")
	errRateLimited = errors.New("rate limit exceeded")
	errUpstream    = errors.New("upstream unavailable")
)

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	message := "invalid request"
	switch {
	case errors.Is(err, errForbidden):
		status = http.StatusForbidden
		message = "forbidden"
	case errors.Is(err, errRateLimited):
		status = http.StatusTooManyRequests
		message = "rate limit exceeded"
	case errors.Is(err, errUpstream):
		status = http.StatusBadGateway
		message = "upstream unavailable"
	}
	response := map[string]string{"error": message}
	if correlationID := w.Header().Get("X-Correlation-ID"); validID(correlationID) {
		response["correlation_id"] = correlationID
	}
	writeJSON(w, status, response)
}

func setCorrelationHeader(w http.ResponseWriter, r *http.Request) {
	if correlationID := r.Header.Get("X-Correlation-ID"); validID(correlationID) {
		w.Header().Set("X-Correlation-ID", correlationID)
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
		http.Error(w, "response encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Warn("write HTTP response", "error", err)
	}
}
