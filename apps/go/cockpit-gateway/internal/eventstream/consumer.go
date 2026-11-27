package eventstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	notificationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/notification/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

const (
	SignalWorkflowChanged   = "workflow.changed"
	SignalWorkItemChanged   = "work-item.changed"
	SignalCaseChanged       = "case.changed"
	SignalAuditChanged      = "audit.changed"
	SignalNotificationInApp = "notification.inapp"
)

type kafkaClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type ConsumerOption func(*Consumer) error

func WithInAppHintTopic(topic string) ConsumerOption {
	return func(c *Consumer) error {
		if topic == "" {
			return nil
		}
		if kafkaconfig.ValidateTopic(topic) != nil {
			return errors.New("cockpit in-app hint topic is invalid")
		}
		c.inAppHintTopic = topic
		return nil
	}
}

type Consumer struct {
	client         kafkaClient
	hub            *subscription.Hub
	batchSize      int
	humanTopic     string
	inAppHintTopic string
}

type inAppHintPayload struct {
	Sequence    uint64 `json:"sequence"`
	MessageID   string `json:"message_id"`
	CategoryKey string `json:"category_key,omitempty"`
}

type payload struct {
	EventID         string `json:"eventId"`
	InstanceID      string `json:"instanceId"`
	WorkflowType    string `json:"workflowType"`
	WorkflowVersion string `json:"workflowVersion"`
	Sequence        uint64 `json:"sequence"`
	CorrelationID   string `json:"correlationId"`
	OccurredAtMS    uint64 `json:"occurredAtEpochMs"`
}

func NewConsumer(
	client kafkaClient,
	hub *subscription.Hub,
	batchSize int,
	humanTopic string,
	options ...ConsumerOption,
) (*Consumer, error) {
	if client == nil || hub == nil || batchSize <= 0 || kafkaconfig.ValidateTopic(humanTopic) != nil {
		return nil, errors.New("cockpit event consumer configuration is invalid")
	}
	consumer := &Consumer{client: client, hub: hub, batchSize: batchSize, humanTopic: humanTopic}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(consumer); err != nil {
			return nil, err
		}
	}
	return consumer, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		fetches := c.client.PollRecords(ctx, c.batchSize)
		if values := fetches.Errors(); len(values) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			if err := kafkaconfig.CheckFetchErrors(ctx, values); err != nil {
				return err
			}
		}
		for _, record := range fetches.Records() {
			recordCtx, span := requestmeta.StartKafkaConsumerSpan(ctx, record)
			if err := c.handle(record); err != nil {
				requestmeta.RecordSpanError(span, err)
				span.End()
				return err
			}
			if err := c.client.CommitRecords(recordCtx, record); err != nil {
				requestmeta.RecordSpanError(span, err)
				span.End()
				return fmt.Errorf("commit cockpit event cursor: %w", err)
			}
			span.End()
		}
	}
	return nil
}

func (c *Consumer) handle(record *kgo.Record) error {
	if record == nil {
		return errors.New("kafka record is required")
	}
	if record.Topic == c.humanTopic {
		return c.handleHumanWorkItemChange(record)
	}
	if c.inAppHintTopic != "" && record.Topic == c.inAppHintTopic {
		return c.handleInAppNotificationHint(record)
	}
	var envelope enginev1.EventEnvelope
	if err := proto.Unmarshal(record.Value, &envelope); err != nil {
		return fmt.Errorf("decode committed engine event: %w", err)
	}
	metadata := envelope.GetMetadata()
	if metadata == nil || metadata.GetEventId() == "" ||
		metadata.GetTenantId() == "" || metadata.GetInstanceId() == "" ||
		metadata.GetSequence() == 0 || envelope.GetEvent() == nil {
		return errors.New("committed engine event metadata is invalid")
	}
	encoded, err := json.Marshal(payload{
		EventID: metadata.GetEventId(), InstanceID: metadata.GetInstanceId(),
		WorkflowType:    metadata.GetWorkflowType(),
		WorkflowVersion: metadata.GetWorkflowVersion(),
		Sequence:        metadata.GetSequence(), CorrelationID: metadata.GetCorrelationId(),
		OccurredAtMS: metadata.GetOccurredAtEpochMs(),
	})
	if err != nil {
		return err
	}
	labels := map[string]string{
		"instance_id":   metadata.GetInstanceId(),
		"workflow_type": metadata.GetWorkflowType(),
	}
	cursor := fmt.Sprintf("%s:%d:%d", record.Topic, record.Partition, record.Offset)
	names := []string{SignalWorkflowChanged, SignalAuditChanged}
	switch envelope.GetEvent().(type) {
	case *enginev1.EventEnvelope_UserTaskActivated,
		*enginev1.EventEnvelope_UserTaskCompleted,
		*enginev1.EventEnvelope_UserTaskCancelled:
		names = append(names, SignalWorkItemChanged)
	case *enginev1.EventEnvelope_CaseActivated,
		*enginev1.EventEnvelope_CasePlanItemTransitioned,
		*enginev1.EventEnvelope_CaseSentrySatisfied,
		*enginev1.EventEnvelope_CaseCompleted:
		names = append(names, SignalCaseChanged)
	}
	for _, name := range names {
		c.hub.PublishToIndexedSubscribers(subscription.Signal{
			Cursor: cursor, TenantID: metadata.GetTenantId(), Name: name,
			Labels: labels, Payload: encoded,
		})
	}
	return nil
}

func (c *Consumer) handleHumanWorkItemChange(record *kgo.Record) error {
	var event humanv1.WorkItemChangedEvent
	if err := proto.Unmarshal(record.Value, &event); err != nil {
		return fmt.Errorf("decode committed human work-item change: %w", err)
	}
	if event.GetEventId() == "" || event.GetTenantId() == "" ||
		event.GetWorkItemId() == "" || event.GetVersion() <= 0 ||
		event.GetOccurredAtEpochMs() == 0 || event.GetAction() == "" {
		return errors.New("committed human work-item change metadata is invalid")
	}
	encoded, err := json.Marshal(payload{
		EventID: event.GetEventId(), InstanceID: event.GetWorkItemId(),
		Sequence: uint64(event.GetVersion()), CorrelationID: event.GetCorrelationId(),
		OccurredAtMS: event.GetOccurredAtEpochMs(),
	})
	if err != nil {
		return err
	}
	cursor := fmt.Sprintf("%s:%d:%d", record.Topic, record.Partition, record.Offset)
	c.hub.PublishToIndexedSubscribers(subscription.Signal{
		Cursor: cursor, TenantID: event.GetTenantId(), Name: SignalWorkItemChanged,
		Labels: map[string]string{"work_item_id": event.GetWorkItemId()}, Payload: encoded,
	})
	return nil
}

func (c *Consumer) handleInAppNotificationHint(record *kgo.Record) error {
	var hint notificationv1.InAppNotificationHint
	if err := proto.Unmarshal(record.Value, &hint); err != nil {
		return fmt.Errorf("decode in-app notification hint: %w", err)
	}
	if hint.GetEventId() == "" || hint.GetTenantId() == "" ||
		hint.GetRecipientSubjectRef() == "" || hint.GetMessageId() == "" ||
		hint.GetSequence() == 0 {
		return errors.New("in-app notification hint metadata is invalid")
	}
	encoded, err := json.Marshal(inAppHintPayload{
		Sequence:    hint.GetSequence(),
		MessageID:   hint.GetMessageId(),
		CategoryKey: hint.GetCategoryKey(),
	})
	if err != nil {
		return err
	}
	cursor := fmt.Sprintf("%s:%d:%d", record.Topic, record.Partition, record.Offset)
	c.hub.PublishToIndexedSubscribers(subscription.Signal{
		Cursor:      cursor,
		TenantID:    hint.GetTenantId(),
		Name:        SignalNotificationInApp,
		RecipientID: hint.GetRecipientSubjectRef(),
		Payload:     encoded,
	})
	return nil
}
