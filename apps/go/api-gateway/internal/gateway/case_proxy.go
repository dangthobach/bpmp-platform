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

const casePath = "/v1/customer-cases"

type caseProxy struct {
	client  httpDoer
	baseURL *url.URL
}

func WithCaseServiceProxy(client httpDoer, rawURL string) HandlerOption {
	return func(handler *Handler) error {
		proxy, err := newCaseProxy(client, rawURL)
		if err != nil {
			return err
		}
		handler.caseProxy = proxy
		return nil
	}
}

func newCaseProxy(client httpDoer, rawURL string) (*caseProxy, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil || client == nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.Path != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("Case Service upstream must be an HTTPS origin")
	}
	return &caseProxy{client: client, baseURL: baseURL}, nil
}

func (h *Handler) proxyCase(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticate(r)
	if r.Method == http.MethodGet {
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
	capability := "case.read"
	if r.Method == http.MethodPost {
		capability = "case.create"
	}
	if !hasCapability(scope.capabilities, capability) {
		writeError(w, errForbidden)
		return
	}
	if r.Method == http.MethodPost && (!validID(scope.commandID) || !validID(scope.idempotencyKey)) {
		writeError(w, errInvalid)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, errInvalid)
		return
	}
	if r.Method == http.MethodPost && (r.URL.Path != casePath || r.ContentLength > scope.runtimePolicy.MaxRequestBodyBytes) {
		writeError(w, errInvalid)
		return
	}
	if len(r.URL.Path) > len(casePath) && !strings.HasPrefix(r.URL.Path, casePath+"/") {
		writeError(w, errInvalid)
		return
	}
	if len(r.URL.Path) < len(casePath) {
		writeError(w, errInvalid)
		return
	}
	if len(r.URL.Path) > len(casePath) && strings.Contains(r.URL.Path[len(casePath)+1:], "..") {
		writeError(w, errInvalid)
		return
	}
	var body io.Reader
	if r.Method == http.MethodPost {
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, scope.runtimePolicy.MaxRequestBodyBytes))
		if readErr != nil {
			writeError(w, errInvalid)
			return
		}
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(r.Context(), scope.runtimePolicy.UpstreamTimeout)
	defer cancel()
	target := *h.caseProxy.baseURL
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
	if r.Method == http.MethodPost {
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
	response, err := h.caseProxy.client.Do(request)
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
	for _, key := range []string{"Content-Type", "ETag", "Cache-Control"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}
