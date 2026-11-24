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

var consumeBounded = redis.NewScript(`
local value = redis.call("GET", KEYS[1])
if not value then
  return false
end
if string.len(value) > tonumber(ARGV[1]) then
  return redis.error_reply("OIDC transaction exceeds configured bound")
end
redis.call("DEL", KEYS[1])
return value
`)

type Config struct {
	Prefix           string
	OperationTimeout time.Duration
	MaxTokenBytes    int
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

type oidcTransactionRecord struct {
	SchemaVersion uint32 `json:"schema_version"`
	CodeVerifier  string `json:"code_verifier"`
	Nonce         string `json:"nonce"`
	TenantID      string `json:"tenant_id,omitempty"`
	ReturnTo      string `json:"return_to,omitempty"`
	ExpiresAtMS   int64  `json:"expires_at_epoch_ms"`
}

func New(client *redis.Client, config Config) (*Store, error) {
	if client == nil || config.Prefix == "" || config.OperationTimeout <= 0 ||
		config.MaxTokenBytes <= 0 || config.MaxTokenBytes > 4_096 ||
		config.MaxRecordBytes <= 0 {
		return nil, errors.New("Redis browser session store configuration is invalid")
	}
	return &Store{client: client, config: config}, nil
}

func (s *Store) Create(ctx context.Context, token string, session ports.BrowserSession) error {
	if token == "" || len(token) > s.config.MaxTokenBytes || session.TenantID == "" ||
		session.ActorID == "" || len(session.CSRFTokenSHA256) != sha256.Size ||
		!session.ExpiresAt.After(time.Now()) {
		return errors.New("browser session is invalid")
	}
	value := record{
		SchemaVersion: 1, TenantID: session.TenantID, ActorID: session.ActorID,
		SignedActorContext: append([]byte(nil), session.SignedActorContext...),
		CSRFTokenSHA256:    append([]byte(nil), session.CSRFTokenSHA256...),
		ExpiresAtEpochMS:   session.ExpiresAt.UnixMilli(),
	}
	data, err := json.Marshal(value)
	if err != nil || int64(len(data)) > s.config.MaxRecordBytes {
		return errors.New("browser session record exceeds configured bound")
	}
	key := s.sessionKey(token)
	ttl := time.Until(session.ExpiresAt)
	bounded, cancel := context.WithTimeout(ctx, s.config.OperationTimeout)
	defer cancel()
	created, err := s.client.SetNX(bounded, key, data, ttl).Result()
	if err != nil {
		return fmt.Errorf("create browser session: %w", err)
	}
	if !created {
		return errors.New("browser session token collision")
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, token string) error {
	if token == "" || len(token) > s.config.MaxTokenBytes {
		return errors.New("browser session token is invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, s.config.OperationTimeout)
	defer cancel()
	if err := s.client.Del(bounded, s.sessionKey(token)).Err(); err != nil {
		return fmt.Errorf("delete browser session: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, token string) (ports.BrowserSession, error) {
	if token == "" {
		return ports.BrowserSession{}, ports.ErrBrowserSessionNotFound
	}
	key := s.sessionKey(token)
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

func (s *Store) Save(ctx context.Context, state string, transaction ports.OIDCLoginTransaction) error {
	if state == "" || len(state) > s.config.MaxTokenBytes || transaction.CodeVerifier == "" ||
		transaction.Nonce == "" || !transaction.ExpiresAt.After(time.Now()) {
		return errors.New("OIDC login transaction is invalid")
	}
	value := oidcTransactionRecord{
		SchemaVersion: 1, CodeVerifier: transaction.CodeVerifier,
		Nonce: transaction.Nonce, TenantID: transaction.TenantID,
		ReturnTo:    transaction.ReturnTo,
		ExpiresAtMS: transaction.ExpiresAt.UnixMilli(),
	}
	data, err := json.Marshal(value)
	if err != nil || int64(len(data)) > s.config.MaxRecordBytes {
		return errors.New("OIDC login transaction exceeds configured bound")
	}
	bounded, cancel := context.WithTimeout(ctx, s.config.OperationTimeout)
	defer cancel()
	created, err := s.client.SetNX(bounded, s.oidcKey(state), data, time.Until(transaction.ExpiresAt)).Result()
	if err != nil {
		return fmt.Errorf("save OIDC login transaction: %w", err)
	}
	if !created {
		return errors.New("OIDC state collision")
	}
	return nil
}

func (s *Store) Consume(ctx context.Context, state string) (ports.OIDCLoginTransaction, error) {
	if state == "" || len(state) > s.config.MaxTokenBytes {
		return ports.OIDCLoginTransaction{}, ports.ErrOIDCTransactionNotFound
	}
	bounded, cancel := context.WithTimeout(ctx, s.config.OperationTimeout)
	defer cancel()
	result, err := consumeBounded.Run(bounded, s.client, []string{s.oidcKey(state)}, s.config.MaxRecordBytes).Result()
	if errors.Is(err, redis.Nil) {
		return ports.OIDCLoginTransaction{}, ports.ErrOIDCTransactionNotFound
	}
	if err != nil {
		return ports.OIDCLoginTransaction{}, fmt.Errorf("consume OIDC login transaction: %w", err)
	}
	data, ok := result.(string)
	if !ok {
		return ports.OIDCLoginTransaction{}, errors.New("OIDC transaction record has invalid type")
	}
	var value oidcTransactionRecord
	decoder := json.NewDecoder(bytes.NewReader([]byte(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return ports.OIDCLoginTransaction{}, fmt.Errorf("decode OIDC transaction: %w", err)
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ports.OIDCLoginTransaction{}, errors.New("OIDC transaction has trailing data")
	}
	if value.SchemaVersion != 1 || value.CodeVerifier == "" || value.Nonce == "" ||
		value.ExpiresAtMS <= time.Now().UnixMilli() {
		return ports.OIDCLoginTransaction{}, errors.New("OIDC transaction record is invalid or expired")
	}
	return ports.OIDCLoginTransaction{
		CodeVerifier: value.CodeVerifier, Nonce: value.Nonce,
		TenantID: value.TenantID, ReturnTo: value.ReturnTo, ExpiresAt: time.UnixMilli(value.ExpiresAtMS).UTC(),
	}, nil
}

func (s *Store) sessionKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return s.config.Prefix + ":" + hex.EncodeToString(digest[:])
}

func (s *Store) oidcKey(state string) string {
	digest := sha256.Sum256([]byte(state))
	return s.config.Prefix + ":oidc:" + hex.EncodeToString(digest[:])
}
