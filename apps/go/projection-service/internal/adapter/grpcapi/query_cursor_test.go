package grpcapi

import (
	"strings"
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func TestQueryCursorIsSignedAndSupportsKeyRotation(t *testing.T) {
	codec, err := NewCursorCodec([]CursorKey{
		{ID: "old", Secret: []byte(strings.Repeat("o", 32))},
		{ID: "current", Secret: []byte(strings.Repeat("c", 32))},
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor := &application.QueryCursor{QueryHash: "query-hash", Values: []application.QueryValue{{
		Kind: application.ValueString, String: "instance-1",
	}}}
	token, err := codec.EncodeQuery("old", cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.DecodeQuery(token)
	if err != nil || decoded.QueryHash != cursor.QueryHash {
		t.Fatalf("rotated key cursor was rejected: %+v, %v", decoded, err)
	}

	raw := []byte(token)
	if raw[len(raw)-1] == 'A' {
		raw[len(raw)-1] = 'B'
	} else {
		raw[len(raw)-1] = 'A'
	}
	if _, err = codec.DecodeQuery(string(raw)); err == nil {
		t.Fatal("tampered cursor must be rejected")
	}
}
