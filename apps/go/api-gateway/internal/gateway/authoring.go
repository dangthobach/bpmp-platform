package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func WithAuthoring(client ports.Authoring) HandlerOption {
	return func(handler *Handler) error {
		if client == nil {
			return errors.New("Authoring client is required")
		}
		handler.authoring = client
		return nil
	}
}

func (h *Handler) listProcessModels(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listProcessModel") {
		writeError(w, errForbidden)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil || pageSize > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListProcessModelsResponse, error) {
		return h.authoring.ListProcessModels(ctx, &authoringv1.ListProcessModelsRequest{TenantId: scope.tenantID, ActorProof: actorProof(scope), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token"), Search: r.URL.Query().Get("search")})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) createProcessModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.createProcessModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.CreateProcessModelRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.tenantID, scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.CreateProcessModelResponse, error) {
		return h.authoring.CreateProcessModel(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) getProcessModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getProcessModel") {
		writeError(w, errForbidden)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getProcessModelDraft") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetProcessModelResponse, error) {
		return h.authoring.GetProcessModel(ctx, &authoringv1.GetProcessModelRequest{TenantId: scope.tenantID, ProcessModelId: r.PathValue("processModelID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) saveProcessModelDraft(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.saveProcessModelDraft") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.SaveProcessModelDraftRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ProcessModelId = scope.tenantID, r.PathValue("processModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.SaveProcessModelDraftResponse, error) {
		return h.authoring.SaveProcessModelDraft(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) validateProcessModelDraft(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.validateProcessModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.ValidateProcessModelDraftRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ProcessModelId = scope.tenantID, r.PathValue("processModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ValidateProcessModelDraftResponse, error) {
		return h.authoring.ValidateProcessModelDraft(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) checkinProcessModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.checkinProcessModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.CheckinProcessModelRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ProcessModelId = scope.tenantID, r.PathValue("processModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.CheckinProcessModelResponse, error) {
		return h.authoring.CheckinProcessModel(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) listProcessModelVersions(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listProcessModelVersions") {
		writeError(w, errForbidden)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil || pageSize > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListProcessModelVersionsResponse, error) {
		return h.authoring.ListProcessModelVersions(ctx, &authoringv1.ListProcessModelVersionsRequest{TenantId: scope.tenantID, ProcessModelId: r.PathValue("processModelID"), ActorProof: actorProof(scope), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token")})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getProcessModelVersion(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getProcessModelVersion") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetProcessModelVersionResponse, error) {
		return h.authoring.GetProcessModelVersion(ctx, &authoringv1.GetProcessModelVersionRequest{TenantId: scope.tenantID, ProcessModelId: r.PathValue("processModelID"), VersionId: r.PathValue("versionID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) checkinDecisionModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.checkinDecisionModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.CheckinDecisionModelRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.DecisionModelId = scope.tenantID, r.PathValue("decisionModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.CheckinDecisionModelResponse, error) {
		return h.authoring.CheckinDecisionModel(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) listDecisionModelVersions(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listDecisionModelVersions") {
		writeError(w, errForbidden)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil || pageSize > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListDecisionModelVersionsResponse, error) {
		return h.authoring.ListDecisionModelVersions(ctx, &authoringv1.ListDecisionModelVersionsRequest{TenantId: scope.tenantID, DecisionModelId: r.PathValue("decisionModelID"), ActorProof: actorProof(scope), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token")})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getDecisionModelVersion(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getDecisionModelVersion") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetDecisionModelVersionResponse, error) {
		return h.authoring.GetDecisionModelVersion(ctx, &authoringv1.GetDecisionModelVersionRequest{TenantId: scope.tenantID, DecisionModelId: r.PathValue("decisionModelID"), VersionId: r.PathValue("versionID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listDecisionModels(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listDecisionModel") {
		writeError(w, errForbidden)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil || pageSize > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListDecisionModelsResponse, error) {
		return h.authoring.ListDecisionModels(ctx, &authoringv1.ListDecisionModelsRequest{TenantId: scope.tenantID, ActorProof: actorProof(scope), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token"), Search: r.URL.Query().Get("search")})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) createDecisionModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.createDecisionModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.CreateDecisionModelRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.tenantID, scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.CreateDecisionModelResponse, error) {
		return h.authoring.CreateDecisionModel(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) getDecisionModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getDecisionModel") || !hasCapability(scope.capabilities, "authoring.getDecisionModelDraft") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetDecisionModelResponse, error) {
		return h.authoring.GetDecisionModel(ctx, &authoringv1.GetDecisionModelRequest{TenantId: scope.tenantID, DecisionModelId: r.PathValue("decisionModelID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) saveDecisionModelDraft(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.saveDecisionModelDraft") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.SaveDecisionModelDraftRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.DecisionModelId = scope.tenantID, r.PathValue("decisionModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.SaveDecisionModelDraftResponse, error) {
		return h.authoring.SaveDecisionModelDraft(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) validateDecisionModelDraft(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.validateDecisionModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.ValidateDecisionModelDraftRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.DecisionModelId = scope.tenantID, r.PathValue("decisionModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ValidateDecisionModelDraftResponse, error) {
		return h.authoring.ValidateDecisionModelDraft(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeAuthoringError(w http.ResponseWriter, err error) {
	grpcStatus, ok := status.FromError(err)
	if !ok {
		writeError(w, errUpstream)
		return
	}
	switch grpcStatus.Code() {
	case codes.PermissionDenied, codes.Unauthenticated:
		writeError(w, errForbidden)
	case codes.InvalidArgument:
		writeError(w, errInvalid)
	case codes.FailedPrecondition:
		requestmeta.WriteProblemResponse(w, http.StatusUnprocessableEntity, "draft_not_validated", "Draft is not valid", grpcStatus.Message(), false)
	case codes.Aborted, codes.AlreadyExists:
		requestmeta.WriteProblemResponse(w, http.StatusConflict, "revision_conflict", "Revision conflict", grpcStatus.Message(), false)
	case codes.NotFound:
		requestmeta.WriteProblemResponse(w, http.StatusNotFound, "not_found", "Not found", "authoring model not found", false)
	default:
		writeError(w, errUpstream)
	}
}
