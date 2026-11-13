package gateway

import (
	"context"
	"net/http"

	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type assignmentPolicyHumanRuntime interface {
	ListAssignmentPolicies(context.Context, *humanv1.ListAssignmentPoliciesRequest, ...grpc.CallOption) (*humanv1.ListAssignmentPoliciesResponse, error)
	UpsertAssignmentPolicy(context.Context, *humanv1.UpsertAssignmentPolicyRequest, ...grpc.CallOption) (*humanv1.UpsertAssignmentPolicyResponse, error)
}

func (h *Handler) listAssignmentPolicies(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "human.assignment-policy.read") {
		writeError(w, errForbidden)
		return
	}
	client, ok := h.human.(assignmentPolicyHumanRuntime)
	if !ok {
		writeError(w, errUpstream)
		return
	}
	pageSize, err := positiveUint32Query(r, "page_size")
	if err != nil {
		writeError(w, errInvalid)
		return
	}
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.ListAssignmentPoliciesResponse, error) {
			return client.ListAssignmentPolicies(ctx, &humanv1.ListAssignmentPoliciesRequest{
				TenantId: scope.tenantID, PageSize: pageSize, PageToken: r.URL.Query().Get("page_token"),
				WorkflowType: r.URL.Query().Get("workflow_type"), WorkflowVersion: r.URL.Query().Get("workflow_version"), ActorProof: actorProof(scope),
			})
		})
	if err != nil {
		writeHumanRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) upsertAssignmentPolicy(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !hasCapability(scope.capabilities, "human.assignment-policy.manage") {
		writeError(w, errForbidden)
		return
	}
	client, ok := h.human.(assignmentPolicyHumanRuntime)
	if !ok {
		writeError(w, errUpstream)
		return
	}
	var request humanv1.UpsertAssignmentPolicyRequest
	if err := decodeProtoBody(w, r, &request, scope.runtimePolicy.MaxRequestBodyBytes); err != nil {
		writeError(w, errInvalid)
		return
	}
	request.TenantId = scope.tenantID
	request.CommandId = scope.commandID
	request.IdempotencyKey = scope.idempotencyKey
	request.CorrelationId = scope.correlationID
	request.ActorProof = actorProof(scope)
	response, err := invokeUpstream(h, upstreamContext(r, scope), scope, humanDependency,
		func(ctx context.Context) (*humanv1.UpsertAssignmentPolicyResponse, error) {
			return client.UpsertAssignmentPolicy(ctx, &request)
		})
	if err != nil {
		writeHumanRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeHumanRuntimeError(w http.ResponseWriter, err error) {
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
	case codes.Aborted:
		requestmeta.WriteProblemResponse(w, http.StatusConflict, "version_conflict", "Version conflict", grpcStatus.Message(), false)
	case codes.AlreadyExists:
		requestmeta.WriteProblemResponse(w, http.StatusConflict, "idempotency_conflict", "Idempotency conflict", grpcStatus.Message(), false)
	case codes.NotFound:
		requestmeta.WriteProblemResponse(w, http.StatusNotFound, "not_found", "Not found", grpcStatus.Message(), false)
	default:
		writeError(w, errUpstream)
	}
}
