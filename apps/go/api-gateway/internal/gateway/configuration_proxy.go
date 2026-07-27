package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type configurationProxy struct {
	client           httpDoer
	baseURL          *url.URL
	maxRequestBytes  int64
	maxResponseBytes int64
}

func newConfigurationProxy(
	client httpDoer,
	rawBaseURL string,
	maxRequestBytes int64,
	maxResponseBytes int64,
) (*configurationProxy, error) {
	if client == nil || maxRequestBytes <= 0 || maxResponseBytes <= 0 {
		return nil, errors.New("configuration proxy bounds are invalid")
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" ||
		baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("configuration upstream URL must be an HTTPS origin")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	return &configurationProxy{
		client:           client,
		baseURL:          baseURL,
		maxRequestBytes:  maxRequestBytes,
		maxResponseBytes: maxResponseBytes,
	}, nil
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

	var body io.Reader
	if r.Body != nil {
		body = http.MaxBytesReader(w, r.Body, h.configurationProxy.maxRequestBytes)
	}
	upstreamRequest, err := http.NewRequestWithContext(
		upstreamContext(r, scope),
		r.Method,
		target.String(),
		body,
	)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	copyConfigurationHeaders(upstreamRequest.Header, r.Header)

	response, err := h.configurationProxy.client.Do(upstreamRequest)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(
		response.Body,
		h.configurationProxy.maxResponseBytes+1,
	))
	if err != nil || int64(len(responseBody)) > h.configurationProxy.maxResponseBytes {
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
		"X-BPMP-Tenant-ID",
		"X-Correlation-ID",
		"X-Command-ID",
		"Idempotency-Key",
		"traceparent",
		"tracestate",
	} {
		if value := source.Get(name); value != "" {
			target.Set(name, value)
		}
	}
}
