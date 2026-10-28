package redissession

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	"github.com/redis/go-redis/v9"
)

var boundedGet = redis.NewScript(`
local value = redis.call("GET", KEYS[1])
if not value then
  return false
end
if string.len(value) > tonumber(ARGV[1]) then
  return redis.error_reply("session record exceeds configured bound")
end
return value
`)

type Config struct {
	Prefix           string
	OperationTimeout time.Duration
	MaxRecordBytes   int64
}

type Store struct {
	client *redis.Client
	config Config
}

type record struct {
	SchemaVersion      uint32 `json:"schema_version"`
	TenantID           string `json:"tenant_id"`
	ActorID            string `json:"actor_id"`
	SignedActorContext []byte `json:"signed_actor_context"`
	CSRFTokenSHA256    []byte `json:"csrf_token_sha256"`
	ExpiresAtEpochMS   int64  `json:"expires_at_epoch_ms"`
}

func New(client *redis.Client, config Config) (*Store, error) {
	if client == nil || config.Prefix == "" || config.OperationTimeout <= 0 ||
		config.MaxRecordBytes <= 0 {
		return nil, errors.New("Redis browser session store configuration is invalid")
	}
	return &Store{client: client, config: config}, nil
}

func (s *Store) Get(ctx context.Context, token string) (ports.BrowserSession, error) {
	if token == "" {
		return ports.BrowserSession{}, ports.ErrBrowserSessionNotFound
	}
	digest := sha256.Sum256([]byte(token))
	key := s.config.Prefix + ":" + hex.EncodeToString(digest[:])
	bounded, cancel := context.WithTimeout(ctx, s.config.OperationTimeout)
	defer cancel()
	result, err := boundedGet.Run(
		bounded, s.client, []string{key}, s.config.MaxRecordBytes,
	).Result()
	if errors.Is(err, redis.Nil) {
		return ports.BrowserSession{}, ports.ErrBrowserSessionNotFound
	}
	if err != nil {
		return ports.BrowserSession{}, fmt.Errorf("load browser session: %w", err)
	}
	data, ok := result.(string)
	if !ok {
		return ports.BrowserSession{}, errors.New("browser session record has invalid type")
	}
	var value record
	decoder := json.NewDecoder(bytes.NewReader([]byte(data)))
	if err = decoder.Decode(&value); err != nil {
		return ports.BrowserSession{}, fmt.Errorf("decode browser session: %w", err)
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ports.BrowserSession{}, errors.New("browser session record has trailing data")
	}
	if value.SchemaVersion != 1 {
		return ports.BrowserSession{}, errors.New("browser session schema version is unsupported")
	}
	return ports.BrowserSession{
		TenantID: value.TenantID, ActorID: value.ActorID,
		SignedActorContext: append([]byte(nil), value.SignedActorContext...),
		CSRFTokenSHA256:    append([]byte(nil), value.CSRFTokenSHA256...),
		ExpiresAt:          time.UnixMilli(value.ExpiresAtEpochMS).UTC(),
	}, nil
}
