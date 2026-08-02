package eventstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

const (
	SignalWorkflowChanged = "workflow.changed"
	SignalWorkItemChanged = "work-item.changed"
	SignalCaseChanged     = "case.changed"
	SignalAuditChanged    = "audit.changed"
)

type kafkaClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type Consumer struct {
	client    kafkaClient
	hub       *subscription.Hub
	batchSize int
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
) (*Consumer, error) {
	if client == nil || hub == nil || batchSize <= 0 {
		return nil, errors.New("cockpit event consumer configuration is invalid")
	}
	return &Consumer{client: client, hub: hub, batchSize: batchSize}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		fetches := c.client.PollRecords(ctx, c.batchSize)
		if values := fetches.Errors(); len(values) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			return values[0].Err
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
