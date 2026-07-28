package eventprojection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type Handler struct {
	service      *application.Service
	consumerName string
}

func New(service *application.Service, consumerName string) (*Handler, error) {
	if service == nil || consumerName == "" {
		return nil, errors.New("projection service and consumer name are required")
	}
	return &Handler{service: service, consumerName: consumerName}, nil
}

func (h *Handler) Handle(ctx context.Context, record *kgo.Record) error {
	if record == nil {
		return errors.New("kafka record is required")
	}
	var envelope enginev1.EventEnvelope
	if err := proto.Unmarshal(record.Value, &envelope); err != nil {
		return fmt.Errorf("decode committed engine event: %w", err)
	}
	event, err := mapEvent(&envelope)
	if err != nil {
		return err
	}
	_, err = h.service.Apply(ctx, application.KafkaPosition{
		ConsumerName: h.consumerName,
		Topic:        record.Topic,
		Partition:    record.Partition,
		Offset:       record.Offset,
	}, event)
	return err
}

func mapEvent(envelope *enginev1.EventEnvelope) (application.InstanceEvent, error) {
	metadata := envelope.GetMetadata()
	if metadata == nil || metadata.GetOccurredAtEpochMs() > uint64(^uint64(0)>>1) {
		return application.InstanceEvent{}, application.ErrInvalidEvent
	}
	event := application.InstanceEvent{
		EventID:         metadata.GetEventId(),
		TenantID:        metadata.GetTenantId(),
		InstanceID:      metadata.GetInstanceId(),
		WorkflowType:    metadata.GetWorkflowType(),
		WorkflowVersion: metadata.GetWorkflowVersion(),
		Sequence:        metadata.GetSequence(),
		OccurredAt:      time.UnixMilli(int64(metadata.GetOccurredAtEpochMs())).UTC(),
		ConfigVersion:   metadata.GetConfigVersion(),
		PolicyVersion:   metadata.GetPolicyVersion(),
		Kind:            application.EventProgress,
	}
	switch value := envelope.GetEvent().(type) {
	case *enginev1.EventEnvelope_WorkflowStarted:
		event.Kind = application.EventStarted
		event.NodeID = value.WorkflowStarted.GetStartNodeId()
	case *enginev1.EventEnvelope_ServiceTaskActivated:
		event.Kind = application.EventNodeActivated
		event.NodeID = value.ServiceTaskActivated.GetNodeId()
	case *enginev1.EventEnvelope_UserTaskActivated:
		event.Kind = application.EventNodeActivated
		event.NodeID = value.UserTaskActivated.GetNodeId()
	case *enginev1.EventEnvelope_ScriptTaskActivated:
		event.Kind = application.EventNodeActivated
		event.NodeID = value.ScriptTaskActivated.GetNodeId()
	case *enginev1.EventEnvelope_CaseActivated:
		event.Kind = application.EventStarted
		event.NodeID = value.CaseActivated.GetCaseId()
	case *enginev1.EventEnvelope_WorkflowCompleted, *enginev1.EventEnvelope_CaseCompleted:
		event.Kind = application.EventCompleted
	case *enginev1.EventEnvelope_WorkflowTerminatedForCompliance:
		event.Kind = application.EventTerminatedForCompliance
	}
	if err := event.Validate(); err != nil {
		return application.InstanceEvent{}, err
	}
	return event, nil
}
