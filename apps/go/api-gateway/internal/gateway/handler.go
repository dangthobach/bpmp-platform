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

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Handler struct {
	engine             ports.Engine
	human              ports.HumanRuntime
	projection         ports.Projection
	verifier           *verifier
	workload           *workloadSigner
	limiter            ports.RateLimiter
	now                func() time.Time
	configurationProxy *configurationProxy
	policyProvider     PolicyProvider
	upstreams          *upstreamController
	sessionStore       ports.BrowserSessionStore
	sessionConfig      config.BrowserSession
}

type RuntimePolicy struct {
	ConfigVersion                  string
	PolicyVersion                  string
	ContentETag                    string
	RateLimitRequests              uint32
	RateLimitWindow                time.Duration
	UpstreamTimeout                time.Duration
	CircuitBreakerFailureThreshold uint32
	CircuitBreakerOpen             time.Duration
	BulkheadMaxConcurrency         uint32
	MaxRequestBodyBytes            int64
	MaxUpstreamResponseBytes       int64
	BatchChunkSize                 uint32
	BatchConcurrency               uint32
	UpstreamRetry                  RetryPolicy
	EncryptionKeyScope             string
}

type RetryPolicy struct {
	MaxAttempts      uint32
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	MultiplierMillis uint32
}

type PolicyProvider interface {
	Policy(string) (RuntimePolicy, error)
}

type instancePolicyProvider interface {
	PolicyForInstance(string, string) (RuntimePolicy, error)
}

type dynamicRateLimiter interface {
	AllowPolicy(context.Context, string, uint32, time.Duration) (bool, error)
}

func NewWithPolicy(
	engine ports.Engine,
	human ports.HumanRuntime,
	projection ports.Projection,
	limiter ports.RateLimiter,
	configurationClient httpDoer,
	value config.Config,
	policyProvider PolicyProvider,
	options ...HandlerOption,
) (*Handler, error) {
	if projection == nil {
		return nil, errors.New("projection client is required")
	}
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
		policyProvider,
		options...,
	)
	if err != nil {
		return nil, err
	}
	handler.projection = projection
	handler.configurationProxy, err = newConfigurationProxy(
		configurationClient,
		value.ConfigurationURL,
	)
	if err != nil {
		return nil, err
	}
	return handler, nil
}

func NewHandler(
	engine ports.Engine,
	human ports.HumanRuntime,
	verifier *verifier,
	workload *workloadSigner,
	limiter ports.RateLimiter,
	policyProvider PolicyProvider,
	options ...HandlerOption,
) (*Handler, error) {
	if engine == nil || human == nil || verifier == nil || workload == nil ||
		limiter == nil || policyProvider == nil {
		return nil, errors.New("gateway dependencies are incomplete")
	}
	handler := &Handler{
		engine: engine, human: human, verifier: verifier, workload: workload,
		limiter: limiter, policyProvider: policyProvider,
		upstreams: newUpstreamController(), now: time.Now,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("gateway option is nil")
		}
		if err := option(handler); err != nil {
			return nil, err
		}
	}
	return handler, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	for _, definition := range publicOperationDefinitions {
		if definition.configuration && h.configurationProxy == nil {
			continue
		}
		definition := definition
		mux.HandleFunc(
			definition.Method+" "+definition.Path,
			func(w http.ResponseWriter, r *http.Request) {
				definition.handler(h, w, r)
			},
		)
	}
	return mux
}

type PublicOperation struct {
	Method      string
	Path        string
	OperationID string
}

type publicOperationDefinition struct {
	PublicOperation
	handler       func(*Handler, http.ResponseWriter, *http.Request)
	configuration bool
}

var publicOperationDefinitions = []publicOperationDefinition{
	{PublicOperation{http.MethodGet, "/v1/runtime/browser-configuration", "getBrowserConfiguration"}, (*Handler).browserConfiguration, false},
	{PublicOperation{http.MethodPost, "/v1/workflows/{workflowType}/instances", "startWorkflow"}, (*Handler).startWorkflow, false},
	{PublicOperation{http.MethodGet, "/v1/work-items", "listWorkItems"}, (*Handler).listWorkItems, false},
	{PublicOperation{http.MethodGet, "/v1/work-items/{workItemID}", "getWorkItem"}, (*Handler).getWorkItem, false},
	{PublicOperation{http.MethodPost, "/v1/work-items/{workItemID}/complete", "completeWorkItem"}, (*Handler).completeWorkItem, false},
	{PublicOperation{http.MethodPost, "/v1/work-items/{workItemID}/delegate", "delegateWorkItem"}, (*Handler).delegateWorkItem, false},
	{PublicOperation{http.MethodPost, "/v1/work-items/{workItemID}/claim", "claimWorkItem"}, (*Handler).claimWorkItem, false},
	{PublicOperation{http.MethodPost, "/v1/work-items/{workItemID}/unclaim", "unclaimWorkItem"}, (*Handler).unclaimWorkItem, false},
	{PublicOperation{http.MethodGet, "/v1/cases/{caseID}", "getCase"}, (*Handler).getCase, false},
	{PublicOperation{http.MethodGet, "/v1/audit-records", "listAuditRecords"}, (*Handler).listAuditRecords, false},
	{PublicOperation{http.MethodPost, "/v1/process-instances/completed/query", "queryCompletedProcesses"}, (*Handler).queryCompletedProcesses, false},
	{PublicOperation{http.MethodPost, "/v1/process-instances/query", "queryProcesses"}, (*Handler).queryProcesses, false},
	{PublicOperation{http.MethodGet, "/v1/process-instances/query-capabilities", "getProcessQueryCapabilities"}, (*Handler).getProcessQueryCapabilities, false},
	{PublicOperation{http.MethodPost, "/v1/process-instances/aggregate", "aggregateProcesses"}, (*Handler).aggregateProcesses, false},
	{PublicOperation{http.MethodPost, "/v1/process-events/query", "queryProcessEvents"}, (*Handler).queryProcessEvents, false},
	{PublicOperation{http.MethodPost, "/v1/saved-queries", "saveProcessQuery"}, (*Handler).saveProcessQuery, false},
	{PublicOperation{http.MethodGet, "/v1/saved-queries", "listSavedQueries"}, (*Handler).listSavedQueries, false},
	{PublicOperation{http.MethodDelete, "/v1/saved-queries/{savedQueryID}", "deleteSavedQuery"}, (*Handler).deleteSavedQuery, false},
	{PublicOperation{http.MethodPost, "/v1/query-exports", "createQueryExport"}, (*Handler).createQueryExport, false},
	{PublicOperation{http.MethodGet, "/v1/query-exports/{exportID}", "getQueryExport"}, (*Handler).getQueryExport, false},
	{PublicOperation{http.MethodDelete, "/v1/query-exports/{exportID}", "cancelQueryExport"}, (*Handler).cancelQueryExport, false},
	{PublicOperation{http.MethodPost, "/v1/ops/query-plan", "explainQueryPlan"}, (*Handler).explainQueryPlan, false},
	{PublicOperation{http.MethodGet, "/v1/configuration/profiles", "listConfigurationProfiles"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodPost, "/v1/configuration/profiles", "createConfigurationProfile"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodGet, "/v1/configuration/profiles/{profileID}", "getConfigurationProfile"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodPost, "/v1/configuration/profiles/{profileID}/versions", "addConfigurationDraft"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodPost, "/v1/configuration/profiles/{profileID}/versions/{versionID}/publish", "publishConfigurationVersion"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodPost, "/v1/configuration/profiles/{profileID}/versions/{versionID}/rollback", "rollbackConfigurationVersion"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodPost, "/v1/configuration/profiles/{profileID}/versions/{versionID}/restore", "restoreConfigurationVersion"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodPost, "/v1/configuration/profiles/{profileID}/retire", "retireConfigurationProfile"}, (*Handler).configuration, true},
	{PublicOperation{http.MethodGet, "/v1/configuration/profiles/{profileID}/diff", "diffConfigurationVersions"}, (*Handler).configuration, true},
}

func (h *Handler) queryCompletedProcesses(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.QueryCompletedProcessesRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId = scope.tenantID
	request.ActorProof = actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency,
		func(ctx context.Context) (*projectionv1.QueryCompletedProcessesResponse, error) {
			return h.projection.QueryCompletedProcesses(ctx, request)
		})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) queryProcesses(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.QueryProcessesRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId = scope.tenantID
	request.ActorProof = actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency,
		func(ctx context.Context) (*projectionv1.QueryProcessesResponse, error) {
			return h.projection.QueryProcesses(ctx, request)
		})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getProcessQueryCapabilities(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency,
		func(ctx context.Context) (*projectionv1.GetQueryCapabilitiesResponse, error) {
			return h.projection.GetQueryCapabilities(ctx, &projectionv1.GetQueryCapabilitiesRequest{
				TenantId: scope.tenantID, ActorProof: actorProof(scope),
			})
		})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) aggregateProcesses(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.AggregateProcessesRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ActorProof = scope.tenantID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency,
		func(ctx context.Context) (*projectionv1.AggregateProcessesResponse, error) {
			return h.projection.AggregateProcesses(ctx, request)
		})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) queryProcessEvents(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.QueryEventsRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ActorProof = scope.tenantID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.QueryEventsResponse, error) {
		return h.projection.QueryEvents(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) saveProcessQuery(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.SaveQueryRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ActorProof = scope.tenantID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.SaveQueryResponse, error) {
		return h.projection.SaveQuery(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listSavedQueries(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil {
		writeError(w, errInvalid)
		return
	}
	request := &projectionv1.ListSavedQueriesRequest{TenantId: scope.tenantID, ActorProof: actorProof(scope), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token")}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.ListSavedQueriesResponse, error) {
		return h.projection.ListSavedQueries(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) deleteSavedQuery(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	version, err := strconv.ParseUint(r.URL.Query().Get("expected_version"), 10, 64)
	if err != nil || version == 0 {
		writeError(w, errInvalid)
		return
	}
	request := &projectionv1.DeleteSavedQueryRequest{TenantId: scope.tenantID, Id: r.PathValue("savedQueryID"), ExpectedVersion: version, ActorProof: actorProof(scope)}
	_, err = invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.DeleteSavedQueryResponse, error) {
		return h.projection.DeleteSavedQuery(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createQueryExport(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.CreateQueryExportRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ActorProof = scope.tenantID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.CreateQueryExportResponse, error) {
		return h.projection.CreateQueryExport(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *Handler) getQueryExport(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.GetQueryExportResponse, error) {
		return h.projection.GetQueryExport(ctx, &projectionv1.GetQueryExportRequest{TenantId: scope.tenantID, Id: r.PathValue("exportID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) cancelQueryExport(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.CancelQueryExportResponse, error) {
		return h.projection.CancelQueryExport(ctx, &projectionv1.CancelQueryExportRequest{TenantId: scope.tenantID, Id: r.PathValue("exportID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) explainQueryPlan(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.projection == nil {
		writeError(w, errUpstream)
		return
	}
	request := &projectionv1.ExplainQueryPlanRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ActorProof = scope.tenantID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.ExplainQueryPlanResponse, error) {
		return h.projection.ExplainQueryPlan(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

type browserConfigurationResponse struct {
	ConfigVersion    string `json:"config_version"`
	PolicyVersion    string `json:"policy_version"`
	BatchChunkSize   uint32 `json:"batch_chunk_size"`
	BatchConcurrency uint32 `json:"batch_concurrency"`
}

func (h *Handler) browserConfiguration(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	etag := `"` + scope.runtimePolicy.ContentETag + `"`
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, browserConfigurationResponse{
		ConfigVersion:    scope.runtimePolicy.ConfigVersion,
		PolicyVersion:    scope.runtimePolicy.PolicyVersion,
		BatchChunkSize:   scope.runtimePolicy.BatchChunkSize,
		BatchConcurrency: scope.runtimePolicy.BatchConcurrency,
	})
}

func PublicOperations() []PublicOperation {
	operations := make([]PublicOperation, 0, len(publicOperationDefinitions))
	for _, definition := range publicOperationDefinitions {
		operations = append(operations, definition.PublicOperation)
	}
	return operations
}

type requestScope struct {
	tenantID, commandID, idempotencyKey, correlationID, actorID string
	traceParent, traceState                                     string
	actorProofType                                              authv1.ActorProofType
	signedActorProof                                            []byte
	occurredAt                                                  time.Time
	runtimePolicy                                               RuntimePolicy
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
	if (tenant != "" && !validID(tenant)) || !validID(correlation) ||
		(commandRequired && (!validID(command) || !validID(idempotency))) {
		return requestScope{}, errors.New("request scope headers are invalid")
	}
	if len(traceParent) > 512 || len(traceState) > 512 || strings.ContainsAny(traceParent+traceState, "\r\n") {
		return requestScope{}, errors.New("trace propagation headers are invalid")
	}
	now := h.now().UTC()
	identity, err := h.authenticateActor(r, tenant, now)
	if err != nil {
		return requestScope{}, err
	}
	tenant = identity.tenantID
	policy, err := h.policyProvider.Policy(tenant)
	if err != nil {
		return requestScope{}, errUpstream
	}
	subject := tenant + "\x00" + identity.actorID
	allowed := false
	if limiter, dynamic := h.limiter.(dynamicRateLimiter); dynamic {
		allowed, err = limiter.AllowPolicy(
			r.Context(),
			subject,
			policy.RateLimitRequests,
			policy.RateLimitWindow,
		)
	} else {
		allowed, err = h.limiter.Allow(r.Context(), subject)
	}
	if err != nil {
		return requestScope{}, errUpstream
	}
	if !allowed {
		return requestScope{}, errRateLimited
	}
	requestmeta.Enrich(r.Context(), requestmeta.Values{
		TenantID: tenant, CommandID: command, ActorID: identity.actorID,
		PolicyVersion: policy.PolicyVersion,
	})
	return requestScope{
		tenantID: tenant, commandID: command, idempotencyKey: idempotency,
		correlationID: correlation, actorID: identity.actorID,
		actorProofType:   identity.proofType,
		signedActorProof: append([]byte(nil), identity.signedProof...),
		traceParent:      traceParent, traceState: traceState,
		occurredAt: now, runtimePolicy: policy,
	}, nil
}

func actorProof(scope requestScope) *authv1.ActorProof {
	return &authv1.ActorProof{
		Type:        scope.actorProofType,
		SignedProof: append([]byte(nil), scope.signedActorProof...),
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
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.ListWorkItemsResponse, error) {
			return h.human.ListWorkItems(ctx, &humanv1.ListWorkItemsRequest{
				TenantId:   scope.tenantID,
				PageSize:   pageSize,
				PageToken:  r.URL.Query().Get("page_token"),
				ActorProof: actorProof(scope),
			})
		})
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
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.GetWorkItemResponse, error) {
			return h.human.GetWorkItem(ctx, &humanv1.GetWorkItemRequest{
				TenantId:   scope.tenantID,
				WorkItemId: id,
				ActorProof: actorProof(scope),
			})
		})
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
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.GetCaseResponse, error) {
			return h.human.GetCase(ctx, &humanv1.GetCaseRequest{
				TenantId:   scope.tenantID,
				CaseId:     id,
				ActorProof: actorProof(scope),
			})
		})
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
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.ListAuditRecordsResponse, error) {
			return h.human.ListAuditRecords(ctx, &humanv1.ListAuditRecordsRequest{
				TenantId:   scope.tenantID,
				WorkItemId: workItemID,
				CaseId:     caseID,
				PageSize:   pageSize,
				PageToken:  r.URL.Query().Get("page_token"),
				ActorProof: actorProof(scope),
			})
		})
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
	if err = decodeBody(w, r, &body, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, err)
		return
	}
	workflowType := r.PathValue("workflowType")
	if !validID(workflowType) || !validID(body.InstanceID) || !validID(body.WorkflowVersion) || !validID(body.StartNodeID) {
		writeError(w, errInvalid)
		return
	}
	if provider, ok := h.policyProvider.(instancePolicyProvider); ok {
		scope.runtimePolicy, err = provider.PolicyForInstance(scope.tenantID, body.InstanceID)
		if err != nil {
			writeError(w, errUpstream)
			return
		}
	}
	workload, err := h.workload.sign(scope.tenantID, scope.commandID, scope.occurredAt)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	envelope := &enginev1.CommandEnvelope{TenantId: scope.tenantID, InstanceId: body.InstanceID, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ActorId: scope.actorID, WorkflowType: workflowType, WorkflowVersion: body.WorkflowVersion, OccurredAtEpochMs: uint64(scope.occurredAt.UnixMilli()), EncryptionKeyScope: scope.runtimePolicy.EncryptionKeyScope, Command: &enginev1.CommandEnvelope_StartWorkflow{StartWorkflow: &enginev1.StartWorkflow{}}, AuthorizationContext: &authv1.AuthorizationContext{TenantId: scope.tenantID, CommandId: scope.commandID, CorrelationId: scope.correlationID, EvaluatedAtEpochMs: uint64(scope.occurredAt.UnixMilli()), ActorProof: actorProof(scope), WorkloadProof: &authv1.WorkloadProof{SignedProof: workload}, Resource: &authv1.TransitionResource{WorkflowType: workflowType, WorkflowVersion: body.WorkflowVersion, InstanceId: body.InstanceID, ActiveNodeId: body.StartNodeID, Action: "START"}}}
	receipt, err := invokeUpstream(h, upstreamContext(r, scope), scope, engineDependency,
		func(ctx context.Context) (*enginev1.CommandReceipt, error) {
			return h.engine.HandleCommand(ctx, envelope)
		})
	if err != nil {
		slog.Error(
			"engine command failed",
			"error", err,
			"tenant_id", scope.tenantID,
			"correlation_id", scope.correlationID,
			"command_id", scope.commandID,
			"workflow_type", workflowType,
		)
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
	if err = decodeBody(w, r, &body, scope.runtimePolicy.MaxRequestBodyBytes); err != nil || !validID(id) || strings.TrimSpace(body.Decision) == "" {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.CompleteWorkItemResponse, error) {
			return h.human.CompleteWorkItem(ctx, &humanv1.CompleteWorkItemRequest{TenantId: scope.tenantID, WorkItemId: id, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, Decision: body.Decision, ExpectedVersion: body.ExpectedVersion, ActorProof: actorProof(scope)})
		})
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
	if err = decodeBody(w, r, &body, scope.runtimePolicy.MaxRequestBodyBytes); err != nil || !validID(id) || (body.AssigneeID == "") == (body.CandidateGroup == "") {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.DelegateWorkItemResponse, error) {
			return h.human.DelegateWorkItem(ctx, &humanv1.DelegateWorkItemRequest{TenantId: scope.tenantID, WorkItemId: id, CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ExpectedVersion: body.ExpectedVersion, AssigneeId: body.AssigneeID, CandidateGroup: body.CandidateGroup, ActorProof: actorProof(scope)})
		})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type claimRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (h *Handler) claimWorkItem(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("workItemID")
	var body claimRequest
	if err = decodeBody(w, r, &body, scope.runtimePolicy.MaxRequestBodyBytes); err != nil || !validID(id) || scope.actorID == "" {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.DelegateWorkItemResponse, error) {
			return h.human.DelegateWorkItem(ctx, &humanv1.DelegateWorkItemRequest{
				TenantId: scope.tenantID, WorkItemId: id,
				CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey,
				CorrelationId: scope.correlationID, ExpectedVersion: body.ExpectedVersion,
				AssigneeId: scope.actorID, CandidateGroup: "",
				ActorProof: actorProof(scope),
			})
		})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type unclaimRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	CandidateGroup  string `json:"candidate_group"`
}

func (h *Handler) unclaimWorkItem(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("workItemID")
	var body unclaimRequest
	if err = decodeBody(w, r, &body, scope.runtimePolicy.MaxRequestBodyBytes); err != nil || !validID(id) || strings.TrimSpace(body.CandidateGroup) == "" {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.DelegateWorkItemResponse, error) {
			return h.human.DelegateWorkItem(ctx, &humanv1.DelegateWorkItemRequest{
				TenantId: scope.tenantID, WorkItemId: id,
				CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey,
				CorrelationId: scope.correlationID, ExpectedVersion: body.ExpectedVersion,
				AssigneeId: "", CandidateGroup: strings.TrimSpace(body.CandidateGroup),
				ActorProof: actorProof(scope),
			})
		})
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

func decodeProtoBody(w http.ResponseWriter, r *http.Request, target proto.Message, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, target)
}
func validID(value string) bool {
	return requestmeta.ValidID(value)
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
	errInvalid      = errors.New("invalid request")
	errUnauthorized = errors.New("unauthorized")
	errForbidden    = errors.New("tenant is not configured")
	errRateLimited  = errors.New("rate limit exceeded")
	errUpstream     = errors.New("upstream unavailable")
)

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "invalid_request"
	message := "Invalid request"
	retryable := false
	switch {
	case errors.Is(err, errUnauthorized):
		status = http.StatusUnauthorized
		code, message = "unauthorized", "Unauthorized"
		w.Header().Set("WWW-Authenticate", "Bearer")
	case errors.Is(err, errForbidden):
		status = http.StatusForbidden
		code, message = "forbidden", "Forbidden"
	case errors.Is(err, errRateLimited):
		status = http.StatusTooManyRequests
		code, message, retryable = "rate_limit_exceeded", "Rate limit exceeded", true
	case errors.Is(err, errUpstream):
		status = http.StatusBadGateway
		code, message, retryable = "upstream_unavailable", "Upstream unavailable", true
	}
	requestmeta.WriteProblemResponse(w, status, code, message, "", retryable)
}

type queryViolationResponse struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type queryProblemResponse struct {
	requestmeta.Problem
	Violations []queryViolationResponse `json:"violations,omitempty"`
}

func writeProjectionError(w http.ResponseWriter, err error) {
	grpcStatus, ok := status.FromError(err)
	if !ok {
		writeError(w, errUpstream)
		return
	}
	if grpcStatus.Code() == codes.PermissionDenied || grpcStatus.Code() == codes.Unauthenticated {
		writeError(w, errForbidden)
		return
	}
	if grpcStatus.Code() == codes.NotFound {
		requestmeta.WriteProblemResponse(w, http.StatusNotFound, "not_found", "Not found", grpcStatus.Message(), false)
		return
	}
	if grpcStatus.Code() == codes.Aborted {
		requestmeta.WriteProblemResponse(w, http.StatusConflict, "version_conflict", "Version conflict", grpcStatus.Message(), false)
		return
	}
	if grpcStatus.Code() == codes.DeadlineExceeded {
		requestmeta.WriteProblemResponse(w, http.StatusGatewayTimeout, "query_timeout", "Query timed out", grpcStatus.Message(), true)
		return
	}
	if grpcStatus.Code() != codes.InvalidArgument {
		writeError(w, errUpstream)
		return
	}
	violations := make([]queryViolationResponse, 0)
	for _, detail := range grpcStatus.Details() {
		badRequest, valid := detail.(*errdetails.BadRequest)
		if !valid {
			continue
		}
		for _, violation := range badRequest.GetFieldViolations() {
			rule, message := "invalid", violation.GetDescription()
			if before, after, found := strings.Cut(message, ": "); found {
				rule, message = before, after
			}
			violations = append(violations, queryViolationResponse{Path: violation.GetField(), Rule: rule, Message: message})
		}
	}
	problem := queryProblemResponse{Problem: requestmeta.Problem{
		Type: "https://docs.bpmp.dev/problems/query_validation_failed", Title: "Query validation failed",
		Status: http.StatusBadRequest, Detail: grpcStatus.Message(), Code: "query_validation_failed",
		RequestID: w.Header().Get(requestmeta.HTTPHeaderRequestID), CorrelationID: w.Header().Get(requestmeta.HTTPHeaderCorrelationID),
	}, Violations: violations}
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, http.StatusBadRequest, problem)
}

func setCorrelationHeader(w http.ResponseWriter, r *http.Request) {
	if values, ok := requestmeta.FromContext(r.Context()); ok {
		w.Header().Set(requestmeta.HTTPHeaderCorrelationID, values.CorrelationID)
		return
	}
	if correlationID := r.Header.Get(requestmeta.HTTPHeaderCorrelationID); requestmeta.ValidID(correlationID) {
		w.Header().Set(requestmeta.HTTPHeaderCorrelationID, correlationID)
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
		requestmeta.WriteProblemResponse(w, http.StatusInternalServerError, "response_encoding_failed", "Response encoding failed", "response could not be encoded", false)
		return
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Warn("write HTTP response", "error", err)
	}
}
