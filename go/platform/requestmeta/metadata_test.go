package requestmeta

import (
	"context"
	"fmt"
	"testing"
	"testing/quick"

	"google.golang.org/grpc/metadata"
)

func TestProperty31CorrelationMetadataIsPreservedAcrossGRPCBoundaries(t *testing.T) {
	property := func(seed uint64, withTrace bool) bool {
		correlationID := fmt.Sprintf("correlation-%d", seed)
		values := Values{
			CorrelationID: correlationID,
			TenantID:      fmt.Sprintf("tenant-%d", seed),
			CommandID:     fmt.Sprintf("command-%d", seed),
		}
		if withTrace {
			values.TraceParent = fmt.Sprintf("00-%032x-%016x-01", seed, seed)
			values.TraceState = fmt.Sprintf("bpmp=%x", seed)
		}
		outgoing, ok := metadata.FromOutgoingContext(OutgoingContext(context.Background(), values))
		if !ok ||
			first(outgoing.Get(CorrelationID)) != correlationID ||
			first(outgoing.Get(TenantID)) != values.TenantID ||
			first(outgoing.Get(CommandID)) != values.CommandID {
			return false
		}
		incoming := metadata.NewIncomingContext(context.Background(), outgoing)
		traceParent, traceState := TraceFromIncomingContext(incoming)
		return traceParent == values.TraceParent && traceState == values.TraceState
	}

	// Feature: rust-bpm-platform, Property 31: correlation ID is propagated end to end
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
