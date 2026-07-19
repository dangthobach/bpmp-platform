package eventprojection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"google.golang.org/protobuf/proto"
)

type Consumer struct{ service *application.Service }

func New(service *application.Service) (*Consumer, error) {
	if service == nil {
		return nil, errors.New("application service is required")
	}
	return &Consumer{service: service}, nil
}

func (c *Consumer) Handle(ctx context.Context, payload []byte) error {
	var envelope enginev1.EventEnvelope
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode committed engine event: %w", err)
	}
	metadata := envelope.GetMetadata()
	if metadata == nil || metadata.GetTenantId() == "" || metadata.GetEventId() == "" {
		return errors.New("committed event metadata is incomplete")
	}
	occurredAt := time.UnixMilli(int64(metadata.GetOccurredAtEpochMs())).UTC()
	switch event := envelope.GetEvent().(type) {
	case *enginev1.EventEnvelope_UserTaskActivated:
		_, _, err := c.service.ProjectActivation(ctx, domain.Activation{
			TenantID: metadata.GetTenantId(), EventID: metadata.GetEventId(),
			InstanceID: metadata.GetInstanceId(), WorkflowType: metadata.GetWorkflowType(),
			WorkflowVersion: metadata.GetWorkflowVersion(), NodeID: event.UserTaskActivated.GetNodeId(),
			TaskType:            event.UserTaskActivated.GetTaskType(),
			AssignmentPolicyRef: event.UserTaskActivated.GetAssignmentPolicyRef(),
			FormKey:             event.UserTaskActivated.GetFormKey(), OccurredAt: occurredAt,
		})
		return err
	case *enginev1.EventEnvelope_UserTaskCompleted:
		return c.service.ProjectCommittedCompletion(
			ctx, metadata.GetTenantId(), metadata.GetInstanceId(),
			event.UserTaskCompleted.GetNodeId(), event.UserTaskCompleted.GetDecision(), occurredAt,
		)
	default:
		return nil
	}
}
