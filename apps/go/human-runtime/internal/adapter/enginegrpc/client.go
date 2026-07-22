package enginegrpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
)

type SecuritySnapshot struct {
	EncryptionKeyScope string
	WorkloadProof      []byte
}

type SecurityProvider interface {
	ForTenant(context.Context, string, string) (SecuritySnapshot, error)
}

type Client struct {
	client   enginev1.EngineCommandServiceClient
	security SecurityProvider
}

func New(client enginev1.EngineCommandServiceClient, security SecurityProvider) (*Client, error) {
	if client == nil || security == nil {
		return nil, errors.New("engine client and security provider are required")
	}
	return &Client{client: client, security: security}, nil
}

func (c *Client) CompleteUserTask(ctx context.Context, command application.EngineCompleteCommand) error {
	snapshot, err := c.security.ForTenant(ctx, command.TenantID, command.CommandID)
	if err != nil {
		return fmt.Errorf("resolve workload security snapshot: %w", err)
	}
	actorProof, err := actorProof(command)
	if err != nil {
		return err
	}
	if command.OccurredAt.IsZero() {
		return errors.New("command occurrence time is required")
	}
	occurredAt := uint64(command.OccurredAt.UnixMilli())
	envelope := &enginev1.CommandEnvelope{
		TenantId: command.TenantID, InstanceId: command.InstanceID,
		CommandId: command.CommandID, IdempotencyKey: command.IdempotencyKey,
		CorrelationId: command.CorrelationID, ActorId: command.ActorID,
		WorkflowType: command.WorkflowType, WorkflowVersion: command.WorkflowVersion,
		OccurredAtEpochMs: occurredAt, EncryptionKeyScope: snapshot.EncryptionKeyScope,
		Command: &enginev1.CommandEnvelope_CompleteUserTask{CompleteUserTask: &enginev1.CompleteUserTask{
			NodeId: command.NodeID, Decision: command.Decision,
		}},
		AuthorizationContext: &authv1.AuthorizationContext{
			TenantId: command.TenantID, CommandId: command.CommandID,
			CorrelationId: command.CorrelationID, EvaluatedAtEpochMs: occurredAt,
			ActorProof: actorProof, WorkloadProof: &authv1.WorkloadProof{SignedProof: append([]byte(nil), snapshot.WorkloadProof...)},
			Resource: &authv1.TransitionResource{
				WorkflowType: command.WorkflowType, WorkflowVersion: command.WorkflowVersion,
				InstanceId: command.InstanceID, ActiveNodeId: command.NodeID, Action: "COMPLETE_USER_TASK",
			},
		},
	}
	receipt, err := c.client.HandleCommand(ctx, envelope)
	if err != nil {
		return err
	}
	if receipt.GetCommandId() != command.CommandID {
		return errors.New("engine receipt command id does not match request")
	}
	return nil
}

func actorProof(command application.EngineCompleteCommand) (*authv1.ActorProof, error) {
	switch {
	case len(command.OriginalToken) > 0 && len(command.SignedActorContext) == 0:
		return &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: append([]byte(nil), command.OriginalToken...)}, nil
	case len(command.SignedActorContext) > 0 && len(command.OriginalToken) == 0:
		return &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT, SignedProof: append([]byte(nil), command.SignedActorContext...)}, nil
	default:
		return nil, application.ErrActorProof
	}
}

var _ application.EnginePort = (*Client)(nil)
