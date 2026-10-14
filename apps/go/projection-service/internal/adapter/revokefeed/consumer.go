package revokefeed

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"log/slog"
	"math"
	"time"
)

var ErrInvalidEvent = errors.New("invalid query revoke event")

const signatureDomain = "bpmp.authz.query-revoke.v1\x00"

type Policy struct {
	BatchSize                      int
	RetryAttempts                  uint32
	RetryBackoff, OperationTimeout time.Duration
}

func (p Policy) Validate() error {
	if p.BatchSize <= 0 || p.BatchSize > 1000 || p.RetryAttempts == 0 || p.RetryAttempts > 20 || p.RetryBackoff <= 0 || p.RetryBackoff > 5*time.Minute || p.OperationTimeout <= 0 || p.OperationTimeout > 5*time.Minute {
		return errors.New("invalid revoke worker policy")
	}
	return nil
}

type Client interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}
type Producer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}
type Store interface {
	Apply(context.Context, *kgo.Record, *authorizationv1.AuthorizationQueryRevokeEvent) error
	Quarantine(context.Context, *kgo.Record, string) (*authorizationv1.AuthorizationQueryRevokeDeadLetter, error)
	AcknowledgeDLQ(context.Context, *kgo.Record, string) error
}
type Consumer struct {
	client     Client
	producer   Producer
	store      Store
	keys       map[string]ed25519.PublicKey
	topic, dlq string
	maxBytes   int
	policy     func() (Policy, error)
}

func New(client Client, producer Producer, store Store, keys map[string]ed25519.PublicKey, topic, dlq string, maxBytes int, policy func() (Policy, error)) (*Consumer, error) {
	if client == nil || producer == nil || store == nil || len(keys) == 0 || topic == "" || dlq == "" || topic == dlq || maxBytes <= 0 || policy == nil {
		return nil, errors.New("invalid revoke consumer")
	}
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		if id == "" || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid revoke trust key")
		}
		copied[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &Consumer{client: client, producer: producer, store: store, keys: copied, topic: topic, dlq: dlq, maxBytes: maxBytes, policy: policy}, nil
}
func Verify(record *kgo.Record, keys map[string]ed25519.PublicKey, maxBytes int) (*authorizationv1.AuthorizationQueryRevokeEvent, error) {
	if record == nil || len(record.Value) == 0 || len(record.Value) > maxBytes {
		return nil, ErrInvalidEvent
	}
	event := &authorizationv1.AuthorizationQueryRevokeEvent{}
	if proto.Unmarshal(record.Value, event) != nil || len(event.ProtoReflect().GetUnknown()) != 0 || event.SchemaVersion != 1 || event.EventSequence == 0 || event.EventSequence > math.MaxInt64 || event.RevokeEpoch == 0 || event.RevokeEpoch > math.MaxInt64 || event.OccurredAtEpochMs == 0 || event.OccurredAtEpochMs > math.MaxInt64 || len(event.ContentHash) != 32 || len(event.Signature) != 64 || len(event.CorrelationId) > 128 {
		return nil, ErrInvalidEvent
	}
	for _, id := range []string{event.EventId, event.TenantId, event.ActorId} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return nil, ErrInvalidEvent
		}
	}
	if !bytes.Equal(record.Key, []byte(event.TenantId)) {
		return nil, ErrInvalidEvent
	}
	key := keys[event.SigningKeyId]
	if len(key) != ed25519.PublicKeySize {
		return nil, ErrInvalidEvent
	}
	signed := proto.Clone(event).(*authorizationv1.AuthorizationQueryRevokeEvent)
	signed.ContentHash = nil
	signed.Signature = nil
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed)
	if err != nil {
		return nil, ErrInvalidEvent
	}
	hash := sha256.Sum256(canonical)
	if !bytes.Equal(hash[:], event.ContentHash) || !ed25519.Verify(key, append([]byte(signatureDomain), hash[:]...), event.Signature) {
		return nil, ErrInvalidEvent
	}
	return event, nil
}
func (c *Consumer) Handle(ctx context.Context, record *kgo.Record) error {
	ctx, span := requestmeta.StartKafkaConsumerSpan(ctx, record)
	defer span.End()
	var err error
	defer func() { requestmeta.RecordSpanError(span, err) }()
	event, verifyErr := Verify(record, c.keys, c.maxBytes)
	if verifyErr == nil {
		err = c.store.Apply(ctx, record, event)
		if !errors.Is(err, ErrInvalidEvent) {
			return err
		}
	}
	// Quarantine is durable before a DLQ send. The source offset stays uncommitted on any failure.
	incident, quarantineErr := c.store.Quarantine(ctx, record, "invalid_or_conflicting_signed_event")
	if quarantineErr != nil {
		err = quarantineErr
		return err
	}
	slog.WarnContext(ctx, "query revoke quarantined; query authorization fenced", "incident_id", incident.IncidentId, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "reason", incident.ReasonCode)
	payload, encodeErr := proto.MarshalOptions{Deterministic: true}.Marshal(incident)
	if encodeErr != nil {
		err = encodeErr
		return err
	}
	if len(payload) > c.maxBytes {
		err = errors.New("DLQ envelope exceeds transport bound")
		return err
	}
	dead := &kgo.Record{Topic: c.dlq, Key: []byte(incident.IncidentId), Value: payload}
	requestmeta.InjectKafka(ctx, dead)
	err = c.producer.ProduceSync(ctx, dead).FirstErr()
	if err != nil {
		return err
	}
	err = c.store.AcknowledgeDLQ(ctx, record, incident.IncidentId)
	return err
}
func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		policy, err := c.policy()
		if err != nil {
			return err
		}
		if err = policy.Validate(); err != nil {
			return err
		}
		fetched := c.client.PollRecords(ctx, policy.BatchSize)
		if faults := fetched.Errors(); len(faults) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			if err := kafkaconfig.CheckFetchErrors(ctx, faults); err != nil {
				return err
			}
		}
		for _, record := range fetched.Records() {
			if record.Topic != c.topic {
				return errors.New("revoke consumer received unconfigured topic")
			}
			for attempt := uint32(0); attempt < policy.RetryAttempts; attempt++ {
				command, cancel := context.WithTimeout(ctx, policy.OperationTimeout)
				err = c.Handle(command, record)
				cancel()
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return nil
				}
				timer := time.NewTimer(policy.RetryBackoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
			}
			if err != nil {
				return fmt.Errorf("revoke processing retries exhausted: %w", err)
			}
			err = kafkaconfig.RetryOffsetCommit(ctx, c.client, policy.RetryAttempts, policy.RetryBackoff, policy.OperationTimeout, record)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func IncidentID(record *kgo.Record) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%d", record.Topic, record.Partition, record.Offset)))
	return hex.EncodeToString(hash[:])
}
