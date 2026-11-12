package gateway

import (
	"context"
	"net/http"

	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
)

func (h *Handler) listAccessQueues(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "authz.catalog.read") {
		writeError(w, errForbidden)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.ListAssignmentGroupsResponse, error) {
		return h.human.ListAssignmentGroups(ctx, &humanv1.ListAssignmentGroupsRequest{
			TenantId: scope.tenantID, ActorProof: actorProof(scope),
		})
	})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
