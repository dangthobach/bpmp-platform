package ports

import (
	"context"

	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
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
	ListAssignmentGroups(context.Context, *humanv1.ListAssignmentGroupsRequest, ...grpc.CallOption) (*humanv1.ListAssignmentGroupsResponse, error)
}

type Authoring interface {
	ListProcessModels(context.Context, *authoringv1.ListProcessModelsRequest, ...grpc.CallOption) (*authoringv1.ListProcessModelsResponse, error)
	CreateProcessModel(context.Context, *authoringv1.CreateProcessModelRequest, ...grpc.CallOption) (*authoringv1.CreateProcessModelResponse, error)
	GetProcessModel(context.Context, *authoringv1.GetProcessModelRequest, ...grpc.CallOption) (*authoringv1.GetProcessModelResponse, error)
	SaveProcessModelDraft(context.Context, *authoringv1.SaveProcessModelDraftRequest, ...grpc.CallOption) (*authoringv1.SaveProcessModelDraftResponse, error)
	ValidateProcessModelDraft(context.Context, *authoringv1.ValidateProcessModelDraftRequest, ...grpc.CallOption) (*authoringv1.ValidateProcessModelDraftResponse, error)
	CheckinProcessModel(context.Context, *authoringv1.CheckinProcessModelRequest, ...grpc.CallOption) (*authoringv1.CheckinProcessModelResponse, error)
	ListProcessModelVersions(context.Context, *authoringv1.ListProcessModelVersionsRequest, ...grpc.CallOption) (*authoringv1.ListProcessModelVersionsResponse, error)
	GetProcessModelVersion(context.Context, *authoringv1.GetProcessModelVersionRequest, ...grpc.CallOption) (*authoringv1.GetProcessModelVersionResponse, error)
	ListDecisionModels(context.Context, *authoringv1.ListDecisionModelsRequest, ...grpc.CallOption) (*authoringv1.ListDecisionModelsResponse, error)
	CreateDecisionModel(context.Context, *authoringv1.CreateDecisionModelRequest, ...grpc.CallOption) (*authoringv1.CreateDecisionModelResponse, error)
	GetDecisionModel(context.Context, *authoringv1.GetDecisionModelRequest, ...grpc.CallOption) (*authoringv1.GetDecisionModelResponse, error)
	SaveDecisionModelDraft(context.Context, *authoringv1.SaveDecisionModelDraftRequest, ...grpc.CallOption) (*authoringv1.SaveDecisionModelDraftResponse, error)
	ValidateDecisionModelDraft(context.Context, *authoringv1.ValidateDecisionModelDraftRequest, ...grpc.CallOption) (*authoringv1.ValidateDecisionModelDraftResponse, error)
	CheckinDecisionModel(context.Context, *authoringv1.CheckinDecisionModelRequest, ...grpc.CallOption) (*authoringv1.CheckinDecisionModelResponse, error)
	ListDecisionModelVersions(context.Context, *authoringv1.ListDecisionModelVersionsRequest, ...grpc.CallOption) (*authoringv1.ListDecisionModelVersionsResponse, error)
	GetDecisionModelVersion(context.Context, *authoringv1.GetDecisionModelVersionRequest, ...grpc.CallOption) (*authoringv1.GetDecisionModelVersionResponse, error)
	ListForms(context.Context, *authoringv1.ListFormsRequest, ...grpc.CallOption) (*authoringv1.ListFormsResponse, error)
	CreateForm(context.Context, *authoringv1.CreateFormRequest, ...grpc.CallOption) (*authoringv1.CreateFormResponse, error)
	GetForm(context.Context, *authoringv1.GetFormRequest, ...grpc.CallOption) (*authoringv1.GetFormResponse, error)
	GetFormByKey(context.Context, *authoringv1.GetFormByKeyRequest, ...grpc.CallOption) (*authoringv1.GetFormByKeyResponse, error)
	SaveFormDraft(context.Context, *authoringv1.SaveFormDraftRequest, ...grpc.CallOption) (*authoringv1.SaveFormDraftResponse, error)
	ValidateFormDraft(context.Context, *authoringv1.ValidateFormDraftRequest, ...grpc.CallOption) (*authoringv1.ValidateFormDraftResponse, error)
	PublishForm(context.Context, *authoringv1.PublishFormRequest, ...grpc.CallOption) (*authoringv1.PublishFormResponse, error)
	ListFormVersions(context.Context, *authoringv1.ListFormVersionsRequest, ...grpc.CallOption) (*authoringv1.ListFormVersionsResponse, error)
	GetFormVersion(context.Context, *authoringv1.GetFormVersionRequest, ...grpc.CallOption) (*authoringv1.GetFormVersionResponse, error)
	DeployProcessModel(context.Context, *authoringv1.DeployProcessModelRequest, ...grpc.CallOption) (*authoringv1.DeployProcessModelResponse, error)
	RollbackRuntimeDeployment(context.Context, *authoringv1.RollbackRuntimeDeploymentRequest, ...grpc.CallOption) (*authoringv1.RollbackRuntimeDeploymentResponse, error)
	GetRuntimeDeployment(context.Context, *authoringv1.GetRuntimeDeploymentRequest, ...grpc.CallOption) (*authoringv1.GetRuntimeDeploymentResponse, error)
	ListRuntimeDeployments(context.Context, *authoringv1.ListRuntimeDeploymentsRequest, ...grpc.CallOption) (*authoringv1.ListRuntimeDeploymentsResponse, error)
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
	SaveDashboard(context.Context, *projectionv1.SaveDashboardRequest, ...grpc.CallOption) (*projectionv1.SaveDashboardResponse, error)
	ListDashboards(context.Context, *projectionv1.ListDashboardsRequest, ...grpc.CallOption) (*projectionv1.ListDashboardsResponse, error)
	DeleteDashboard(context.Context, *projectionv1.DeleteDashboardRequest, ...grpc.CallOption) (*projectionv1.DeleteDashboardResponse, error)
	CreateQueryExport(context.Context, *projectionv1.CreateQueryExportRequest, ...grpc.CallOption) (*projectionv1.CreateQueryExportResponse, error)
	GetQueryExport(context.Context, *projectionv1.GetQueryExportRequest, ...grpc.CallOption) (*projectionv1.GetQueryExportResponse, error)
	CancelQueryExport(context.Context, *projectionv1.CancelQueryExportRequest, ...grpc.CallOption) (*projectionv1.CancelQueryExportResponse, error)
	ExplainQueryPlan(context.Context, *projectionv1.ExplainQueryPlanRequest, ...grpc.CallOption) (*projectionv1.ExplainQueryPlanResponse, error)
}

type RateLimiter interface {
	Allow(context.Context, string) (bool, error)
}
