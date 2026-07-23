package enginegrpc

import (
	"context"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type recordingClient struct {
	envelope *enginev1.CommandEnvelope
	metadata metadata.MD
}

func (r *recordingClient) HandleCommand(ctx context.Context, in *enginev1.CommandEnvelope, _ ...grpc.CallOption) (*enginev1.CommandReceipt, error) {
	r.envelope = in
	r.metadata, _ = metadata.FromOutgoingContext(ctx)
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
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	))
	err = client.CompleteUserTask(ctx, application.EngineCompleteCommand{
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
	if values := recorder.metadata.Get("x-bpmp-correlation-id"); len(values) != 1 || values[0] != "correlation-1" {
		t.Fatalf("correlation metadata was not preserved: %v", values)
	}
	if values := recorder.metadata.Get("traceparent"); len(values) != 1 {
		t.Fatalf("trace metadata was not forwarded: %v", values)
	}
}
