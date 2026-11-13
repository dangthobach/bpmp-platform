package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type authorizationProxy struct {
	client   httpDoer
	baseURL  *url.URL
	audience string
}

func WithAuthorizationAdmin(client httpDoer, rawURL, audience string) HandlerOption {
	return func(handler *Handler) error {
		proxy, err := newAuthorizationProxy(client, rawURL, audience)
		if err != nil {
			return err
		}
		handler.authorizationProxy = proxy
		return nil
	}
}

func newAuthorizationProxy(client httpDoer, rawURL, audience string) (*authorizationProxy, error) {
	if client == nil || strings.TrimSpace(audience) == "" {
		return nil, errors.New("authorization proxy dependencies are incomplete")
	}
	baseURL, err := url.Parse(rawURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("authorization upstream URL must be an HTTPS origin")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	return &authorizationProxy{client: client, baseURL: baseURL, audience: audience}, nil
}

func (h *Handler) authorizationCatalog(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	requiredCapability := "authz.catalog.read"
	if r.URL.Path == "/v1/access/users" {
		requiredCapability = "identity.user.read"
	}
	if r.Method != http.MethodGet {
		requiredCapability = "authz.catalog.write"
		if strings.HasSuffix(r.URL.Path, "/publish") {
			requiredCapability = "authz.catalog.publish"
		}
	}
	if !hasCapability(scope.capabilities, requiredCapability) {
		writeError(w, errForbidden)
		return
	}
	if h.authorizationProxy == nil {
		writeError(w, errUpstream)
		return
	}

	token, err := h.workload.signAuthzDelegation(
		scope.tenantID, scope.actorID, requiredCapability,
		h.authorizationProxy.audience, scope.occurredAt,
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	target := *h.authorizationProxy.baseURL
	internalPath := strings.TrimPrefix(r.URL.Path, "/v1/access")
	target.Path += "/admin/v1" + internalPath
	target.RawPath = ""
	target.RawQuery = r.URL.Query().Encode()
	query := target.Query()
	query.Set("tenant_id", scope.tenantID)
	target.RawQuery = query.Encode()

	maxRequestBytes := scope.runtimePolicy.MaxRequestBodyBytes
	maxResponseBytes := scope.runtimePolicy.MaxUpstreamResponseBytes
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil || int64(len(body)) > maxRequestBytes {
			writeError(w, errInvalid)
			return
		}
		if r.Method != http.MethodGet && len(body) > 0 {
			var value map[string]json.RawMessage
			if json.Unmarshal(body, &value) != nil {
				writeError(w, errInvalid)
				return
			}
			tenantJSON, _ := json.Marshal(scope.tenantID)
			value["tenant_id"] = tenantJSON
			body, err = json.Marshal(value)
			if err != nil || int64(len(body)) > maxRequestBytes {
				writeError(w, errInvalid)
				return
			}
		}
	}
	timeout := scope.runtimePolicy.UpstreamTimeout
	if timeout <= 0 {
		writeError(w, errUpstream)
		return
	}
	ctx, cancel := context.WithTimeout(upstreamContext(r, scope), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", r.Header.Get("Accept"))
	if r.Method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
	}
	requestmeta.InjectHTTP(ctx, request.Header)
	response, err := h.authorizationProxy.client.Do(request)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || int64(len(responseBody)) > maxResponseBytes {
		writeError(w, errUpstream)
		return
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func hasCapability(capabilities []string, expected string) bool {
	for _, capability := range capabilities {
		if capability == expected {
			return true
		}
	}
	return false
}
