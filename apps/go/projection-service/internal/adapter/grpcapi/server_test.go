package grpcapi

import (
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func TestCursorRoundTripAndBound(t *testing.T) {
	codec, err := NewCursorCodec([]CursorKey{{ID: "v1", Secret: []byte(strings.Repeat("s", 32))}})
	if err != nil {
		t.Fatal(err)
	}
	expected := &application.QueryCursor{QueryHash: "hash", Values: []application.QueryValue{{Kind: application.ValueTimestamp, Time: time.UnixMilli(1234).UTC()}}}
	token, err := codec.EncodeQuery("v1", expected)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := codec.DecodeQuery(token)
	if err != nil {
		t.Fatal(err)
	}
	if !actual.Values[0].Time.Equal(expected.Values[0].Time) || actual.QueryHash != expected.QueryHash {
		t.Fatalf("cursor changed: expected=%+v actual=%+v", expected, actual)
	}
	if _, err = codec.DecodeQuery(strings.Repeat("a", maxQueryPageTokenBytes+1)); err == nil {
		t.Fatal("oversized cursor must be rejected before decoding")
	}
}
