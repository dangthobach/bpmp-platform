package engine

import (
	"context"
	"errors"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
)

type Client struct {
	rpc enginev1.EngineDeploymentServiceClient
}

func New(rpc enginev1.EngineDeploymentServiceClient) (*Client, error) {
	if rpc == nil {
		return nil, errors.New("Engine deployment RPC client is required")
	}
	return &Client{rpc: rpc}, nil
}

func (c *Client) CommitRuntimeDeployment(ctx context.Context, command application.EngineDeploymentCommand) (application.EngineDeploymentState, error) {
	response, err := c.rpc.CommitRuntimeDeployment(ctx, &enginev1.CommitRuntimeDeploymentRequest{
		DeploymentId: command.DeploymentID, TenantId: command.TenantID, WorkflowType: command.WorkflowType, WorkflowVersion: command.WorkflowVersion,
		EnvironmentReference: command.EnvironmentReference, SignedWirArtifact: command.SignedWIRArtifact, ArtifactSha256: command.ArtifactSHA256,
		RollbackOfDeploymentId: command.RollbackOfDeploymentID, DeploymentSequence: command.DeploymentSequence,
	})
	if err != nil {
		return application.EngineDeploymentState{}, err
	}
	return application.EngineDeploymentState{DeploymentID: response.GetDeploymentId(), Status: response.GetStatus(), ArtifactID: response.GetArtifactId(), AppliedNodeIDs: response.GetAppliedNodeIds(), PendingNodeIDs: response.GetPendingNodeIds(), EngineConfigVersion: response.GetEngineConfigVersion(), EnginePolicyVersion: response.GetEnginePolicyVersion(), EngineConfigSHA256: response.GetEngineConfigSha256(), GovernanceConfigVersion: response.GetGovernanceConfigVersion(), GovernancePolicyVersion: response.GetGovernancePolicyVersion()}, nil
}

func (c *Client) GetRuntimeDeployment(ctx context.Context, id string) (application.EngineDeploymentState, error) {
	response, err := c.rpc.GetRuntimeDeployment(ctx, &enginev1.GetRuntimeDeploymentRequest{DeploymentId: id})
	if err != nil {
		return application.EngineDeploymentState{}, err
	}
	return application.EngineDeploymentState{DeploymentID: response.GetDeploymentId(), Status: response.GetStatus(), ArtifactID: response.GetArtifactId(), FailureCode: response.GetFailureCode(), AppliedNodeIDs: response.GetAppliedNodeIds(), PendingNodeIDs: response.GetPendingNodeIds(), EngineConfigVersion: response.GetEngineConfigVersion(), EnginePolicyVersion: response.GetEnginePolicyVersion(), EngineConfigSHA256: response.GetEngineConfigSha256(), GovernanceConfigVersion: response.GetGovernanceConfigVersion(), GovernancePolicyVersion: response.GetGovernancePolicyVersion()}, nil
}

var _ application.RuntimeDeploymentEngine = (*Client)(nil)
