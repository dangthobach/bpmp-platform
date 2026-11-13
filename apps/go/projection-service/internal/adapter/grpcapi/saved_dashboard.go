package grpcapi

import (
	"context"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) SaveDashboard(ctx context.Context, request *projectionv1.SaveDashboardRequest) (*projectionv1.SaveDashboardResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.service.SaveDashboard(ctx, application.SavedDashboard{TenantID: request.GetTenantId(), ID: request.GetId(), Name: request.GetName(), ConfigJSON: request.GetConfigJson()}, request.GetExpectedVersion(), credential)
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.SaveDashboardResponse{Dashboard: toWireSavedDashboard(item)}, nil
}

func (s *Server) ListDashboards(ctx context.Context, request *projectionv1.ListDashboardsRequest) (*projectionv1.ListDashboardsResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	var afterTime time.Time
	afterID := ""
	if request.GetPageToken() != "" {
		cursor, decodeErr := s.cursors.DecodeQuery(request.GetPageToken())
		if decodeErr != nil || cursor.QueryHash != "saved-dashboard-list-v1" || len(cursor.Values) != 2 || cursor.Values[0].Kind != application.ValueTimestamp || cursor.Values[1].Kind != application.ValueString {
			return nil, status.Error(codes.InvalidArgument, "dashboard page token is invalid")
		}
		afterTime, afterID = cursor.Values[0].Time, cursor.Values[1].String
	}
	items, limit, keyID, err := s.service.ListDashboards(ctx, request.GetTenantId(), credential, afterTime, afterID, int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.ListDashboardsResponse{}
	if len(items) > limit {
		last := items[limit-1]
		response.NextPageToken, err = s.cursors.EncodeQuery(keyID, &application.QueryCursor{QueryHash: "saved-dashboard-list-v1", Values: []application.QueryValue{{Kind: application.ValueTimestamp, Time: last.UpdatedAt}, {Kind: application.ValueString, String: last.ID}}})
		if err != nil {
			return nil, status.Error(codes.Internal, "encode dashboard page token")
		}
		items = items[:limit]
	}
	for _, item := range items {
		response.Dashboards = append(response.Dashboards, toWireSavedDashboard(item))
	}
	return response, nil
}

func (s *Server) DeleteDashboard(ctx context.Context, request *projectionv1.DeleteDashboardRequest) (*projectionv1.DeleteDashboardResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	if err = s.service.DeleteDashboard(ctx, request.GetTenantId(), request.GetId(), request.GetExpectedVersion(), credential); err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.DeleteDashboardResponse{}, nil
}

func toWireSavedDashboard(item application.SavedDashboard) *projectionv1.SavedDashboard {
	return &projectionv1.SavedDashboard{Id: item.ID, Name: item.Name, Version: item.Version, ConfigJson: item.ConfigJSON, CreatedAtEpochMs: uint64(item.CreatedAt.UnixMilli()), UpdatedAtEpochMs: uint64(item.UpdatedAt.UnixMilli())}
}
