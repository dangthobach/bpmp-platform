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
		traceSeed := seed | 1
		correlationID := fmt.Sprintf("correlation-%d", seed)
		values := Values{
			CorrelationID: correlationID,
			TenantID:      fmt.Sprintf("tenant-%d", seed),
			CommandID:     fmt.Sprintf("command-%d", seed),
		}
		if withTrace {
			values.TraceParent = fmt.Sprintf("00-%032x-%016x-01", traceSeed, traceSeed)
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

func TestInvalidTraceContextIsDiscarded(t *testing.T) {
	ctx := WithValues(context.Background(), Values{
		RequestID:   "request-1",
		TraceParent: "00-00000000000000000000000000000000-0000000000000000-01",
		TraceState:  "bpmp=untrusted",
	})
	values, ok := FromContext(ctx)
	if !ok {
		t.Fatal("request metadata is missing")
	}
	if values.TraceParent != "" || values.TraceState != "" {
		t.Fatalf("invalid W3C context was retained: %+v", values)
	}
}

func TestEnrichOnlyChangesAuthenticatedFields(t *testing.T) {
	ctx := WithValues(context.Background(), Values{
		RequestID: "request-1", CorrelationID: "correlation-1",
	})
	if !Enrich(ctx, Values{
		RequestID: "replacement", CorrelationID: "replacement",
		TenantID: "tenant-1", CommandID: "command-1", ActorID: "actor-1",
		WorkflowID: "workflow-1", PolicyVersion: "policy-1",
	}) {
		t.Fatal("metadata state was not enriched")
	}
	values, _ := FromContext(ctx)
	if values.RequestID != "request-1" || values.CorrelationID != "correlation-1" ||
		values.TenantID != "tenant-1" || values.CommandID != "command-1" ||
		values.ActorID != "actor-1" || values.WorkflowID != "workflow-1" ||
		values.PolicyVersion != "policy-1" {
		t.Fatalf("unexpected metadata: %+v", values)
	}
}
