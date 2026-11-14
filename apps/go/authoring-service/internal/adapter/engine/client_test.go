package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"google.golang.org/grpc"
)

type deploymentRPCStub struct {
	enginev1.EngineDeploymentServiceClient
	commitRequest  *enginev1.CommitRuntimeDeploymentRequest
	getRequest     *enginev1.GetRuntimeDeploymentRequest
	commitResponse *enginev1.CommitRuntimeDeploymentResponse
	getResponse    *enginev1.GetRuntimeDeploymentResponse
	commitError    error
	getError       error
}

func (s *deploymentRPCStub) CommitRuntimeDeployment(_ context.Context, request *enginev1.CommitRuntimeDeploymentRequest, _ ...grpc.CallOption) (*enginev1.CommitRuntimeDeploymentResponse, error) {
	s.commitRequest = request
	return s.commitResponse, s.commitError
}

func (s *deploymentRPCStub) GetRuntimeDeployment(_ context.Context, request *enginev1.GetRuntimeDeploymentRequest, _ ...grpc.CallOption) (*enginev1.GetRuntimeDeploymentResponse, error) {
	s.getRequest = request
	return s.getResponse, s.getError
}

func TestClientMapsDeploymentCommandAndPartialAcknowledgement(t *testing.T) {
	stub := &deploymentRPCStub{commitResponse: &enginev1.CommitRuntimeDeploymentResponse{
		DeploymentId: "deployment-12", Status: "ACTIVATING", ArtifactId: "artifact-sha256",
		AppliedNodeIds: []uint64{1}, PendingNodeIds: []uint64{2},
		EngineConfigVersion: "engine-config-4", EnginePolicyVersion: "engine-policy-6",
		EngineConfigSha256: "engine-config-digest", GovernanceConfigVersion: "gov-config-8",
		GovernancePolicyVersion: "gov-policy-9",
	}}
	client, err := New(stub)
	if err != nil {
		t.Fatal(err)
	}
	command := application.EngineDeploymentCommand{
		DeploymentID: "deployment-12", TenantID: "tenant-1", WorkflowType: "invoice",
		WorkflowVersion: "version-3", EnvironmentReference: "production",
		SignedWIRArtifact: []byte("signed-wir"), ArtifactSHA256: []byte("artifact-digest"),
		RollbackOfDeploymentID: "deployment-7", DeploymentSequence: 12,
	}
	state, err := client.CommitRuntimeDeployment(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	wantRequest := &enginev1.CommitRuntimeDeploymentRequest{
		DeploymentId: command.DeploymentID, TenantId: command.TenantID,
		WorkflowType: command.WorkflowType, WorkflowVersion: command.WorkflowVersion,
		EnvironmentReference: command.EnvironmentReference, SignedWirArtifact: command.SignedWIRArtifact,
		ArtifactSha256: command.ArtifactSHA256, RollbackOfDeploymentId: command.RollbackOfDeploymentID,
		DeploymentSequence: command.DeploymentSequence,
	}
	if !reflect.DeepEqual(stub.commitRequest, wantRequest) {
		t.Fatalf("deployment intent changed across Engine gRPC boundary:\n got: %+v\nwant: %+v", stub.commitRequest, wantRequest)
	}
	wantState := application.EngineDeploymentState{
		DeploymentID: "deployment-12", Status: "ACTIVATING", ArtifactID: "artifact-sha256",
		AppliedNodeIDs: []uint64{1}, PendingNodeIDs: []uint64{2},
		EngineConfigVersion: "engine-config-4", EnginePolicyVersion: "engine-policy-6",
		EngineConfigSHA256: "engine-config-digest", GovernanceConfigVersion: "gov-config-8",
		GovernancePolicyVersion: "gov-policy-9",
	}
	if !reflect.DeepEqual(state, wantState) {
		t.Fatalf("Engine deployment receipt mapping mismatch:\n got: %+v\nwant: %+v", state, wantState)
	}
}

func TestClientMapsGetAndPropagatesErrors(t *testing.T) {
	stub := &deploymentRPCStub{getResponse: &enginev1.GetRuntimeDeploymentResponse{
		DeploymentId: "deployment-13", Status: "FAILED", ArtifactId: "artifact-13",
		FailureCode: "NODE_APPLY_FAILED", AppliedNodeIds: []uint64{1}, PendingNodeIds: []uint64{2},
		EngineConfigVersion: "engine-config-1", EnginePolicyVersion: "engine-policy-1",
		EngineConfigSha256: "digest", GovernanceConfigVersion: "gov-config-1",
		GovernancePolicyVersion: "gov-policy-1",
	}}
	client, err := New(stub)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.GetRuntimeDeployment(context.Background(), "deployment-13")
	if err != nil {
		t.Fatal(err)
	}
	if stub.getRequest.GetDeploymentId() != "deployment-13" || state.DeploymentID != "deployment-13" || state.Status != "FAILED" || state.FailureCode != "NODE_APPLY_FAILED" || !reflect.DeepEqual(state.PendingNodeIDs, []uint64{2}) {
		t.Fatalf("Engine deployment status mapping mismatch: request=%+v state=%+v", stub.getRequest, state)
	}

	upstreamErr := errors.New("Engine unavailable")
	stub.commitError = upstreamErr
	if _, err = client.CommitRuntimeDeployment(context.Background(), application.EngineDeploymentCommand{}); !errors.Is(err, upstreamErr) {
		t.Fatalf("Engine commit error was not preserved: %v", err)
	}
	stub.getError = upstreamErr
	if _, err = client.GetRuntimeDeployment(context.Background(), "deployment-13"); !errors.Is(err, upstreamErr) {
		t.Fatalf("Engine read error was not preserved: %v", err)
	}
}

func TestNewRejectsMissingEngineRPC(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("missing Engine RPC client was accepted")
	}
}
