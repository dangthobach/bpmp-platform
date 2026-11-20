package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type issuedActorContext struct {
	proof        []byte
	capabilities []string
}

type actorContextResponse struct {
	TenantID           string   `json:"tenant_id"`
	ActorID            string   `json:"actor_id"`
	AudienceWorkloadID string   `json:"audience_workload_id"`
	CommandID          string   `json:"command_id"`
	IssuedAtEpochMS    uint64   `json:"issued_at_epoch_ms"`
	ExpiresAtEpochMS   uint64   `json:"expires_at_epoch_ms"`
	Roles              []string `json:"roles"`
	SignedActorContext string   `json:"signed_actor_context"`
	Capabilities       []string `json:"capabilities"`
}

type authorizationProxy struct {
	client   httpDoer
	baseURL  *url.URL
	audience string
}

const maxActorContextResponseBytes = 1 << 20

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

func (p *authorizationProxy) issueActorContext(
	parent context.Context,
	signer *workloadSigner,
	tenantID, actorID, audience, commandID string,
	policy RuntimePolicy,
	now time.Time,
) (issuedActorContext, error) {
	if p == nil || signer == nil || policy.UpstreamTimeout <= 0 ||
		policy.MaxRequestBodyBytes <= 0 || policy.MaxUpstreamResponseBytes <= 0 {
		return issuedActorContext{}, errUpstream
	}
	serviceToken, err := signer.signAuthzDelegation(
		tenantID, actorID, "identity.actor-context.issue", p.audience, now,
	)
	if err != nil {
		return issuedActorContext{}, errUpstream
	}
	body, err := json.Marshal(struct {
		TenantID           string `json:"tenant_id"`
		AudienceWorkloadID string `json:"audience_workload_id"`
		CommandID          string `json:"command_id"`
	}{tenantID, audience, commandID})
	if err != nil || int64(len(body)) > policy.MaxRequestBodyBytes {
		return issuedActorContext{}, errInvalid
	}
	target := *p.baseURL
	target.Path += "/authz/v1/actor-context"
	target.RawPath = ""
	ctx, cancel := context.WithTimeout(parent, policy.UpstreamTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return issuedActorContext{}, errUpstream
	}
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	requestmeta.InjectHTTP(ctx, request.Header)
	response, err := p.client.Do(request)
	if err != nil {
		return issuedActorContext{}, errUpstream
	}
	defer response.Body.Close()
	maxResponseBytes := policy.MaxUpstreamResponseBytes
	if maxResponseBytes > maxActorContextResponseBytes {
		maxResponseBytes = maxActorContextResponseBytes
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || int64(len(responseBody)) > maxResponseBytes {
		return issuedActorContext{}, errUpstream
	}
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			return issuedActorContext{}, errUnauthorized
		case http.StatusForbidden, http.StatusNotFound:
			return issuedActorContext{}, errForbidden
		case http.StatusTooManyRequests:
			return issuedActorContext{}, errRateLimited
		default:
			return issuedActorContext{}, errUpstream
		}
	}
	var decoded actorContextResponse
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&decoded); err != nil {
		return issuedActorContext{}, errUpstream
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return issuedActorContext{}, errUpstream
	}
	if decoded.TenantID != tenantID || decoded.ActorID != actorID ||
		decoded.AudienceWorkloadID != audience || decoded.CommandID != commandID ||
		len(decoded.Roles) > 256 || len(decoded.Capabilities) > 512 ||
		len(decoded.SignedActorContext) == 0 ||
		!validActorContextClaims(decoded, now) {
		return issuedActorContext{}, errUpstream
	}
	proof, err := base64.StdEncoding.Strict().DecodeString(decoded.SignedActorContext)
	if err != nil || len(proof) == 0 || int64(len(proof)) > maxResponseBytes {
		return issuedActorContext{}, errUpstream
	}
	return issuedActorContext{proof: proof, capabilities: append([]string(nil), decoded.Capabilities...)}, nil
}

func validActorContextClaims(response actorContextResponse, now time.Time) bool {
	issuedAt := response.IssuedAtEpochMS
	expiresAt := response.ExpiresAtEpochMS
	nowUnixMS := now.UTC().UnixMilli()
	if nowUnixMS <= 0 {
		return false
	}
	nowMS := uint64(nowUnixMS)
	if issuedAt == 0 || expiresAt <= issuedAt ||
		(issuedAt > nowMS && issuedAt-nowMS > 30_000) ||
		expiresAt <= nowMS || expiresAt-issuedAt > 120_000 {
		return false
	}
	seenRoles := make(map[string]struct{}, len(response.Roles))
	for _, role := range response.Roles {
		if !validAuthorizationCode(role) {
			return false
		}
		if _, duplicate := seenRoles[role]; duplicate {
			return false
		}
		seenRoles[role] = struct{}{}
	}
	seenCapabilities := make(map[string]struct{}, len(response.Capabilities))
	for _, capability := range response.Capabilities {
		if !validAuthorizationCode(capability) {
			return false
		}
		if _, duplicate := seenCapabilities[capability]; duplicate {
			return false
		}
		seenCapabilities[capability] = struct{}{}
	}
	return true
}

func validAuthorizationCode(value string) bool {
	if len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_.:", character)) {
			return false
		}
	}
	return true
}

func actorContextAudience(path, gatewayWorkloadID string) (string, bool) {
	switch {
	case strings.HasPrefix(path, "/v1/work-items"),
		strings.HasPrefix(path, "/v1/cases/"),
		path == "/v1/audit-records",
		path == "/v1/assignment-policies":
		return "human-runtime", true
	case strings.HasPrefix(path, "/v1/authoring/"):
		return "authoring-service", true
	case strings.HasPrefix(path, "/v1/process-instances/"),
		strings.HasPrefix(path, "/v1/process-events/"),
		strings.HasPrefix(path, "/v1/saved-queries"),
		strings.HasPrefix(path, "/v1/saved-dashboards"),
		strings.HasPrefix(path, "/v1/query-exports"),
		strings.HasPrefix(path, "/v1/ops/"):
		return "projection-service", true
	case strings.HasPrefix(path, "/v1/workflows/"),
		strings.HasPrefix(path, "/v1/access/"),
		strings.HasPrefix(path, "/v1/configuration/"):
		return gatewayWorkloadID, gatewayWorkloadID != ""
	default:
		return "", false
	}
}

func (h *Handler) authorizationCatalog(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	scope, err := h.authenticateQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	requiredCapability := "authz.catalog.read"
	if r.Method != http.MethodGet {
		requiredCapability = "authz.catalog.write"
		if strings.HasSuffix(r.URL.Path, "/publish") {
			requiredCapability = "authz.catalog.publish"
		}
	}
	if r.URL.Path == "/v1/access/users" {
		requiredCapability = "identity.user.read"
	} else if r.URL.Path == "/v1/access/identity/profiles" {
		requiredCapability = "identity.profile.manage"
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
	if idempotencyKey := r.Header.Get("Idempotency-Key"); idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if correlationID := r.Header.Get("X-Correlation-ID"); correlationID != "" {
		request.Header.Set("X-Correlation-ID", correlationID)
	}
	if r.Method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
	}
	requestmeta.InjectHTTP(ctx, request.Header)
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
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
