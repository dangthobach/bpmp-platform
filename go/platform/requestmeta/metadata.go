package requestmeta

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

const (
	RequestID     = "x-bpmp-request-id"
	CorrelationID = "x-bpmp-correlation-id"
	TenantID      = "x-bpmp-tenant-id"
	CommandID     = "x-bpmp-command-id"
	TraceID       = "x-bpmp-trace-id"
	TraceParent   = "traceparent"
	TraceState    = "tracestate"

	maxIDLength = 128
)

type Values struct {
	RequestID     string
	CorrelationID string
	TenantID      string
	CommandID     string
	ActorID       string
	WorkflowID    string
	PolicyVersion string
	TraceID       string
	TraceParent   string
	TraceState    string
}

type contextKey struct{}

type contextState struct {
	mu     sync.RWMutex
	values Values
}

// WithValues attaches validated transport metadata. Tenant and command IDs remain
// explicit because only authenticated adapters may populate them.
func WithValues(ctx context.Context, values Values) context.Context {
	values = normalizeValues(ctx, values)
	return context.WithValue(ctx, contextKey{}, &contextState{values: values})
}

func normalizeValues(ctx context.Context, values Values) Values {
	values.RequestID = normalizedID(values.RequestID)
	if values.RequestID == "" {
		values.RequestID = newID()
	}
	values.CorrelationID = normalizedID(values.CorrelationID)
	if values.CorrelationID == "" {
		values.CorrelationID = values.RequestID
	}
	values.TenantID = normalizedID(values.TenantID)
	values.CommandID = normalizedID(values.CommandID)
	if current := traceID(ctx); current != "" {
		values.TraceID = current
	} else if !validTraceID(values.TraceID) {
		values.TraceID = ""
	}
	if current := traceParent(ctx); current != "" {
		values.TraceParent = current
		values.TraceState = trace.SpanContextFromContext(ctx).TraceState().String()
	} else if !validTraceParent(values.TraceParent) {
		values.TraceParent = ""
		values.TraceState = ""
	} else if !validTraceState(values.TraceState) {
		values.TraceState = ""
	}
	return values
}

// Enrich records authenticated transport attributes in the request-scoped state.
// Request and correlation identifiers are immutable after ingress normalization.
func Enrich(ctx context.Context, values Values) bool {
	state, ok := ctx.Value(contextKey{}).(*contextState)
	if !ok {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if tenantID := normalizedID(values.TenantID); tenantID != "" {
		state.values.TenantID = tenantID
	}
	if commandID := normalizedID(values.CommandID); commandID != "" {
		state.values.CommandID = commandID
	}
	if actorID := normalizedID(values.ActorID); actorID != "" {
		state.values.ActorID = actorID
	}
	if workflowID := normalizedID(values.WorkflowID); workflowID != "" {
		state.values.WorkflowID = workflowID
	}
	if policyVersion := normalizedID(values.PolicyVersion); policyVersion != "" {
		state.values.PolicyVersion = policyVersion
	}
	return true
}

func Restore(ctx context.Context, values Values) context.Context {
	carrier := propagation.MapCarrier{}
	if values.TraceParent != "" {
		carrier.Set(TraceParent, values.TraceParent)
	}
	if values.TraceState != "" {
		carrier.Set(TraceState, values.TraceState)
	}
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)
	return WithValues(ctx, values)
}

func Ensure(ctx context.Context) context.Context {
	if _, ok := FromContext(ctx); ok {
		return ctx
	}
	return WithValues(ctx, Values{})
}

func FromContext(ctx context.Context) (Values, bool) {
	state, ok := ctx.Value(contextKey{}).(*contextState)
	if !ok {
		return Values{}, false
	}
	state.mu.RLock()
	values := state.values
	state.mu.RUnlock()
	if current := traceID(ctx); current != "" {
		values.TraceID = current
	}
	return values, true
}

func OutgoingContext(ctx context.Context, values Values) context.Context {
	if current, ok := FromContext(ctx); ok {
		values = merge(current, values)
	}
	ctx = WithValues(ctx, values)
	values, _ = FromContext(ctx)
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	setMetadata(outgoing, RequestID, values.RequestID)
	setMetadata(outgoing, CorrelationID, values.CorrelationID)
	setMetadata(outgoing, TenantID, values.TenantID)
	setMetadata(outgoing, CommandID, values.CommandID)
	setMetadata(outgoing, TraceParent, values.TraceParent)
	setMetadata(outgoing, TraceState, values.TraceState)
	return metadata.NewOutgoingContext(ctx, outgoing)
}

func ValuesFromIncomingContext(ctx context.Context) Values {
	incoming, _ := metadata.FromIncomingContext(ctx)
	return Values{
		RequestID:     firstValid(incoming.Get(RequestID)),
		CorrelationID: firstValid(incoming.Get(CorrelationID)),
		TenantID:      firstValid(incoming.Get(TenantID)),
		CommandID:     firstValid(incoming.Get(CommandID)),
		TraceParent:   first(incoming.Get(TraceParent)),
		TraceState:    first(incoming.Get(TraceState)),
	}
}

func TraceFromIncomingContext(ctx context.Context) (string, string) {
	values := ValuesFromIncomingContext(ctx)
	return values.TraceParent, values.TraceState
}

func TenantFromOutgoingContext(ctx context.Context) string {
	outgoing, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return ""
	}
	return first(outgoing.Get(TenantID))
}

func SlogAttrs(ctx context.Context) []any {
	values, ok := FromContext(ctx)
	if !ok {
		return nil
	}
	attrs := []any{
		slog.String("request_id", values.RequestID),
		slog.String("correlation_id", values.CorrelationID),
	}
	if values.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", values.TraceID))
	}
	if values.TenantID != "" {
		attrs = append(attrs, slog.String("tenant_id", values.TenantID))
	}
	if values.CommandID != "" {
		attrs = append(attrs, slog.String("command_id", values.CommandID))
	}
	if values.ActorID != "" {
		attrs = append(attrs, slog.String("actor_id", values.ActorID))
	}
	if values.WorkflowID != "" {
		attrs = append(attrs, slog.String("workflow_instance_id", values.WorkflowID))
	}
	if values.PolicyVersion != "" {
		attrs = append(attrs, slog.String("policy_version", values.PolicyVersion))
	}
	return attrs
}

func ValidID(value string) bool {
	return normalizedID(value) != ""
}

func merge(base, override Values) Values {
	if override.RequestID == "" {
		override.RequestID = base.RequestID
	}
	if override.CorrelationID == "" {
		override.CorrelationID = base.CorrelationID
	}
	if override.TenantID == "" {
		override.TenantID = base.TenantID
	}
	if override.CommandID == "" {
		override.CommandID = base.CommandID
	}
	if override.TraceID == "" {
		override.TraceID = base.TraceID
	}
	if override.TraceParent == "" {
		override.TraceParent = base.TraceParent
	}
	if override.TraceState == "" {
		override.TraceState = base.TraceState
	}
	return override
}

func normalizedID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxIDLength {
		return ""
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("-_.:", r)) {
			return ""
		}
	}
	return value
}

func validTraceID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return value != "00000000000000000000000000000000"
}

func validTraceParent(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 ||
		len(parts[2]) != 16 || len(parts[3]) != 2 {
		return false
	}
	return validTraceID(parts[1]) && validLowerHex(parts[2]) &&
		parts[2] != "0000000000000000" && validLowerHex(parts[3])
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func validTraceState(value string) bool {
	if len(value) > 512 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}

func traceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func traceParent(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return fmt.Sprintf(
		"00-%s-%s-%02x",
		spanContext.TraceID(), spanContext.SpanID(), byte(spanContext.TraceFlags()),
	)
}

func setMetadata(values metadata.MD, key, value string) {
	if value == "" {
		return
	}
	values.Set(key, value)
}

func firstValid(values []string) string {
	for _, value := range values {
		if value = normalizedID(value); value != "" {
			return value
		}
	}
	return ""
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
