package grpcapi

import (
	"context"
	"errors"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	projectionv1.UnimplementedProjectionQueryServiceServer
	service *application.Service
	cursors *CursorCodec
}

func New(service *application.Service, cursors *CursorCodec) (*Server, error) {
	if service == nil || cursors == nil {
		return nil, errors.New("projection application service and cursor codec are required")
	}
	return &Server{service: service, cursors: cursors}, nil
}

func (s *Server) GetWorkflowInstance(
	ctx context.Context,
	request *projectionv1.GetWorkflowInstanceRequest,
) (*projectionv1.GetWorkflowInstanceResponse, error) {
	if request.GetInstanceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance ID is required")
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	processes, _, _, err := s.service.QueryInstances(ctx, application.ProcessQuery{
		TenantID: request.GetTenantId(), Credential: credential, Limit: 1,
		Filter: &application.ProcessExpression{Predicate: &application.ProcessPredicate{
			Field: application.ProcessInstanceID, Operator: application.CompareEqual,
			Values: []application.QueryValue{{Kind: application.ValueString, String: request.GetInstanceId()}},
		}},
	})
	if err != nil {
		return nil, mapError(err)
	}
	if len(processes) == 0 {
		return nil, mapError(application.ErrNotFound)
	}
	return &projectionv1.GetWorkflowInstanceResponse{Instance: toWire(processes[0].Instance)}, nil
}

func (s *Server) ListWorkflowInstances(
	ctx context.Context,
	request *projectionv1.ListWorkflowInstancesRequest,
) (*projectionv1.ListWorkflowInstancesResponse, error) {
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	after, err := s.cursors.DecodeQuery(request.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	query := application.ProcessQuery{TenantID: request.GetTenantId(), Credential: credential,
		IncludeDeleted: request.GetIncludeDeleted(), Limit: int(request.GetPageSize()), After: after}
	if request.GetWorkflowType() != "" {
		query.Filter = &application.ProcessExpression{Predicate: &application.ProcessPredicate{
			Field: application.ProcessWorkflowType, Operator: application.CompareEqual,
			Values: []application.QueryValue{{Kind: application.ValueString, String: request.GetWorkflowType()}},
		}}
	}
	if len(request.GetStatuses()) != 0 {
		values := make([]application.QueryValue, 0, len(request.GetStatuses()))
		for _, value := range request.GetStatuses() {
			values = append(values, application.QueryValue{Kind: application.ValueString, String: value})
		}
		statuses := application.ProcessExpression{Predicate: &application.ProcessPredicate{Field: application.ProcessStatus, Operator: application.CompareIn, Values: values}}
		if query.Filter == nil {
			query.Filter = &statuses
		} else {
			query.Filter = &application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{*query.Filter, statuses}}
		}
	}
	instances, limit, normalized, err := s.service.QueryInstances(ctx, query)
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.ListWorkflowInstancesResponse{}
	if limit > 0 && len(instances) > limit {
		cursor, cursorErr := application.CursorFor(normalized, instances[limit-1])
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "build projection cursor")
		}
		response.NextPageToken, err = s.cursors.EncodeQuery(normalized.CursorSigningKeyID, cursor)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode projection cursor")
		}
		instances = instances[:limit]
	}
	response.Instances = make([]*projectionv1.WorkflowInstanceReadModel, 0, len(instances))
	for _, instance := range instances {
		response.Instances = append(response.Instances, toWire(instance.Instance))
	}
	return response, nil
}

func toWire(instance application.Instance) *projectionv1.WorkflowInstanceReadModel {
	result := &projectionv1.WorkflowInstanceReadModel{
		TenantId: instance.TenantID, InstanceId: instance.InstanceID,
		WorkflowType: instance.WorkflowType, WorkflowVersion: instance.WorkflowVersion,
		Status: instance.Status, ActiveNodeId: instance.ActiveNodeID,
		LastEventSequence: instance.LastEventSequence,
		StartedAtEpochMs:  uint64(instance.StartedAt.UnixMilli()),
		UpdatedAtEpochMs:  uint64(instance.UpdatedAt.UnixMilli()),
		ConfigVersion:     instance.ConfigVersion,
		PolicyVersion:     instance.PolicyVersion,
		InitiatorActorId:  instance.InitiatorActorID,
		IsDeleted:         instance.IsDeleted,
		Version:           instance.Version,
	}
	if instance.CompletedAt != nil {
		result.CompletedAtEpochMs = uint64(instance.CompletedAt.UnixMilli())
	}
	return result
}

func mapError(err error) error {
	switch {
	case errors.Is(err, application.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, application.ErrInvalidEvent):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, application.ErrInvalidQuery):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, application.ErrQueryForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "projection query deadline exceeded")
	default:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
}
