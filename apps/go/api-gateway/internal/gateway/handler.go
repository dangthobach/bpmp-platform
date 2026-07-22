package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
)

type Handler struct {
	engine    enginev1.EngineCommandServiceClient
	human     humanv1.HumanRuntimeServiceClient
	verifier  *verifier
	workload  *workloadSigner
	limiter   *rateLimiter
	keyScopes map[string]string
	maxBody   int64
	now       func() time.Time
}

func New(engine enginev1.EngineCommandServiceClient, human humanv1.HumanRuntimeServiceClient, value config.Config) (*Handler, error) {
	identity, err := newVerifier(value.Identity)
	if err != nil {
		return nil, err
	}
	workload, err := newWorkloadSigner(value.Workload)
	if err != nil {
		return nil, err
	}
	limiter := newRateLimiter(value.RateLimit.Requests, time.Duration(value.RateLimit.WindowMS)*time.Millisecond, value.RateLimit.MaxSubjects)
	return NewHandler(engine, human, identity, workload, limiter, value.TenantKeyScopes, value.HTTP.MaxBodyBytes)
}

func NewHandler(engine enginev1.EngineCommandServiceClient, human humanv1.HumanRuntimeServiceClient, verifier *verifier, workload *workloadSigner, limiter *rateLimiter, keyScopes map[string]string, maxBody int64) (*Handler, error) {
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
	mux.HandleFunc("POST /v1/work-items/{workItemID}/complete", h.completeWorkItem)
	mux.HandleFunc("POST /v1/work-items/{workItemID}/delegate", h.delegateWorkItem)
	return mux
}

type requestScope struct {
	tenantID, commandID, idempotencyKey, correlationID, rawToken, actorID string
	occurredAt                                                            time.Time
}

func (h *Handler) authenticate(r *http.Request) (requestScope, error) {
	tenant := r.Header.Get("X-BPMP-Tenant-ID")
	command := r.Header.Get("X-Command-ID")
	idempotency := r.Header.Get("Idempotency-Key")
	correlation := r.Header.Get("X-Correlation-ID")
	if !validID(tenant) || !validID(command) || !validID(idempotency) || !validID(correlation) {
		return requestScope{}, errors.New("request scope headers are invalid")
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
	if !h.limiter.allow(tenant+"\x00"+actor.ID, now) {
		return requestScope{}, errRateLimited
	}
	return requestScope{tenantID: tenant, commandID: command, idempotencyKey: idempotency, correlationID: correlation, rawToken: raw, actorID: actor.ID, occurredAt: now}, nil
}

type startRequest struct {
	InstanceID      string `json:"instance_id"`
	WorkflowVersion string `json:"workflow_version"`
	StartNodeID     string `json:"start_node_id"`
}

func (h *Handler) startWorkflow(w http.ResponseWriter, r *http.Request) {
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
	envelope := &enginev1.CommandEnvelope{TenantId: scope.tenantID, InstanceId: body.InstanceID, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ActorId: scope.actorID, WorkflowType: workflowType, WorkflowVersion: body.WorkflowVersion, OccurredAtEpochMs: uint64(scope.occurredAt.UnixMilli()), EncryptionKeyScope: keyScope, Command: &enginev1.CommandEnvelope_StartWorkflow{StartWorkflow: &enginev1.StartWorkflow{}}, AuthorizationContext: &authv1.AuthorizationContext{TenantId: scope.tenantID, CommandId: scope.commandID, CorrelationId: scope.correlationID, EvaluatedAtEpochMs: uint64(scope.occurredAt.UnixMilli()), ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(scope.rawToken)}, WorkloadProof: &authv1.WorkloadProof{SignedProof: workload}, Resource: &authv1.TransitionResource{WorkflowType: workflowType, WorkflowVersion: body.WorkflowVersion, InstanceId: body.InstanceID, ActiveNodeId: body.StartNodeID, Action: "START"}}}
	receipt, err := h.engine.HandleCommand(r.Context(), envelope)
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
	response, err := h.human.CompleteWorkItem(r.Context(), &humanv1.CompleteWorkItemRequest{TenantId: scope.tenantID, WorkItemId: id, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, Decision: body.Decision, ExpectedVersion: body.ExpectedVersion, ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(scope.rawToken)}})
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
	response, err := h.human.DelegateWorkItem(r.Context(), &humanv1.DelegateWorkItemRequest{TenantId: scope.tenantID, WorkItemId: id, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ExpectedVersion: body.ExpectedVersion, AssigneeId: body.AssigneeID, CandidateGroup: body.CandidateGroup, ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(scope.rawToken)}})
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

var (
	errInvalid     = errors.New("invalid request")
	errForbidden   = errors.New("tenant is not configured")
	errRateLimited = errors.New("rate limit exceeded")
	errUpstream    = errors.New("upstream unavailable")
)

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errForbidden):
		status = http.StatusForbidden
	case errors.Is(err, errRateLimited):
		status = http.StatusTooManyRequests
	case errors.Is(err, errUpstream):
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
