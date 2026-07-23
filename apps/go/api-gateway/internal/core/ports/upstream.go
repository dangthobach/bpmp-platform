package ports

import (
	"context"

	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"google.golang.org/grpc"
)

type Engine interface {
	HandleCommand(context.Context, *enginev1.CommandEnvelope, ...grpc.CallOption) (*enginev1.CommandReceipt, error)
}

type HumanRuntime interface {
	CompleteWorkItem(context.Context, *humanv1.CompleteWorkItemRequest, ...grpc.CallOption) (*humanv1.CompleteWorkItemResponse, error)
	DelegateWorkItem(context.Context, *humanv1.DelegateWorkItemRequest, ...grpc.CallOption) (*humanv1.DelegateWorkItemResponse, error)
}

type RateLimiter interface {
	Allow(context.Context, string) (bool, error)
}
