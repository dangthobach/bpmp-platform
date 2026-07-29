package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/cockpit-gateway/subscription"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
)

type Config struct {
	Path                  string
	AllowedSignalNames    []string
	AllowedOrigins        []string
	MaxNamesPerConnection uint32
	MaxSignalNamesBytes   uint32
	MaxConnections        uint32
	HeartbeatInterval     time.Duration
}

type Handler struct {
	config         Config
	hub            *subscription.Hub
	identity       *jwtauth.Verifier
	allowedNames   map[string]struct{}
	allowedOrigins map[string]struct{}
	connections    chan struct{}
	now            func() time.Time
}

func New(
	config Config,
	hub *subscription.Hub,
	identity *jwtauth.Verifier,
) (*Handler, error) {
	if config.Path == "" || len(config.AllowedSignalNames) == 0 ||
		len(config.AllowedOrigins) == 0 || config.MaxNamesPerConnection == 0 ||
		config.MaxSignalNamesBytes == 0 ||
		config.MaxConnections == 0 || config.HeartbeatInterval <= 0 ||
		hub == nil || identity == nil {
		return nil, errors.New("realtime HTTP configuration is invalid")
	}
	return &Handler{
		config: config, hub: hub, identity: identity,
		allowedNames:   stringSet(config.AllowedSignalNames),
		allowedOrigins: stringSet(config.AllowedOrigins),
		connections:    make(chan struct{}, config.MaxConnections),
		now:            time.Now,
	}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET "+h.config.Path, h)
	mux.Handle("OPTIONS "+h.config.Path, h)
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !h.authorizeOrigin(response, request) {
		return
	}
	if request.Method == http.MethodOptions {
		response.Header().Set("Access-Control-Allow-Headers",
			"Authorization, Last-Event-ID, X-BPMP-Tenant-ID, X-Correlation-ID")
		response.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		response.WriteHeader(http.StatusNoContent)
		return
	}
	tenantID := request.Header.Get("X-BPMP-Tenant-ID")
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if _, err := h.identity.Verify(token, tenantID, h.now()); err != nil {
		writeError(response, http.StatusUnauthorized, "authentication_invalid")
		return
	}
	names, err := h.signalNames(request.URL.Query().Get("names"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "signal_names_invalid")
		return
	}
	select {
	case h.connections <- struct{}{}:
		defer func() { <-h.connections }()
	default:
		writeError(response, http.StatusServiceUnavailable, "connection_capacity_reached")
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "streaming_unavailable")
		return
	}
	subscriptions := make([]*subscription.Subscription, 0, len(names))
	defer func() {
		for _, value := range subscriptions {
			value.Close()
		}
	}()
	resumeReset := false
	for _, name := range names {
		value, subscribeErr := h.hub.SubscribeFrom(subscription.Filter{
			TenantID: tenantID, Name: name,
		}, request.Header.Get("Last-Event-ID"))
		if subscribeErr != nil {
			status := http.StatusServiceUnavailable
			if !errors.Is(subscribeErr, subscription.ErrCapacity) {
				status = http.StatusBadRequest
			}
			writeError(response, status, "subscription_unavailable")
			return
		}
		subscriptions = append(subscriptions, value)
		resumeReset = resumeReset || value.ResumeReset
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	if resumeReset {
		writeSSE(response, "", "resync-required", []byte(`{"reason":"cursor_unavailable"}`))
	}
	flusher.Flush()

	signals, interrupted := merge(request, subscriptions)
	heartbeat := time.NewTicker(h.config.HeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-interrupted:
			return
		case signal, open := <-signals:
			if !open {
				return
			}
			if err := writeSSE(response, signal.Cursor, signal.Name, signal.Payload); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(response, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func merge(
	request *http.Request,
	values []*subscription.Subscription,
) (<-chan subscription.Signal, <-chan struct{}) {
	output := make(chan subscription.Signal)
	interrupted := make(chan struct{}, 1)
	var remaining = len(values)
	closed := make(chan struct{}, len(values))
	for _, value := range values {
		signals := value.Signals
		go func() {
			defer func() { closed <- struct{}{} }()
			for {
				select {
				case <-request.Context().Done():
					return
				case signal, ok := <-signals:
					if !ok {
						select {
						case interrupted <- struct{}{}:
						default:
						}
						return
					}
					select {
					case output <- signal:
					case <-request.Context().Done():
						return
					}
				}
			}
		}()
	}
	go func() {
		for remaining > 0 {
			<-closed
			remaining--
		}
		close(output)
	}()
	return output, interrupted
}

func (h *Handler) authorizeOrigin(response http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if _, ok := h.allowedOrigins[origin]; !ok {
		writeError(response, http.StatusForbidden, "origin_not_allowed")
		return false
	}
	response.Header().Set("Access-Control-Allow-Origin", origin)
	response.Header().Set("Vary", "Origin")
	return true
}

func (h *Handler) signalNames(raw string) ([]string, error) {
	if len(raw) > int(h.config.MaxSignalNamesBytes) {
		return nil, errors.New("signal names are oversized")
	}
	values := strings.Split(raw, ",")
	if raw == "" || len(values) > int(h.config.MaxNamesPerConnection) {
		return nil, errors.New("signal names are missing or oversized")
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := h.allowedNames[value]; !ok {
			return nil, errors.New("signal name is not allowed")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func bearerToken(value string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	return token, token != ""
}

func writeSSE(response http.ResponseWriter, cursor, event string, data []byte) error {
	if cursor != "" {
		if _, err := fmt.Fprintf(response, "id: %s\n", sanitizeField(cursor)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(response, "event: %s\n", sanitizeField(event)); err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if _, err := fmt.Fprintf(response, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(response, "\n")
	return err
}

func sanitizeField(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}

func writeError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": code})
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
