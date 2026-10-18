package grpcapi

import (
	"context"
	"errors"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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
	processes, _, normalized, err := s.service.QueryInstances(ctx, application.ProcessQuery{
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
	return &projectionv1.GetWorkflowInstanceResponse{Instance: toWireAuthorized(processes[0].Instance, normalized.Fields)}, nil
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
		response.Instances = append(response.Instances, toWireAuthorized(instance.Instance, normalized.Fields))
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

func toWireAuthorized(instance application.Instance, fields *application.FieldAuthorization) *projectionv1.WorkflowInstanceReadModel {
	result, _ := toWireSelected(instance, fields, nil)
	return result
}

func toWireSelected(instance application.Instance, fields *application.FieldAuthorization, selected []application.ProcessField) (*projectionv1.WorkflowInstanceReadModel, []string) {
	result := toWire(instance)
	message := result.ProtoReflect()
	descriptors := message.Descriptor().Fields()
	selectedNames := make(map[string]struct{}, len(selected))
	for _, field := range selected {
		selectedNames[application.ProcessFieldName(field)] = struct{}{}
	}
	masked := make([]string, 0)
	for i := 0; i < descriptors.Len(); i++ {
		field := descriptors.Get(i)
		name := string(field.Name())
		readable := fields.CanRead(name)
		_, requested := selectedNames[name]
		if !readable || (len(selectedNames) != 0 && !requested) {
			message.Clear(field)
		}
		if !readable {
			masked = append(masked, name)
		}
	}
	return result, masked
}

func mapError(err error) error {
	switch {
	case errors.Is(err, application.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, application.ErrInvalidEvent):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, application.ErrInvalidQuery):
		queryStatus := status.New(codes.InvalidArgument, err.Error())
		var validation *application.QueryValidationError
		if errors.As(err, &validation) {
			detail := &errdetails.BadRequest{}
			for _, violation := range validation.Violations {
				detail.FieldViolations = append(detail.FieldViolations, &errdetails.BadRequest_FieldViolation{
					Field: violation.Path, Description: violation.Rule + ": " + violation.Message,
				})
			}
			if withDetails, detailErr := queryStatus.WithDetails(detail); detailErr == nil {
				queryStatus = withDetails
			}
		}
		return queryStatus.Err()
	case errors.Is(err, application.ErrQueryForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "projection query deadline exceeded")
	default:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
}
