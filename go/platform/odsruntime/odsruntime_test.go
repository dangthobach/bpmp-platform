package odsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type memOutboxStore struct {
	events    []OutboxEnvelope
	published map[string]bool
}

func (m *memOutboxStore) FetchUnpublished(_ context.Context, limit int) ([]OutboxEnvelope, error) {
	var out []OutboxEnvelope
	for _, e := range m.events {
		if !m.published[e.EventID] {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *memOutboxStore) MarkPublished(_ context.Context, eventID string, _ time.Time) error {
	m.published[eventID] = true
	return nil
}

type flakyPublisher struct {
	failFirst bool
	attempts  int
	seenIDs   []string
}

func (f *flakyPublisher) Publish(_ context.Context, _, _ string, env OutboxEnvelope) error {
	f.attempts++
	f.seenIDs = append(f.seenIDs, env.EventID)
	if f.failFirst && f.attempts == 1 {
		return errors.New("broker unavailable")
	}
	return nil
}

func (f *flakyPublisher) Close() {}

func TestOutboxRelayRetainsUnpublishedOnBrokerFailureAndRetriesSameEventID(t *testing.T) {
	store := &memOutboxStore{
		events: []OutboxEnvelope{
			{
				EventID: "evt-1", EventType: "DocumentAvailable", SchemaVersion: 1,
				TenantID: "t1", AggregateType: "Document", AggregateID: "doc-1", AggregateVersion: 2,
				OccurredAt: time.Now().UTC(), Producer: "document-adapter", CorrelationID: "c1",
				Payload: json.RawMessage(`{"document_id":"doc-1"}`),
			},
		},
		published: make(map[string]bool),
	}
	pub := &flakyPublisher{failFirst: true}

	// First pass fails on broker -> must NOT mark published.
	n, hadErr := RelayOnce(context.Background(), nil, store, pub, "bpmp.document.events.v1.e2e", 10)
	if n != 0 || !hadErr {
		t.Fatalf("expected 0 published and hadErr=true, got n=%d hadErr=%v", n, hadErr)
	}
	if store.published["evt-1"] {
		t.Fatalf("event must not be marked published when broker ACK fails")
	}

	// Second pass retries exact same EventID and marks published after ACK.
	n, hadErr = RelayOnce(context.Background(), nil, store, pub, "bpmp.document.events.v1.e2e", 10)
	if n != 1 || hadErr {
		t.Fatalf("expected 1 published and hadErr=false, got n=%d hadErr=%v", n, hadErr)
	}
	if !store.published["evt-1"] || len(pub.seenIDs) != 2 || pub.seenIDs[0] != "evt-1" || pub.seenIDs[1] != "evt-1" {
		t.Fatalf("expected identical event ID retry and durable mark, seen=%v", pub.seenIDs)
	}
}

func TestBuildMTLSConfigValidation(t *testing.T) {
	if cfg, err := BuildMTLSConfig("", "", ""); err != nil || cfg != nil {
		t.Fatalf("expected plaintext nil config when all paths empty, got cfg=%v err=%v", cfg, err)
	}
	if _, err := BuildMTLSConfig("cert.pem", "", "ca.pem"); err == nil {
		t.Fatalf("expected error on partial mTLS config")
	}
}
