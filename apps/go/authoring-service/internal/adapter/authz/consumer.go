package authz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type publicationClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type acknowledgementProducer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

// RunBundleConsumer requires a distinct consumer group per Authoring replica so
// every evaluator installs every signed publication before acknowledging it.
func RunBundleConsumer(ctx context.Context, client publicationClient, producer acknowledgementProducer, store *BundleStore, topic, acknowledgementTopic, consumerID string, maxRecords, maxMessageBytes int) error {
	if client == nil || producer == nil || store == nil || topic == "" || acknowledgementTopic == "" || consumerID == "" || maxRecords < 1 || maxRecords > 100 || maxMessageBytes < 1 {
		return ErrInvalidBundle
	}
	for ctx.Err() == nil {
		fetches := client.PollRecords(ctx, maxRecords)
		if faults := fetches.Errors(); len(faults) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("authorization bundle fetch failed: %w", faults[0].Err)
		}
		for _, record := range fetches.Records() {
			if record.Topic != topic || len(record.Value) == 0 || len(record.Value) > maxMessageBytes {
				return ErrInvalidBundle
			}
			publication := &authv1.AuthorizationPolicyBundlePublication{}
			if (&proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(record.Value, publication) != nil || publication.GetEventId() == "" || publication.GetTenantId() == "" || publication.GetBundleSequence() == 0 || publication.GetCatalogVersion() == 0 || publication.GetPublishedAtEpochMs() == 0 || len(publication.GetBundleBytes()) == 0 || !bytes.Equal(record.Key, []byte(publication.GetTenantId())) {
				return ErrInvalidBundle
			}
			canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(publication)
			if err != nil || !bytes.Equal(canonical, record.Value) {
				return ErrInvalidBundle
			}
			bundle := &authv1.AuthorizationPolicyBundle{}
			if proto.Unmarshal(publication.GetBundleBytes(), bundle) != nil || bundle.GetTenantId() != publication.GetTenantId() || bundle.GetBundleSequence() != publication.GetBundleSequence() || bundle.GetCatalogVersion() != publication.GetCatalogVersion() {
				return ErrInvalidBundle
			}
			if err = store.InstallContext(ctx, publication.GetBundleBytes()); err != nil {
				return err
			}
			ack := &authv1.AuthorizationPolicyBundleAcknowledgement{
				SchemaVersion:     1,
				AcknowledgementId: fmt.Sprintf("%s/%s/%d", consumerID, publication.GetTenantId(), publication.GetBundleSequence()),
				ConsumerId:        consumerID, ConsumerType: "AUTHORING", TenantId: publication.GetTenantId(),
				BundleSequence: publication.GetBundleSequence(), CatalogVersion: publication.GetCatalogVersion(),
				BundleContentHash: append([]byte(nil), bundle.GetContentHash()...), AcknowledgedAtEpochMs: uint64(time.Now().UTC().UnixMilli()),
			}
			ackBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(ack)
			if err != nil {
				return err
			}
			if err = producer.ProduceSync(ctx, &kgo.Record{Topic: acknowledgementTopic, Key: []byte(publication.GetTenantId()), Value: ackBytes}).FirstErr(); err != nil {
				return fmt.Errorf("publish authorization bundle acknowledgement: %w", err)
			}
			if err = client.CommitRecords(ctx, record); err != nil {
				return err
			}
		}
	}
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}
	return nil
}
