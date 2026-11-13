package gateway

import (
	"context"
	"net/http"

	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
)

func (h *Handler) listForms(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listForm") {
		writeError(w, errForbidden)
		return
	}
	size, err := positiveUint32Query(r, "page_size")
	if err != nil || size > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListFormsResponse, error) {
		return h.authoring.ListForms(ctx, &authoringv1.ListFormsRequest{TenantId: scope.tenantID, ActorProof: actorProof(scope), PageSize: size, PageToken: r.URL.Query().Get("page_token"), Search: r.URL.Query().Get("search")})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) createForm(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.createForm") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.CreateFormRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.tenantID, scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.CreateFormResponse, error) {
		return h.authoring.CreateForm(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) getPublishedFormByKey(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getPublishedForm") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetFormByKeyResponse, error) {
		return h.authoring.GetFormByKey(ctx, &authoringv1.GetFormByKeyRequest{TenantId: scope.tenantID, FormKey: r.PathValue("formKey"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getForm(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getForm") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetFormResponse, error) {
		return h.authoring.GetForm(ctx, &authoringv1.GetFormRequest{TenantId: scope.tenantID, FormId: r.PathValue("formID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) saveFormDraft(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.saveFormDraft") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.SaveFormDraftRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.FormId, request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.tenantID, r.PathValue("formID"), scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.SaveFormDraftResponse, error) {
		return h.authoring.SaveFormDraft(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) validateFormDraft(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.validateFormDraft") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.ValidateFormDraftRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.FormId, request.ActorProof = scope.tenantID, r.PathValue("formID"), actorProof(scope)
	request.CommandId, request.IdempotencyKey, request.CorrelationId = scope.commandID, scope.idempotencyKey, scope.correlationID
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ValidateFormDraftResponse, error) {
		return h.authoring.ValidateFormDraft(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) publishForm(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.publishForm") {
		writeError(w, errForbidden)
		return
	}
	var request authoringv1.PublishFormRequest
	if err = decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.FormId, request.CommandId, request.IdempotencyKey, request.CorrelationId, request.ActorProof = scope.tenantID, r.PathValue("formID"), scope.commandID, scope.idempotencyKey, scope.correlationID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.PublishFormResponse, error) {
		return h.authoring.PublishForm(ctx, &request)
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) listFormVersions(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.listFormVersions") {
		writeError(w, errForbidden)
		return
	}
	size, err := positiveUint32Query(r, "page_size")
	if err != nil || size > 100 {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.ListFormVersionsResponse, error) {
		return h.authoring.ListFormVersions(ctx, &authoringv1.ListFormVersionsRequest{TenantId: scope.tenantID, FormId: r.PathValue("formID"), ActorProof: actorProof(scope), PageSize: size, PageToken: r.URL.Query().Get("page_token")})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getFormVersion(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authoring.getFormVersion") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, authoringDependency, func(ctx context.Context) (*authoringv1.GetFormVersionResponse, error) {
		return h.authoring.GetFormVersion(ctx, &authoringv1.GetFormVersionRequest{TenantId: scope.tenantID, FormId: r.PathValue("formID"), VersionId: r.PathValue("versionID"), ActorProof: actorProof(scope)})
	})
	if err != nil {
		writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
