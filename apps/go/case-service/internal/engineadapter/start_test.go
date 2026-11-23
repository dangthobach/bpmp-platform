package engineadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type credentialsStub struct {
	calls int
	now   time.Time
	bad   bool
}

func (s *credentialsStub) ResolveStart(_ context.Context, job application.StartJob) (StartCredentials, error) {
	s.calls++
	actor := &authv1.SignedActorContext{TenantId: job.TenantID.String(), ActorId: job.ActorID,
		CommandId: job.CommandID.String(), AudienceWorkloadId: "case-service",
		IssuedAtEpochMs: uint64(s.now.UnixMilli()), ExpiresAtEpochMs: uint64(s.now.Add(10 * time.Second).UnixMilli()),
		ContentHash: make([]byte, sha256.Size), Signature: make([]byte, ed25519.SignatureSize)}
	if s.bad {
		actor.TenantId = uuid.NewString()
	}
	workload := &authv1.SignedWorkloadContext{TenantId: job.TenantID.String(), WorkloadId: "case-service",
		CommandId: job.CommandID.String(), IssuedAtEpochMs: uint64(s.now.UnixMilli()),
		ExpiresAtEpochMs: uint64(s.now.Add(10 * time.Second).UnixMilli()),
		ContentHash:      make([]byte, sha256.Size), Signature: make([]byte, ed25519.SignatureSize)}
	actorBytes, _ := proto.Marshal(actor)
	workloadBytes, _ := proto.Marshal(workload)
	return StartCredentials{SignedActorContext: actorBytes, SignedWorkloadProof: workloadBytes,
		StartNodeID: "start-node", EncryptionKeyScope: "tenant/operational"}, nil
}

type engineStub struct {
	commands []*enginev1.CommandEnvelope
	receipt  *enginev1.CommandReceipt
	err      error
}

func (s *engineStub) HandleCommand(_ context.Context, command *enginev1.CommandEnvelope, _ ...grpc.CallOption) (*enginev1.CommandReceipt, error) {
	s.commands = append(s.commands, command)
	if s.err != nil {
		return nil, s.err
	}
	if s.receipt != nil {
		return s.receipt, nil
	}
	return &enginev1.CommandReceipt{CommandId: command.GetCommandId(), CommittedSequence: 1}, nil
}

func TestStartAdapterKeepsEngineIdentityAndRefreshesProof(t *testing.T) {
	now := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	job := application.StartJob{TenantID: uuid.New(), CaseID: uuid.New(), ActorID: uuid.NewString(),
		CorrelationID: "corr-1", WorkflowType: "approval", WorkflowVersion: "1"}
	job.CommandID = job.CaseID
	provider := &credentialsStub{now: now}
	client := &engineStub{}
	adapter, err := New(client, provider, "case-service", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.StartWorkflow(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	provider.now = now
	client.err = errors.New("ambiguous Engine response")
	if err = adapter.StartWorkflow(context.Background(), job); err == nil {
		t.Fatal("ambiguous Engine response was treated as committed")
	}
	if provider.calls != 2 || len(client.commands) != 2 {
		t.Fatalf("credentials were not refreshed on retry: calls=%d commands=%d", provider.calls, len(client.commands))
	}
	first, second := client.commands[0], client.commands[1]
	for _, command := range client.commands {
		if command.GetTenantId() != job.TenantID.String() || command.GetInstanceId() != job.CaseID.String() ||
			command.GetCommandId() != job.CaseID.String() || command.GetIdempotencyKey() != job.CaseID.String() ||
			command.GetAuthorizationContext().GetResource().GetActiveNodeId() != "start-node" ||
			command.GetAuthorizationContext().GetResource().GetAction() != "START" || command.GetStartWorkflow() == nil {
			t.Fatalf("invalid Engine start envelope: %+v", command)
		}
	}
	if string(first.GetAuthorizationContext().GetActorProof().GetSignedProof()) == string(second.GetAuthorizationContext().GetActorProof().GetSignedProof()) {
		t.Fatal("retry reused an old actor proof")
	}
}

func TestStartAdapterRejectsWrongProofScopeAndReceipt(t *testing.T) {
	now := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	job := application.StartJob{TenantID: uuid.New(), CaseID: uuid.New(), ActorID: uuid.NewString(),
		CorrelationID: "corr", WorkflowType: "approval", WorkflowVersion: "1"}
	job.CommandID = job.CaseID
	provider := &credentialsStub{now: now, bad: true}
	client := &engineStub{}
	adapter, _ := New(client, provider, "case-service", func() time.Time { return now })
	if err := adapter.StartWorkflow(context.Background(), job); !errors.Is(err, application.ErrInvalid) || len(client.commands) != 0 {
		t.Fatalf("wrong-tenant proof reached Engine: %v", err)
	}
	provider.bad = false
	client.receipt = &enginev1.CommandReceipt{CommandId: uuid.NewString(), CommittedSequence: 1}
	if err := adapter.StartWorkflow(context.Background(), job); err == nil {
		t.Fatal("mismatched Engine receipt was accepted")
	}
}
