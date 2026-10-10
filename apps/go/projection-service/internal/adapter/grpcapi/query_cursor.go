package grpcapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

const (
	maxQueryPageTokenBytes = 8192
	queryCursorKind        = "completed-process-query-v1"
)

type CursorKey struct {
	ID     string
	Secret []byte
}

type CursorCodec struct {
	keys map[string][]byte
}

type signedQueryCursor struct {
	Kind      string          `json:"kind"`
	KeyID     string          `json:"key_id"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func NewCursorCodec(keys []CursorKey) (*CursorCodec, error) {
	if len(keys) == 0 {
		return nil, errors.New("at least one projection cursor signing key is required")
	}
	codec := &CursorCodec{keys: make(map[string][]byte, len(keys))}
	for _, key := range keys {
		if key.ID == "" || len(key.Secret) < 32 || len(key.Secret) > 64 {
			return nil, errors.New("projection cursor signing key is invalid")
		}
		if _, duplicate := codec.keys[key.ID]; duplicate {
			return nil, errors.New("projection cursor signing key is duplicated")
		}
		codec.keys[key.ID] = append([]byte(nil), key.Secret...)
	}
	return codec, nil
}

func (c *CursorCodec) EncodeQuery(keyID string, cursor *application.QueryCursor) (string, error) {
	key, ok := c.keys[keyID]
	if !ok || cursor == nil || cursor.QueryHash == "" || len(cursor.Values) == 0 {
		return "", errors.New("completed process page token input is invalid")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	envelope := signedQueryCursor{Kind: queryCursorKind, KeyID: keyID, Payload: payload}
	envelope.Signature = base64.RawURLEncoding.EncodeToString(signCursor(key, envelope.Kind, keyID, payload))
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(encoded)
	if len(token) > maxQueryPageTokenBytes {
		return "", errors.New("completed process page token exceeds size limit")
	}
	return token, nil
}

func (c *CursorCodec) DecodeQuery(token string) (*application.QueryCursor, error) {
	if token == "" {
		return nil, nil
	}
	if len(token) > maxQueryPageTokenBytes {
		return nil, errors.New("completed process page token exceeds size limit")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errors.New("completed process page token is malformed")
	}
	var envelope signedQueryCursor
	if err = decodeExactJSON(encoded, &envelope); err != nil ||
		envelope.Kind != queryCursorKind || envelope.KeyID == "" ||
		len(envelope.Payload) == 0 || envelope.Signature == "" {
		return nil, errors.New("completed process page token is invalid")
	}
	key, ok := c.keys[envelope.KeyID]
	if !ok {
		return nil, errors.New("completed process page token signing key is unavailable")
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || !hmac.Equal(signature, signCursor(key, envelope.Kind, envelope.KeyID, envelope.Payload)) {
		return nil, errors.New("completed process page token signature is invalid")
	}
	var cursor application.QueryCursor
	if err = decodeExactJSON(envelope.Payload, &cursor); err != nil ||
		cursor.QueryHash == "" || len(cursor.Values) == 0 {
		return nil, errors.New("completed process page token payload is invalid")
	}
	return &cursor, nil
}

func signCursor(key []byte, kind, keyID string, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(keyID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func decodeExactJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}
