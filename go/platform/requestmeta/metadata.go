package requestmeta

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const (
	CorrelationID = "x-bpmp-correlation-id"
	TenantID      = "x-bpmp-tenant-id"
	CommandID     = "x-bpmp-command-id"
	TraceParent   = "traceparent"
	TraceState    = "tracestate"
)

type Values struct {
	CorrelationID string
	TenantID      string
	CommandID     string
	TraceParent   string
	TraceState    string
}

func OutgoingContext(ctx context.Context, values Values) context.Context {
	pairs := []string{
		CorrelationID, values.CorrelationID,
		TenantID, values.TenantID,
		CommandID, values.CommandID,
	}
	if values.TraceParent != "" {
		pairs = append(pairs, TraceParent, values.TraceParent)
	}
	if values.TraceState != "" {
		pairs = append(pairs, TraceState, values.TraceState)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

func TraceFromIncomingContext(ctx context.Context) (string, string) {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ""
	}
	return first(incoming.Get(TraceParent)), first(incoming.Get(TraceState))
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
