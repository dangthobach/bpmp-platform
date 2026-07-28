package grpcapi

import (
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

func TestCursorRoundTripAndBound(t *testing.T) {
	expected := application.Cursor{
		UpdatedAt: time.UnixMilli(1234).UTC(), InstanceID: "instance-1",
	}
	token, err := encodeCursor(expected)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := decodeCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	if !actual.UpdatedAt.Equal(expected.UpdatedAt) || actual.InstanceID != expected.InstanceID {
		t.Fatalf("cursor changed: expected=%+v actual=%+v", expected, actual)
	}
	if _, err = decodeCursor(strings.Repeat("a", maxPageTokenBytes+1)); err == nil {
		t.Fatal("oversized cursor must be rejected before decoding")
	}
}
