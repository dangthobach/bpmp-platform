package ports

import (
	"context"

	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/grpc"
)

type Engine interface {
	HandleCommand(context.Context, *enginev1.CommandEnvelope, ...grpc.CallOption) (*enginev1.CommandReceipt, error)
}

type HumanRuntime interface {
	GetWorkItem(context.Context, *humanv1.GetWorkItemRequest, ...grpc.CallOption) (*humanv1.GetWorkItemResponse, error)
	ListWorkItems(context.Context, *humanv1.ListWorkItemsRequest, ...grpc.CallOption) (*humanv1.ListWorkItemsResponse, error)
	CompleteWorkItem(context.Context, *humanv1.CompleteWorkItemRequest, ...grpc.CallOption) (*humanv1.CompleteWorkItemResponse, error)
	DelegateWorkItem(context.Context, *humanv1.DelegateWorkItemRequest, ...grpc.CallOption) (*humanv1.DelegateWorkItemResponse, error)
	GetCase(context.Context, *humanv1.GetCaseRequest, ...grpc.CallOption) (*humanv1.GetCaseResponse, error)
	ListAuditRecords(context.Context, *humanv1.ListAuditRecordsRequest, ...grpc.CallOption) (*humanv1.ListAuditRecordsResponse, error)
}

type Projection interface {
	QueryCompletedProcesses(context.Context, *projectionv1.QueryCompletedProcessesRequest, ...grpc.CallOption) (*projectionv1.QueryCompletedProcessesResponse, error)
	QueryProcesses(context.Context, *projectionv1.QueryProcessesRequest, ...grpc.CallOption) (*projectionv1.QueryProcessesResponse, error)
	GetQueryCapabilities(context.Context, *projectionv1.GetQueryCapabilitiesRequest, ...grpc.CallOption) (*projectionv1.GetQueryCapabilitiesResponse, error)
	AggregateProcesses(context.Context, *projectionv1.AggregateProcessesRequest, ...grpc.CallOption) (*projectionv1.AggregateProcessesResponse, error)
	QueryEvents(context.Context, *projectionv1.QueryEventsRequest, ...grpc.CallOption) (*projectionv1.QueryEventsResponse, error)
	SaveQuery(context.Context, *projectionv1.SaveQueryRequest, ...grpc.CallOption) (*projectionv1.SaveQueryResponse, error)
	ListSavedQueries(context.Context, *projectionv1.ListSavedQueriesRequest, ...grpc.CallOption) (*projectionv1.ListSavedQueriesResponse, error)
	DeleteSavedQuery(context.Context, *projectionv1.DeleteSavedQueryRequest, ...grpc.CallOption) (*projectionv1.DeleteSavedQueryResponse, error)
	CreateQueryExport(context.Context, *projectionv1.CreateQueryExportRequest, ...grpc.CallOption) (*projectionv1.CreateQueryExportResponse, error)
	GetQueryExport(context.Context, *projectionv1.GetQueryExportRequest, ...grpc.CallOption) (*projectionv1.GetQueryExportResponse, error)
	CancelQueryExport(context.Context, *projectionv1.CancelQueryExportRequest, ...grpc.CallOption) (*projectionv1.CancelQueryExportResponse, error)
	ExplainQueryPlan(context.Context, *projectionv1.ExplainQueryPlanRequest, ...grpc.CallOption) (*projectionv1.ExplainQueryPlanResponse, error)
}

type RateLimiter interface {
	Allow(context.Context, string) (bool, error)
}
