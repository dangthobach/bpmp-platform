package engineadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// StartCredentials are resolved anew for every job attempt. The provider owns
// Authz issuance and workflow metadata lookup; Engine verifies both signatures.
type StartCredentials struct {
	SignedActorContext  []byte
	SignedWorkloadProof []byte
	StartNodeID         string
	EncryptionKeyScope  string
}

type CredentialsProvider interface {
	ResolveStart(context.Context, application.StartJob) (StartCredentials, error)
}

type Adapter struct {
	client     enginev1.EngineCommandServiceClient
	provider   CredentialsProvider
	workloadID string
	now        func() time.Time
}

func New(client enginev1.EngineCommandServiceClient, provider CredentialsProvider, workloadID string, now func() time.Time) (*Adapter, error) {
	if client == nil || provider == nil || workloadID == "" || now == nil {
		return nil, application.ErrInvalid
	}
	return &Adapter{client: client, provider: provider, workloadID: workloadID, now: now}, nil
}

func (a *Adapter) StartWorkflow(parent context.Context, job application.StartJob) error {
	if job.TenantID == uuid.Nil || job.CaseID == uuid.Nil || job.CommandID != job.CaseID ||
		job.ActorID == "" || job.CorrelationID == "" || job.WorkflowType == "" || job.WorkflowVersion == "" {
		return application.ErrInvalid
	}
	// Keep the RPC deadline within the 30-second job lease.
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	credentials, err := a.provider.ResolveStart(ctx, job)
	if err != nil {
		return errors.Join(application.ErrBeforeEngineSubmit, fmt.Errorf("resolve fresh Engine start credentials: %w", err))
	}
	now := a.now().UTC()
	if err = validateCredentials(credentials, job, a.workloadID, now); err != nil {
		return errors.Join(application.ErrBeforeEngineSubmit, err)
	}
	identity := job.CaseID.String()
	envelope := &enginev1.CommandEnvelope{
		TenantId: job.TenantID.String(), InstanceId: identity, CommandId: identity, IdempotencyKey: identity,
		CorrelationId: job.CorrelationID, ActorId: job.ActorID, WorkflowType: job.WorkflowType,
		WorkflowVersion: job.WorkflowVersion, OccurredAtEpochMs: uint64(now.UnixMilli()),
		EncryptionKeyScope: credentials.EncryptionKeyScope,
		Command:            &enginev1.CommandEnvelope_StartWorkflow{StartWorkflow: &enginev1.StartWorkflow{}},
		AuthorizationContext: &authv1.AuthorizationContext{
			TenantId: job.TenantID.String(), CommandId: identity, CorrelationId: job.CorrelationID,
			EvaluatedAtEpochMs: uint64(now.UnixMilli()),
			ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT,
				SignedProof: credentials.SignedActorContext},
			WorkloadProof: &authv1.WorkloadProof{SignedProof: credentials.SignedWorkloadProof},
			Resource: &authv1.TransitionResource{WorkflowType: job.WorkflowType, WorkflowVersion: job.WorkflowVersion,
				InstanceId: identity, ActiveNodeId: credentials.StartNodeID, Action: "START"},
		},
	}
	receipt, err := a.client.HandleCommand(ctx, envelope)
	if err != nil {
		return err
	}
	if receipt == nil || receipt.GetCommandId() != identity || receipt.GetCommittedSequence() == 0 {
		return errors.New("Engine returned an invalid workflow-start receipt")
	}
	return nil
}

func validateCredentials(value StartCredentials, job application.StartJob, workloadID string, now time.Time) error {
	if value.StartNodeID == "" || value.EncryptionKeyScope == "" ||
		len(value.SignedActorContext) == 0 || len(value.SignedActorContext) > 64*1024 ||
		len(value.SignedWorkloadProof) == 0 || len(value.SignedWorkloadProof) > 64*1024 || now.IsZero() {
		return application.ErrInvalid
	}
	var actor authv1.SignedActorContext
	if err := proto.Unmarshal(value.SignedActorContext, &actor); err != nil {
		return fmt.Errorf("invalid actor context: %w", err)
	}
	var workload authv1.SignedWorkloadContext
	if err := proto.Unmarshal(value.SignedWorkloadProof, &workload); err != nil {
		return fmt.Errorf("invalid workload proof: %w", err)
	}
	nowMS := uint64(now.UnixMilli())
	if _, parseErr := uuid.Parse(job.ActorID); parseErr != nil {
		return application.ErrInvalid
	}
	if actor.GetTenantId() != job.TenantID.String() || actor.GetActorId() != job.ActorID ||
		actor.GetCommandId() != job.CommandID.String() || actor.GetAudienceWorkloadId() != workloadID ||
		actor.GetIssuedAtEpochMs() > nowMS || actor.GetExpiresAtEpochMs() <= nowMS ||
		actor.GetExpiresAtEpochMs()-actor.GetIssuedAtEpochMs() > 120_000 ||
		len(actor.GetSignature()) != ed25519.SignatureSize || len(actor.GetContentHash()) != sha256.Size ||
		workload.GetTenantId() != job.TenantID.String() || workload.GetCommandId() != job.CommandID.String() ||
		workload.GetWorkloadId() != workloadID ||
		workload.GetIssuedAtEpochMs() > nowMS || workload.GetExpiresAtEpochMs() <= nowMS ||
		len(workload.GetSignature()) != ed25519.SignatureSize || len(workload.GetContentHash()) != sha256.Size {
		return application.ErrInvalid
	}
	return nil
}
