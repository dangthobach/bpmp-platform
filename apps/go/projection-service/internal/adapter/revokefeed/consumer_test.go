package revokefeed

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"testing"
)

func signedRecord(t *testing.T) (*kgo.Record, map[string]ed25519.PublicKey) {
	t.Helper()
	private := ed25519.NewKeyFromSeed(make([]byte, 32))
	event := &authorizationv1.AuthorizationQueryRevokeEvent{SchemaVersion: 1, EventId: uuid.NewString(), TenantId: uuid.NewString(), ActorId: uuid.NewString(), RevokeEpoch: 1, EventSequence: 7, Revoked: true, OccurredAtEpochMs: 1000, SigningKeyId: "k1"}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	event.ContentHash = hash[:]
	event.Signature = ed25519.Sign(private, append([]byte(signatureDomain), hash[:]...))
	raw, err = proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Topic: "bpmp.authz.query-revocations.v1.test", Key: []byte(event.TenantId), Value: raw, Partition: 0, Offset: 0}, map[string]ed25519.PublicKey{"k1": private.Public().(ed25519.PublicKey)}
}
func TestSignedEventVerification(t *testing.T) {
	r, keys := signedRecord(t)
	if _, err := Verify(r, keys, 4096); err != nil {
		t.Fatal(err)
	}
	r.Value[len(r.Value)-1] ^= 1
	if _, err := Verify(r, keys, 4096); err == nil {
		t.Fatal("tampered signature accepted")
	}
}

func TestVerifierRejectsUntrustedEnvelope(t *testing.T) {
	for _, scenario := range []string{"wrong-key", "unknown-signing-key", "oversized", "unknown-field", "empty", "negative-sequence-wire"} {
		t.Run(scenario, func(t *testing.T) {
			r, keys := signedRecord(t)
			bound := 4096
			switch scenario {
			case "wrong-key":
				r.Key = []byte(uuid.NewString())
			case "unknown-signing-key":
				keys = map[string]ed25519.PublicKey{}
			case "oversized":
				bound = len(r.Value) - 1
			case "unknown-field":
				r.Value = append(r.Value, 0x98, 0x06, 0x01)
			case "empty":
				r.Value = nil
			case "negative-sequence-wire":
				e := &authorizationv1.AuthorizationQueryRevokeEvent{}
				if err := proto.Unmarshal(r.Value, e); err != nil {
					t.Fatal(err)
				}
				e.EventSequence = ^uint64(0)
				r.Value, _ = proto.Marshal(e)
			}
			if _, err := Verify(r, keys, bound); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("accepted %s: %v", scenario, err)
			}
		})
	}
}

func FuzzVerifySignedEvent(f *testing.F) {
	f.Add([]byte("malformed"), []byte("tenant"))
	f.Fuzz(func(t *testing.T, value, key []byte) {
		public := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
		_, _ = Verify(&kgo.Record{Value: value, Key: key}, map[string]ed25519.PublicKey{"k1": public}, 4096)
	})
}

type fakeStore struct {
	applied, quarantined, acknowledged int
	failAck                            bool
}

func (s *fakeStore) Apply(context.Context, *kgo.Record, *authorizationv1.AuthorizationQueryRevokeEvent) error {
	s.applied++
	return nil
}
func (s *fakeStore) Quarantine(_ context.Context, r *kgo.Record, _ string) (*authorizationv1.AuthorizationQueryRevokeDeadLetter, error) {
	s.quarantined++
	return &authorizationv1.AuthorizationQueryRevokeDeadLetter{SchemaVersion: 1, IncidentId: IncidentID(r)}, nil
}
func (s *fakeStore) AcknowledgeDLQ(context.Context, *kgo.Record, string) error {
	if s.failAck {
		return errors.New("database down")
	}
	s.acknowledged++
	return nil
}

type fakeProducer struct {
	err   error
	sends int
}

func (p *fakeProducer) ProduceSync(_ context.Context, r ...*kgo.Record) kgo.ProduceResults {
	p.sends++
	return kgo.ProduceResults{{Record: r[0], Err: p.err}}
}

type fakeClient struct{}

func (fakeClient) PollRecords(context.Context, int) kgo.Fetches        { return nil }
func (fakeClient) CommitRecords(context.Context, ...*kgo.Record) error { return nil }
func TestDLQMustAckBeforeDurableCheckpoint(t *testing.T) {
	r, keys := signedRecord(t)
	r.Value = []byte("malformed")
	store := &fakeStore{}
	producer := &fakeProducer{err: errors.New("broker down")}
	consumer, err := New(fakeClient{}, producer, store, keys, r.Topic, "bpmp.authz.query-revocations-dlq.v1.test", 4096, func() (Policy, error) { return Policy{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if consumer.Handle(context.Background(), r) == nil || store.quarantined != 1 || store.acknowledged != 0 {
		t.Fatal("checkpoint advanced before DLQ ACK")
	}
	producer.err = nil
	store.failAck = true
	if consumer.Handle(context.Background(), r) == nil || store.acknowledged != 0 {
		t.Fatal("failed durable ACK accepted")
	}
	store.failAck = false
	if err = consumer.Handle(context.Background(), r); err != nil || store.acknowledged != 1 {
		t.Fatalf("replay failed: %v", err)
	}
}
