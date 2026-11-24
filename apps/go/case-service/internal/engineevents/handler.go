package engineevents

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const MaxEventBytes = 1 << 20

type Handler struct {
	service *application.EngineEventService
}

func New(service *application.EngineEventService) (*Handler, error) {
	if service == nil {
		return nil, errors.New("Case Engine event handler dependencies are required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) Handle(ctx context.Context, record *kgo.Record) error {
	if record == nil || len(record.Value) == 0 || len(record.Value) > MaxEventBytes {
		return errors.New("committed Engine event record is missing or exceeds the configured bound")
	}
	var envelope enginev1.EventEnvelope
	if err := proto.Unmarshal(record.Value, &envelope); err != nil {
		return fmt.Errorf("decode committed Engine event: %w", err)
	}
	event, err := mapEvent(&envelope, sha256.Sum256(record.Value))
	if err != nil {
		return err
	}
	_, err = h.service.Apply(ctx, event)
	return err
}

func mapEvent(envelope *enginev1.EventEnvelope, digest [32]byte) (application.EngineEvent, error) {
	metadata := envelope.GetMetadata()
	field := envelope.ProtoReflect().WhichOneof(envelope.ProtoReflect().Descriptor().Oneofs().ByName("event"))
	if metadata == nil || field == nil || metadata.GetSequence() == 0 || metadata.GetSequence() > math.MaxInt64 ||
		metadata.GetOccurredAtEpochMs() > math.MaxInt64 {
		return application.EngineEvent{}, errors.New("committed Engine event metadata is invalid")
	}
	tenantID, err := uuid.Parse(metadata.GetTenantId())
	if err != nil {
		return application.EngineEvent{}, errors.New("committed Engine event tenant identity is invalid")
	}
	if metadata.GetInstanceId() == "" || len(metadata.GetInstanceId()) > 256 {
		return application.EngineEvent{}, errors.New("committed Engine event instance identity is invalid")
	}
	return application.EngineEvent{
		TenantID: tenantID, InstanceID: metadata.GetInstanceId(), EventID: metadata.GetEventId(),
		WorkflowType: metadata.GetWorkflowType(), WorkflowVersion: metadata.GetWorkflowVersion(),
		Sequence: int64(metadata.GetSequence()), EventType: string(field.Name()), NodeID: allowlistedNodeID(envelope, field),
		OccurredAt: time.UnixMilli(int64(metadata.GetOccurredAtEpochMs())).UTC(), PayloadDigest: digest,
	}, nil
}

func allowlistedNodeID(envelope *enginev1.EventEnvelope, field protoreflect.FieldDescriptor) string {
	value := envelope.ProtoReflect().Get(field)
	if !value.IsValid() || field.Kind() != protoreflect.MessageKind {
		return ""
	}
	message := value.Message()
	fields := message.Descriptor().Fields()
	for _, name := range []protoreflect.Name{"node_id", "start_node_id", "gateway_id", "plan_item_id", "boundary_event_id", "scope_node_id"} {
		candidate := fields.ByName(name)
		if candidate != nil && candidate.Kind() == protoreflect.StringKind {
			return message.Get(candidate).String()
		}
	}
	return ""
}
