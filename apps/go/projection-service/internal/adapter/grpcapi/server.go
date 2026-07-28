package grpcapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxPageTokenBytes = 1024

type Server struct {
	projectionv1.UnimplementedProjectionQueryServiceServer
	service *application.Service
}

func New(service *application.Service) (*Server, error) {
	if service == nil {
		return nil, errors.New("projection application service is required")
	}
	return &Server{service: service}, nil
}

func (s *Server) GetWorkflowInstance(
	ctx context.Context,
	request *projectionv1.GetWorkflowInstanceRequest,
) (*projectionv1.GetWorkflowInstanceResponse, error) {
	instance, err := s.service.Get(ctx, request.GetTenantId(), request.GetInstanceId())
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.GetWorkflowInstanceResponse{Instance: toWire(instance)}, nil
}

func (s *Server) ListWorkflowInstances(
	ctx context.Context,
	request *projectionv1.ListWorkflowInstancesRequest,
) (*projectionv1.ListWorkflowInstancesResponse, error) {
	after, err := decodeCursor(request.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	instances, limit, err := s.service.List(ctx, application.ListFilter{
		TenantID:       request.GetTenantId(),
		Statuses:       append([]string(nil), request.GetStatuses()...),
		WorkflowType:   request.GetWorkflowType(),
		IncludeDeleted: request.GetIncludeDeleted(),
		Limit:          int(request.GetPageSize()),
		After:          after,
	})
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.ListWorkflowInstancesResponse{}
	if limit > 0 && len(instances) > limit {
		last := instances[limit-1]
		response.NextPageToken, err = encodeCursor(application.Cursor{
			UpdatedAt: last.UpdatedAt, InstanceID: last.InstanceID,
		})
		if err != nil {
			return nil, status.Error(codes.Internal, "encode projection cursor")
		}
		instances = instances[:limit]
	}
	response.Instances = make([]*projectionv1.WorkflowInstanceReadModel, 0, len(instances))
	for _, instance := range instances {
		response.Instances = append(response.Instances, toWire(instance))
	}
	return response, nil
}

type cursorPayload struct {
	UpdatedAtEpochMs int64  `json:"updated_at_epoch_ms"`
	InstanceID       string `json:"instance_id"`
}

func encodeCursor(cursor application.Cursor) (string, error) {
	data, err := json.Marshal(cursorPayload{
		UpdatedAtEpochMs: cursor.UpdatedAt.UnixMilli(),
		InstanceID:       cursor.InstanceID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(token string) (*application.Cursor, error) {
	if token == "" {
		return nil, nil
	}
	if len(token) > maxPageTokenBytes {
		return nil, errors.New("projection page token exceeds size limit")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errors.New("projection page token is malformed")
	}
	var payload cursorPayload
	decoderErr := json.Unmarshal(data, &payload)
	if decoderErr != nil || payload.UpdatedAtEpochMs <= 0 || payload.InstanceID == "" {
		return nil, errors.New("projection page token is invalid")
	}
	return &application.Cursor{
		UpdatedAt:  time.UnixMilli(payload.UpdatedAtEpochMs).UTC(),
		InstanceID: payload.InstanceID,
	}, nil
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
	default:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
}
