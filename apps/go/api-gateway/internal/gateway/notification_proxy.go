package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

const notificationPath = "/v1/notifications"

type notificationProxy struct {
	client  httpDoer
	baseURL *url.URL
}

func WithNotificationServiceProxy(client httpDoer, rawURL string) HandlerOption {
	return func(handler *Handler) error {
		proxy, err := newNotificationProxy(client, rawURL)
		if err != nil {
			return err
		}
		handler.notificationProxy = proxy
		return nil
	}
}

func newNotificationProxy(client httpDoer, rawURL string) (*notificationProxy, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil || client == nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.Path != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("Notification Service upstream must be an HTTPS origin")
	}
	return &notificationProxy{client: client, baseURL: baseURL}, nil
}

func (h *Handler) proxyNotification(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	if !allowedNotificationMethod(r.Method) {
		writeError(w, errInvalid)
		return
	}
	isMutation := r.Method != http.MethodGet
	var (
		scope requestScope
		err   error
	)
	if isMutation {
		scope, err = h.authenticate(r)
	} else {
		scope, err = h.authenticateQuery(r)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if scope.actorProofType != authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT || len(scope.signedActorProof) == 0 {
		writeError(w, errUnauthorized)
		return
	}
	if isMutation && (!validID(scope.commandID) || !validID(scope.idempotencyKey)) {
		writeError(w, errInvalid)
		return
	}
	if !validNotificationPath(r.URL) {
		writeError(w, errInvalid)
		return
	}
	subpath := strings.TrimPrefix(r.URL.Path, notificationPath)
	requiredCaps, knownRoute := notificationRequiredCapabilities(r.Method, subpath, r.URL.Query())
	if !knownRoute || !hasAnyCapability(scope.capabilities, requiredCaps) {
		writeError(w, errForbidden)
		return
	}
	var body io.Reader
	if isMutation {
		if r.ContentLength > scope.runtimePolicy.MaxRequestBodyBytes {
			writeError(w, errInvalid)
			return
		}
		if r.Body != nil {
			data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, scope.runtimePolicy.MaxRequestBodyBytes))
			if readErr != nil {
				writeError(w, errInvalid)
				return
			}
			if len(data) > 0 {
				body = bytes.NewReader(data)
			}
		}
	}
	ctx, cancel := context.WithTimeout(upstreamContext(r, scope), scope.runtimePolicy.UpstreamTimeout)
	defer cancel()
	target := *h.notificationProxy.baseURL
	target.Path, target.RawPath, target.RawQuery = r.URL.Path, "", r.URL.RawQuery
	request, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-BPMP-Tenant-ID", scope.tenantID)
	request.Header.Set("X-BPMP-Signed-Actor-Context", base64.StdEncoding.EncodeToString(scope.signedActorProof))
	request.Header.Set("X-Correlation-ID", scope.correlationID)
	if isMutation {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", scope.idempotencyKey)
		request.Header.Set("X-Command-ID", scope.commandID)
	}
	if scope.traceParent != "" {
		request.Header.Set("traceparent", scope.traceParent)
	}
	if scope.traceState != "" {
		request.Header.Set("tracestate", scope.traceState)
	}
	requestmeta.InjectHTTP(ctx, request.Header)
	response, err := h.notificationProxy.client.Do(request)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	defer response.Body.Close()
	max := scope.runtimePolicy.MaxUpstreamResponseBytes
	if max <= 0 {
		writeError(w, errUpstream)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || int64(len(data)) > max {
		writeError(w, errUpstream)
		return
	}
	for _, key := range []string{"Content-Type", "ETag"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}

func allowedNotificationMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validNotificationPath(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	p := parsed.Path
	if len(p) < len(notificationPath) {
		return false
	}
	if len(p) > len(notificationPath) && !strings.HasPrefix(p, notificationPath+"/") {
		return false
	}
	if p != notificationPath && !strings.HasPrefix(p, notificationPath+"/") {
		return false
	}
	raw := strings.ToLower(parsed.RawPath)
	if strings.Contains(p, "..") || strings.Contains(raw, "..") || strings.Contains(raw, "%2e") {
		return false
	}
	return true
}

func notificationRequiredCapabilities(method, subpath string, query url.Values) ([]string, bool) {
	isGet := method == http.MethodGet
	switch {
	case matchSubpathPrefix(subpath, "/inbox"):
		if isGet {
			return []string{"notification.inbox.read"}, true
		}
		return []string{"notification.inbox.write"}, true

	case matchSubpathPrefix(subpath, "/preferences"):
		if isGet {
			return []string{"notification.preference.read", "notification.inbox.read"}, true
		}
		return []string{"notification.preference.write", "notification.inbox.write", "notification.updateNotificationPreferences"}, true

	case method == http.MethodPost && subpath == "/send":
		if strings.EqualFold(query.Get("dry_run"), "true") {
			return []string{"notification.operations.plan", "notification.operations.send"}, true
		}
		return []string{"notification.operations.send"}, true

	case method == http.MethodPost && subpath == "/test-send":
		return []string{"notification.operations.test_send"}, true

	case method == http.MethodPost && subpath == "/preview":
		return []string{"notification.preview.render"}, true

	case method == http.MethodPost && subpath == "/templates/validate":
		return []string{"notification.template.validate", "notification.preview.render"}, true

	case method == http.MethodPost && subpath == "/templates/batch-actions":
		return []string{"notification.template.batch", "notification.template.write"}, true

	case matchSubpathPrefix(subpath, "/templates"):
		if isGet {
			return []string{"notification.template.read"}, true
		}
		if strings.HasSuffix(subpath, "/publish") {
			return []string{"notification.template.publish"}, true
		}
		if method == http.MethodPost && strings.Contains(subpath, "/releases") {
			if strings.HasSuffix(subpath, "/rollback") {
				return []string{"notification.release.rollback", "notification.release.activate"}, true
			}
			return []string{"notification.release.activate", "notification.release.rollback"}, true
		}
		return []string{"notification.template.write"}, true

	case isGet && matchSubpathPrefix(subpath, "/variable-mapping"):
		return []string{"notification.variable_mapping.read"}, true

	case matchSubpathPrefix(subpath, "/trigger-rules"):
		if isGet {
			return []string{"notification.rule.read"}, true
		}
		if strings.HasSuffix(subpath, "/activate") {
			return []string{"notification.rule.activate"}, true
		}
		return []string{"notification.rule.write"}, true

	case matchSubpathPrefix(subpath, "/applications"):
		if isGet {
			return []string{"notification.application.read"}, true
		}
		if strings.Contains(subpath, "/keys/") {
			return []string{"notification.application.rotate_key"}, true
		}
		return []string{"notification.application.write"}, true

	case matchSubpathPrefix(subpath, "/providers"):
		if isGet {
			return []string{"notification.provider.read"}, true
		}
		switch {
		case strings.HasSuffix(subpath, "/circuit-breaker/override"):
			return []string{"notification.breaker.override"}, true
		case strings.HasSuffix(subpath, "/circuit-breaker/probe"):
			return []string{"notification.breaker.probe"}, true
		case strings.HasSuffix(subpath, "/test-connection"), strings.HasSuffix(subpath, "/simulate-failure"):
			return []string{"notification.provider.probe"}, true
		case strings.HasSuffix(subpath, "/activate"):
			return []string{"notification.provider.activate"}, true
		default:
			return []string{"notification.provider.write"}, true
		}

	case matchSubpathPrefix(subpath, "/routing-policies"):
		if isGet {
			return []string{"notification.routing.read"}, true
		}
		return []string{"notification.routing.write"}, true

	case matchSubpathPrefix(subpath, "/deliveries"), matchSubpathPrefix(subpath, "/requests"):
		if isGet {
			return []string{"notification.delivery.read"}, true
		}
		if strings.HasSuffix(subpath, "/resend") {
			return []string{"notification.delivery.resend"}, true
		}
		return nil, false

	case method == http.MethodPost && subpath == "/metrics/query":
		return []string{"notification.metrics.read"}, true

	case isGet && subpath == "/audit":
		return []string{"notification.audit.read"}, true

	default:
		return nil, false
	}
}

func matchSubpathPrefix(subpath, prefix string) bool {
	return subpath == prefix || strings.HasPrefix(subpath, prefix+"/")
}

func hasAnyCapability(granted, required []string) bool {
	for _, capability := range required {
		if hasCapability(granted, capability) {
			return true
		}
	}
	return false
}
