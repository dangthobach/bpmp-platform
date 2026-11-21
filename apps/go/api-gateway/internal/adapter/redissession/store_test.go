package redissession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	"github.com/redis/go-redis/v9"
)

func TestStoreUsesHashedKeyAndDecodesBoundedRecord(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, err := New(client, Config{
		Prefix: "bff:session", OperationTimeout: time.Second, MaxTokenBytes: 256, MaxRecordBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	csrf := sha256.Sum256([]byte("csrf-token"))
	want := record{
		SchemaVersion: 1,
		TenantID:      "tenant-a", ActorID: "actor-1",
		SignedActorContext: []byte("signed-context"), CSRFTokenSHA256: csrf[:],
		ExpiresAtEpochMS: time.Unix(2_000, 0).UnixMilli(),
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	token := "opaque-session-token"
	digest := sha256.Sum256([]byte(token))
	key := "bff:session:" + hex.EncodeToString(digest[:])
	server.Set(key, string(data))

	got, err := store.Get(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != want.TenantID || got.ActorID != want.ActorID ||
		string(got.SignedActorContext) != string(want.SignedActorContext) ||
		!got.ExpiresAt.Equal(time.UnixMilli(want.ExpiresAtEpochMS)) {
		t.Fatalf("unexpected session: %#v", got)
	}
	if server.Exists("bff:session:" + token) {
		t.Fatal("raw session token was used as a Redis key")
	}
	server.Set(key, `{"schema_version":2}`)
	if _, err = store.Get(context.Background(), token); err == nil {
		t.Fatal("unsupported schema version unexpectedly succeeded")
	}
}

func TestStoreFailsClosedForMissingMalformedAndOversizedRecords(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, err := New(client, Config{
		Prefix: "bff:session", OperationTimeout: time.Second, MaxTokenBytes: 256, MaxRecordBytes: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(context.Background(), "missing"); !errors.Is(err, ports.ErrBrowserSessionNotFound) {
		t.Fatalf("missing session error = %v", err)
	}
	for token, value := range map[string]string{
		"malformed": "not-json",
		"oversized": "record-that-is-too-large",
	} {
		digest := sha256.Sum256([]byte(token))
		server.Set("bff:session:"+hex.EncodeToString(digest[:]), value)
		if _, err = store.Get(context.Background(), token); err == nil {
			t.Fatalf("%s record unexpectedly succeeded", token)
		}
	}
}

func TestStoreCreatesDeletesSessionsAndConsumesOIDCStateOnce(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, err := New(client, Config{
		Prefix: "bff:session", OperationTimeout: time.Second,
		MaxTokenBytes: 256, MaxRecordBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	csrf := sha256.Sum256([]byte("csrf"))
	if err = store.Create(context.Background(), "opaque-session", ports.BrowserSession{
		TenantID: "tenant-a", ActorID: "actor-a", CSRFTokenSHA256: csrf[:],
		ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(context.Background(), "opaque-session"); err != nil || got.ActorID != "actor-a" {
		t.Fatalf("created session not readable: session=%#v err=%v", got, err)
	}
	if err = store.Delete(context.Background(), "opaque-session"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(context.Background(), "opaque-session"); !errors.Is(err, ports.ErrBrowserSessionNotFound) {
		t.Fatalf("deleted session remains readable: %v", err)
	}
	state := "random-state"
	transaction := ports.OIDCLoginTransaction{
		CodeVerifier: "pkce-verifier", Nonce: "nonce", TenantID: "tenant-a",
		ExpiresAt: now.Add(time.Minute),
	}
	if err = store.Save(context.Background(), state, transaction); err != nil {
		t.Fatal(err)
	}
	got, err := store.Consume(context.Background(), state)
	if err != nil || got.CodeVerifier != transaction.CodeVerifier || got.Nonce != transaction.Nonce || got.TenantID != transaction.TenantID {
		t.Fatalf("consumed transaction mismatch: transaction=%#v err=%v", got, err)
	}
	if _, err = store.Consume(context.Background(), state); !errors.Is(err, ports.ErrOIDCTransactionNotFound) {
		t.Fatalf("OIDC state was replayable: %v", err)
	}
}
