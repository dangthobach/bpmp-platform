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

type httpsOriginProxy struct {
	client  httpDoer
	baseURL *url.URL
}

func newHTTPSOriginProxy(client httpDoer, rawURL, name string) (*httpsOriginProxy, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil || client == nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.Path != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New(name + " upstream must be an HTTPS origin")
	}
	return &httpsOriginProxy{client: client, baseURL: baseURL}, nil
}

func WithDocumentAdapterProxy(client httpDoer, rawURL string) HandlerOption {
	return func(h *Handler) error {
		p, err := newHTTPSOriginProxy(client, rawURL, "Document Adapter")
		if err != nil {
			return err
		}
		h.documentProxy = p
		return nil
	}
}

func WithIntegrationHubProxy(client httpDoer, rawURL string) HandlerOption {
	return func(h *Handler) error {
		p, err := newHTTPSOriginProxy(client, rawURL, "Integration Hub")
		if err != nil {
			return err
		}
		h.integrationProxy = p
		return nil
	}
}

func WithMasterDataProxy(client httpDoer, rawURL string) HandlerOption {
	return func(h *Handler) error {
		p, err := newHTTPSOriginProxy(client, rawURL, "Master Data Service")
		if err != nil {
			return err
		}
		h.masterDataProxy = p
		return nil
	}
}

func WithAuditProjectionProxy(client httpDoer, rawURL string) HandlerOption {
	return func(h *Handler) error {
		p, err := newHTTPSOriginProxy(client, rawURL, "Audit Projection Service")
		if err != nil {
			return err
		}
		h.auditProjectionProxy = p
		return nil
	}
}

func (h *Handler) proxyDocument(w http.ResponseWriter, r *http.Request) {
	caps, ok := documentRequiredCapabilities(r.Method, r.URL.Path)
	if !ok {
		setCorrelationHeader(w, r)
		writeError(w, errForbidden)
		return
	}
	h.proxyODSService(w, r, h.documentProxy, "/v1/documents", caps, true)
}

func (h *Handler) proxyIntegration(w http.ResponseWriter, r *http.Request) {
	caps, ok := integrationRequiredCapabilities(r.Method, r.URL.Path)
	if !ok {
		setCorrelationHeader(w, r)
		writeError(w, errForbidden)
		return
	}
	prefix := "/v1/admin/endpoints"
	if strings.HasPrefix(r.URL.Path, "/v1/admin/integration") {
		prefix = "/v1/admin/integration"
	}
	h.proxyODSService(w, r, h.integrationProxy, prefix, caps, true)
}

func (h *Handler) proxyMasterData(w http.ResponseWriter, r *http.Request) {
	caps, ok := masterDataRequiredCapabilities(r.Method, r.URL.Path)
	if !ok {
		setCorrelationHeader(w, r)
		writeError(w, errForbidden)
		return
	}
	requireIdem := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/mdm/publications")
	h.proxyODSService(w, r, h.masterDataProxy, "/v1/mdm", caps, requireIdem)
}

func (h *Handler) proxyAuditProjection(w http.ResponseWriter, r *http.Request) {
	caps, ok := auditProjectionRequiredCapabilities(r.Method, r.URL.Path)
	if !ok {
		setCorrelationHeader(w, r)
		writeError(w, errForbidden)
		return
	}
	h.proxyODSService(w, r, h.auditProjectionProxy, "/v1/audit", caps, false)
}

func (h *Handler) proxyODSService(
	w http.ResponseWriter,
	r *http.Request,
	upstream *httpsOriginProxy,
	basePrefix string,
	requiredCaps []string,
	requireMutationKeys bool,
) {
	setCorrelationHeader(w, r)
	if upstream == nil {
		writeError(w, errUpstream)
		return
	}
	if !allowedODSMethod(r.Method) || !validPrefixedPath(r.URL, basePrefix) {
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
	if !hasAnyCapability(scope.capabilities, requiredCaps) {
		writeError(w, errForbidden)
		return
	}
	if isMutation && requireMutationKeys && (!validID(scope.commandID) || !validID(scope.idempotencyKey)) {
		writeError(w, errInvalid)
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
	target := *upstream.baseURL
	target.Path, target.RawPath, target.RawQuery = r.URL.Path, "", r.URL.RawQuery
	request, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-BPMP-Tenant-ID", scope.tenantID)
	request.Header.Set("X-BPMP-Actor-ID", scope.actorID)
	request.Header.Set("X-BPMP-Capabilities", strings.Join(scope.capabilities, ","))
	request.Header.Set("X-BPMP-Signed-Actor-Context", base64.StdEncoding.EncodeToString(scope.signedActorProof))
	request.Header.Set("X-Correlation-ID", scope.correlationID)
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	if isMutation {
		request.Header.Set("Content-Type", "application/json")
		if scope.idempotencyKey != "" {
			request.Header.Set("Idempotency-Key", scope.idempotencyKey)
		}
		if scope.commandID != "" {
			request.Header.Set("X-Command-ID", scope.commandID)
		}
	}
	if scope.traceParent != "" {
		request.Header.Set("traceparent", scope.traceParent)
	}
	if scope.traceState != "" {
		request.Header.Set("tracestate", scope.traceState)
	}
	requestmeta.InjectHTTP(ctx, request.Header)

	response, err := upstream.client.Do(request)
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

func allowedODSMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validPrefixedPath(parsed *url.URL, prefix string) bool {
	if parsed == nil {
		return false
	}
	p := parsed.Path
	if p != prefix && !strings.HasPrefix(p, prefix+"/") {
		return false
	}
	raw := strings.ToLower(parsed.RawPath)
	if strings.Contains(p, "..") || strings.Contains(raw, "..") || strings.Contains(raw, "%2e") {
		return false
	}
	return true
}

func documentRequiredCapabilities(method, path string) ([]string, bool) {
	if strings.HasPrefix(path, "/v1/documents/scan-callbacks/") {
		return nil, false // scanner callbacks use dedicated provider ingress, never browser session
	}
	if method == http.MethodGet {
		return []string{"document.read"}, true
	}
	if strings.HasSuffix(path, "/download-url") && method == http.MethodPost {
		return []string{"document.download", "document.read"}, true
	}
	if strings.HasSuffix(path, "/legal-hold") && method == http.MethodPost {
		return []string{"document.hold"}, true
	}
	return []string{"document.write"}, true
}

func integrationRequiredCapabilities(method, path string) ([]string, bool) {
	if strings.HasPrefix(path, "/v1/admin/endpoints") {
		if method == http.MethodGet {
			return []string{"integration.endpoint.read"}, true
		}
		return []string{"integration.endpoint.write"}, true
	}
	if strings.HasPrefix(path, "/v1/admin/integration/deliveries") {
		if method == http.MethodGet {
			return []string{"integration.delivery.read"}, true
		}
		if strings.HasSuffix(path, "/retry") || strings.HasSuffix(path, "/compensate") {
			return []string{"integration.delivery.retry"}, true
		}
		return []string{"integration.delivery.write", "integration.endpoint.write"}, true
	}
	return nil, false
}

func masterDataRequiredCapabilities(method, path string) ([]string, bool) {
	if method == http.MethodGet || (method == http.MethodPost && path == "/v1/mdm/resolve-batch") {
		return []string{"mdm.read"}, true
	}
	if method == http.MethodPost && path == "/v1/mdm/consumers/ack" {
		return []string{"mdm.ack", "mdm.read"}, true
	}
	if method == http.MethodPost && strings.HasPrefix(path, "/v1/mdm/publications") {
		return []string{"mdm.publish"}, true
	}
	return nil, false
}

func auditProjectionRequiredCapabilities(method, path string) ([]string, bool) {
	if method == http.MethodGet && path == "/v1/audit/entries" {
		return []string{"audit.projection.read", "audit.read"}, true
	}
	if method == http.MethodPost && path == "/v1/audit/rebuild" {
		return []string{"audit.projection.rebuild"}, true
	}
	return nil, false
}
