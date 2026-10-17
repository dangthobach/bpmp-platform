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
}

type RateLimiter interface {
	Allow(context.Context, string) (bool, error)
}
