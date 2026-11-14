package gateway

import (
	"context"
	"net/http"

	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
)

func (h *Handler) deployProcessModel(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.deployProcessModel") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.DeployProcessModelRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ProcessModelId = scope.tenantID, r.PathValue("processModelID")
	request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.DeployProcessModelResponse, error) {
		return h.authoring.DeployProcessModel(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *Handler) rollbackRuntimeDeployment(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.rollbackRuntimeDeployment") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.RollbackRuntimeDeploymentResponse, error) {
		return h.authoring.RollbackRuntimeDeployment(ctx, &authoringv1.RollbackRuntimeDeploymentRequest{TenantId: scope.tenantID, TargetDeploymentId: r.PathValue("deploymentID"), CommandId: scope.commandID, IdempotencyKey: scope.idempotencyKey, CorrelationId: scope.correlationID, ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *Handler) getRuntimeDeployment(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getRuntimeDeployment") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetRuntimeDeploymentResponse, error) {
		return h.authoring.GetRuntimeDeployment(ctx, &authoringv1.GetRuntimeDeploymentRequest{TenantId: scope.tenantID, DeploymentId: r.PathValue("deploymentID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listRuntimeDeployments(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listRuntimeDeployments") {
		writeError(w, errForbidden)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil || pageSize > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListRuntimeDeploymentsResponse, error) {
		return h.authoring.ListRuntimeDeployments(ctx, &authoringv1.ListRuntimeDeploymentsRequest{TenantId: scope.tenantID, ProcessModelId: r.PathValue("processModelID"), TargetEnvironment: r.URL.Query().Get("environment"), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
