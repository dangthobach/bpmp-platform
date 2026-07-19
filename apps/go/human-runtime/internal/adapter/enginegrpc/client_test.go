package enginegrpc

import (
	"context"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"google.golang.org/grpc"
)

type recordingClient struct{ envelope *enginev1.CommandEnvelope }

func (r *recordingClient) HandleCommand(_ context.Context, in *enginev1.CommandEnvelope, _ ...grpc.CallOption) (*enginev1.CommandReceipt, error) {
	r.envelope = in
	return &enginev1.CommandReceipt{CommandId: in.CommandId, CommittedSequence: 7}, nil
}

type staticSecurity struct{}

func (staticSecurity) ForTenant(context.Context, string, string) (SecuritySnapshot, error) {
	return SecuritySnapshot{EncryptionKeyScope: "tenant-a/operational", WorkloadProof: []byte("workload-proof")}, nil
}

func TestClientBuildsActorPreservingAuthorizedEngineCommand(t *testing.T) {
	recorder := &recordingClient{}
	client, err := New(recorder, staticSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	err = client.CompleteUserTask(context.Background(), application.EngineCompleteCommand{
		TenantID: "tenant-a", InstanceID: "instance-1", NodeID: "review",
		CommandID: "command-1", CorrelationID: "correlation-1", Decision: "approved",
		WorkflowType: "approval", WorkflowVersion: "1", ActorID: "alice",
		OriginalToken: []byte("actor.jwt"), OccurredAt: time.UnixMilli(123),
	})
	if err != nil {
		t.Fatal(err)
	}
	proof := recorder.envelope.GetAuthorizationContext().GetActorProof()
	if string(proof.GetSignedProof()) != "actor.jwt" {
		t.Fatalf("actor proof changed: %q", proof.GetSignedProof())
	}
	if recorder.envelope.GetCompleteUserTask().GetDecision() != "approved" {
		t.Fatalf("decision missing from command: %#v", recorder.envelope)
	}
}
