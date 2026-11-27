package gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

const (
	realtimePath           = "/realtime/v1/events"
	maxRealtimeNames       = 5
	maxRealtimeNameBytes   = 256
	maxRealtimeConnections = 512
)

type realtimeProxy struct {
	client      httpDoer
	baseURL     *url.URL
	connections chan struct{}
}

func WithRealtimeProxy(client httpDoer, rawURL string) HandlerOption {
	return func(handler *Handler) error {
		proxy, err := newRealtimeProxy(client, rawURL)
		if err != nil {
			return err
		}
		handler.realtimeProxy = proxy
		return nil
	}
}

func newRealtimeProxy(client httpDoer, rawURL string) (*realtimeProxy, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.Path != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" || client == nil {
		return nil, errors.New("Cockpit realtime upstream must be an HTTPS origin")
	}
	return &realtimeProxy{client: client, baseURL: baseURL, connections: make(chan struct{}, maxRealtimeConnections)}, nil
}

func (h *Handler) proxyRealtime(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	names, err := validateRealtimeNames(r.URL.Query().Get("names"), scope.capabilities)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(scope.signedActorProof) == 0 || scope.actorProofType != authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT {
		writeError(w, errUnauthorized)
		return
	}
	lastEventID := r.Header.Get("Last-Event-ID")
	if len(lastEventID) > 256 || strings.ContainsAny(lastEventID, "\r\n") {
		writeError(w, errInvalid)
		return
	}
	select {
	case h.realtimeProxy.connections <- struct{}{}:
		defer func() { <-h.realtimeProxy.connections }()
	default:
		writeError(w, errRateLimited)
		return
	}

	ctx := r.Context()
	cancel := func() {}
	if scope.sessionBound && !scope.sessionExpiresAt.IsZero() {
		ctx, cancel = context.WithDeadline(ctx, scope.sessionExpiresAt)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	if scope.sessionBound {
		h.cancelOnSessionEnd(ctx, cancel, r, scope)
	}
	upstreamURL := *h.realtimeProxy.baseURL
	upstreamURL.Path = realtimePath
	upstreamURL.RawQuery = url.Values{"names": []string{strings.Join(names, ",")}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL.String(), nil)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("X-BPMP-Tenant-ID", scope.tenantID)
	request.Header.Set("X-BPMP-Signed-Actor-Context", base64.StdEncoding.EncodeToString(scope.signedActorProof))
	request.Header.Set("X-Correlation-ID", scope.correlationID)
	if origin := r.Header.Get("Origin"); origin != "" {
		request.Header.Set("Origin", origin)
	}
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	requestmeta.InjectHTTP(ctx, request.Header)
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	response, err := h.realtimeProxy.client.Do(request)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			writeError(w, errUnauthorized)
		case http.StatusForbidden:
			writeError(w, errForbidden)
		case http.StatusTooManyRequests:
			writeError(w, errRateLimited)
		default:
			writeError(w, errUpstream)
		}
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, errUpstream)
		return
	}
	for _, key := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	buffer := make([]byte, 16*1024)
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, writeErr := w.Write(buffer[:read]); writeErr != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
				return
			}
			return
		}
	}
}

func (h *Handler) cancelOnSessionEnd(ctx context.Context, cancel context.CancelFunc, r *http.Request, scope requestScope) {
	tokens := h.browserSessionTokens(r)
	if len(tokens) != 1 || tokens[0] == "" {
		cancel()
		return
	}
	go func(token, tenantID, actorID string) {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				session, err := h.sessionStore.Get(ctx, token)
				if err != nil || session.TenantID != tenantID || session.ActorID != actorID || !session.ExpiresAt.After(h.now()) {
					cancel()
					return
				}
			}
		}
	}(tokens[0], scope.tenantID, scope.actorID)
}

func validateRealtimeNames(raw string, capabilities []string) ([]string, error) {
	if raw == "" || len(raw) > maxRealtimeNameBytes {
		return nil, errInvalid
	}
	values := strings.Split(raw, ",")
	if len(values) > maxRealtimeNames {
		return nil, errInvalid
	}
	required := map[string][]string{
		"work-item.changed":  {"task.read"},
		"case.changed":       {"case.read"},
		"audit.changed":      {"audit.read"},
		"workflow.changed":   {"workflow.query.own", "workflow.query.all", "task.read"},
		"notification.inapp": {"notification.inbox.read", "notification.read"},
	}
	seen := make(map[string]struct{}, len(values))
	for _, name := range values {
		capabilitiesForName, ok := required[name]
		if !ok || strings.TrimSpace(name) != name {
			return nil, errInvalid
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		allowed := false
		for _, capability := range capabilitiesForName {
			allowed = allowed || hasCapability(capabilities, capability)
		}
		if !allowed {
			return nil, errForbidden
		}
	}
	return values, nil
}
