package gateway

import (
	"context"
	"net/http"
	"strconv"

	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
)

func (h *Handler) saveDashboard(w http.ResponseWriter, r *http.Request) {
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
	request := &projectionv1.SaveDashboardRequest{}
	if err = decodeProtoBody(w, r, request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId, request.ActorProof = scope.tenantID, actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.SaveDashboardResponse, error) {
		return h.projection.SaveDashboard(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listDashboards(w http.ResponseWriter, r *http.Request) {
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
	request := &projectionv1.ListDashboardsRequest{TenantId: scope.tenantID, ActorProof: actorProof(scope), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token")}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.ListDashboardsResponse, error) {
		return h.projection.ListDashboards(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) deleteDashboard(w http.ResponseWriter, r *http.Request) {
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
	request := &projectionv1.DeleteDashboardRequest{TenantId: scope.tenantID, Id: r.PathValue("dashboardID"), ExpectedVersion: version, ActorProof: actorProof(scope)}
	_, err = invokeUpstream(h, upstreamContext(r, scope), scope, projectionDependency, func(ctx context.Context) (*projectionv1.DeleteDashboardResponse, error) {
		return h.projection.DeleteDashboard(ctx, request)
	})
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
