package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type configurationProxy struct {
	client  httpDoer
	baseURL *url.URL
}

func newConfigurationProxy(
	client httpDoer,
	rawBaseURL string,
) (*configurationProxy, error) {
	if client == nil {
		return nil, errors.New("configuration proxy client is required")
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" ||
		baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("configuration upstream URL must be an HTTPS origin")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	return &configurationProxy{client: client, baseURL: baseURL}, nil
}

func (h *Handler) configuration(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	commandRequired := r.Method != http.MethodGet
	scope, err := h.authenticateRequest(r, commandRequired)
	if err != nil {
		writeError(w, err)
		return
	}

	target := *h.configurationProxy.baseURL
	target.Path += r.URL.Path
	target.RawPath = ""
	target.RawQuery = r.URL.RawQuery

	maxRequestBytes := scope.runtimePolicy.MaxRequestBodyBytes
	maxResponseBytes := scope.runtimePolicy.MaxUpstreamResponseBytes
	var requestBody []byte
	if r.Body != nil {
		requestBody, err = io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil || int64(len(requestBody)) > maxRequestBytes {
			writeError(w, errInvalid)
			return
		}
	}
	response, err := invokeUpstream(
		h,
		upstreamContext(r, scope),
		scope,
		configurationDependency,
		func(ctx context.Context) (*http.Response, error) {
			upstreamRequest, requestErr := http.NewRequestWithContext(
				ctx,
				r.Method,
				target.String(),
				bytes.NewReader(requestBody),
			)
			if requestErr != nil {
				return nil, requestErr
			}
			copyConfigurationHeaders(upstreamRequest.Header, r.Header)
			requestmeta.InjectHTTP(ctx, upstreamRequest.Header)
			return h.configurationProxy.client.Do(upstreamRequest)
		},
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxResponseBytes+1,
	))
	if err != nil || int64(len(responseBody)) > maxResponseBytes {
		writeError(w, errUpstream)
		return
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	if _, err = w.Write(responseBody); err != nil {
		return
	}
}

func copyConfigurationHeaders(target, source http.Header) {
	for _, name := range []string{
		"Accept",
		"Authorization",
		"Content-Type",
		"Idempotency-Key",
	} {
		if value := source.Get(name); value != "" {
			target.Set(name, value)
		}
	}
}
