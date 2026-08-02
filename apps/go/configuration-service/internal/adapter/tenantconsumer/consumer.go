package tenantconsumer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	postgresadapter "github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/adapter/postgres"
	tenancyv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/tenancy/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type Client interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type Store interface {
	ApplyTenantLifecycle(
		context.Context,
		postgresadapter.TenantLifecycle,
		time.Time,
	) (bool, error)
}

type Consumer struct {
	client    Client
	store     Store
	batchSize int
}

func New(client Client, store Store, batchSize int) (*Consumer, error) {
	if client == nil || store == nil || batchSize <= 0 {
		return nil, errors.New("tenant lifecycle consumer is invalid")
	}
	return &Consumer{client: client, store: store, batchSize: batchSize}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		fetches := c.client.PollRecords(ctx, c.batchSize)
		if fetchErrors := fetches.Errors(); len(fetchErrors) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			return fetchErrors[0].Err
		}
		for _, record := range fetches.Records() {
			recordCtx, span := requestmeta.StartKafkaConsumerSpan(ctx, record)
			transport, _ := requestmeta.FromContext(recordCtx)
			event, err := decode(record)
			if err != nil {
				requestmeta.RecordSpanError(span, err)
				span.End()
				return err
			}
			_, err = c.store.ApplyTenantLifecycle(recordCtx, postgresadapter.TenantLifecycle{
				EventID:       event.GetEventId(),
				TenantID:      event.GetTenantId(),
				TenantVersion: int64(event.GetTenantVersion()),
				EventSequence: int64(event.GetEventSequence()),
				Deleted: event.GetKind() ==
					tenancyv1.TenantLifecycleKind_TENANT_LIFECYCLE_KIND_DELETED,
				RequestID: transport.RequestID, CorrelationID: event.GetCorrelationId(),
				CommandID: transport.CommandID, TraceParent: transport.TraceParent,
				TraceState: transport.TraceState,
			}, time.UnixMilli(int64(event.GetOccurredAtEpochMs())).UTC())
			if err != nil {
				requestmeta.RecordSpanError(span, err)
				span.End()
				return fmt.Errorf("apply tenant lifecycle readiness: %w", err)
			}
			if err = c.client.CommitRecords(recordCtx, record); err != nil {
				requestmeta.RecordSpanError(span, err)
				span.End()
				return fmt.Errorf("commit tenant lifecycle offset: %w", err)
			}
			span.End()
		}
	}
	return nil
}

func decode(record *kgo.Record) (*tenancyv1.TenantLifecycleEvent, error) {
	if record == nil {
		return nil, errors.New("tenant lifecycle record is required")
	}
	event := &tenancyv1.TenantLifecycleEvent{}
	if err := proto.Unmarshal(record.Value, event); err != nil {
		return nil, fmt.Errorf("decode tenant lifecycle event: %w", err)
	}
	kind := event.GetKind()
	if event.GetSchemaVersion() != 1 ||
		event.GetEventId() == "" ||
		event.GetEventSequence() == 0 ||
		event.GetEventSequence() > math.MaxInt64 ||
		event.GetTenantId() == "" ||
		event.GetTenantCode() == "" ||
		event.GetTenantVersion() > math.MaxInt64 ||
		kind == tenancyv1.TenantLifecycleKind_TENANT_LIFECYCLE_KIND_UNSPECIFIED ||
		event.GetActorRef() == "" ||
		event.GetCorrelationId() == "" ||
		event.GetOccurredAtEpochMs() == 0 {
		return nil, errors.New("tenant lifecycle event metadata is invalid")
	}
	return event, nil
}
