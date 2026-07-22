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
	if metadata == nil || metadata.GetTenantId() == "" || metadata.GetEventId() == "" || metadata.GetSequence() == 0 {
		return errors.New("committed event metadata is incomplete")
	}
	occurredAt := time.UnixMilli(int64(metadata.GetOccurredAtEpochMs())).UTC()
	switch event := envelope.GetEvent().(type) {
	case *enginev1.EventEnvelope_UserTaskActivated:
		_, _, err := c.service.ProjectActivation(ctx, domain.Activation{
			TenantID: metadata.GetTenantId(), EventID: metadata.GetEventId(),
			Sequence:   metadata.GetSequence(),
			InstanceID: metadata.GetInstanceId(), WorkflowType: metadata.GetWorkflowType(),
			WorkflowVersion: metadata.GetWorkflowVersion(), NodeID: event.UserTaskActivated.GetNodeId(),
			TaskType:            event.UserTaskActivated.GetTaskType(),
			AssignmentPolicyRef: event.UserTaskActivated.GetAssignmentPolicyRef(),
			FormKey:             event.UserTaskActivated.GetFormKey(), OccurredAt: occurredAt,
		})
		return err
	case *enginev1.EventEnvelope_UserTaskCompleted:
		return c.service.ProjectCommittedCompletion(ctx, application.CommittedCompletion{
			TenantID: metadata.GetTenantId(), EventID: metadata.GetEventId(), Sequence: metadata.GetSequence(),
			InstanceID: metadata.GetInstanceId(), NodeID: event.UserTaskCompleted.GetNodeId(),
			Decision: event.UserTaskCompleted.GetDecision(), OccurredAt: occurredAt,
		})
	case *enginev1.EventEnvelope_UserTaskCancelled:
		return c.service.ProjectCommittedCancellation(ctx, application.CommittedCancellation{
			TenantID: metadata.GetTenantId(), EventID: metadata.GetEventId(), Sequence: metadata.GetSequence(),
			InstanceID: metadata.GetInstanceId(), NodeID: event.UserTaskCancelled.GetNodeId(),
			Reason: event.UserTaskCancelled.GetReason(), OccurredAt: occurredAt,
		})
	case *enginev1.EventEnvelope_CaseActivated:
		caseState, err := domain.NewCase(metadata.GetTenantId(), event.CaseActivated.GetCaseId(), event.CaseActivated.GetCaseType(), event.CaseActivated.GetStageIds(), event.CaseActivated.GetMilestoneIds(), occurredAt)
		if err != nil {
			return err
		}
		_, err = c.service.ProjectCase(ctx, application.CommittedCase{EventID: metadata.GetEventId(), Sequence: metadata.GetSequence(), Case: caseState})
		return err
	case *enginev1.EventEnvelope_CasePlanItemTransitioned:
		return c.service.ProjectCommittedCaseTransition(ctx, application.CommittedCaseTransition{
			TenantID: metadata.GetTenantId(), EventID: metadata.GetEventId(), Sequence: metadata.GetSequence(),
			CaseID: event.CasePlanItemTransitioned.GetCaseId(), PlanItemID: event.CasePlanItemTransitioned.GetPlanItemId(),
			PlanItemKind: event.CasePlanItemTransitioned.GetPlanItemKind(), Status: domain.PlanItemStatus(event.CasePlanItemTransitioned.GetStatus()),
			SatisfiedSentryIDs: append([]string(nil), event.CasePlanItemTransitioned.GetSatisfiedSentryIds()...), OccurredAt: occurredAt,
		})
	case *enginev1.EventEnvelope_CaseCompleted:
		return c.service.ProjectCommittedCaseCompletion(ctx, application.CommittedCaseCompletion{
			TenantID: metadata.GetTenantId(), EventID: metadata.GetEventId(), Sequence: metadata.GetSequence(),
			CaseID: event.CaseCompleted.GetCaseId(), OccurredAt: occurredAt,
		})
	default:
		return nil
	}
}
