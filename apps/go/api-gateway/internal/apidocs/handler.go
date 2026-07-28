package apidocs

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

//go:embed openapi.json
var assets embed.FS

type Config struct {
	OpenAPIPath     string
	ReferencePath   string
	ScalarScriptURL string
}

type Handler struct {
	config  Config
	spec    []byte
	etag    string
	page    []byte
	cspHost string
}

func New(config Config) (*Handler, error) {
	scriptURL, err := url.Parse(config.ScalarScriptURL)
	if err != nil || scriptURL.Scheme != "https" || scriptURL.Host == "" {
		return nil, errors.New("invalid API reference script URL")
	}
	spec, err := assets.ReadFile("openapi.json")
	if err != nil {
		return nil, err
	}
	pageTemplate, err := template.New("reference").Parse(referencePage)
	if err != nil {
		return nil, err
	}
	var pageBuffer bytes.Buffer
	if err = pageTemplate.Execute(&pageBuffer, map[string]string{
		"OpenAPIPath": config.OpenAPIPath,
		"ScriptURL":   config.ScalarScriptURL,
	}); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(spec)
	return &Handler{
		config:  config,
		spec:    spec,
		etag:    `"` + hex.EncodeToString(digest[:]) + `"`,
		page:    pageBuffer.Bytes(),
		cspHost: scriptURL.Scheme + "://" + scriptURL.Host,
	}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+h.config.OpenAPIPath, h.openAPI)
	mux.HandleFunc("HEAD "+h.config.OpenAPIPath, h.openAPI)
	mux.HandleFunc("GET "+h.config.ReferencePath, h.reference)
	mux.HandleFunc("HEAD "+h.config.ReferencePath, h.reference)
}

func (h *Handler) openAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.1")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", h.etag)
	w.Header().Set("Content-Length", strconv.Itoa(len(h.spec)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == h.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(h.spec); err != nil {
		slog.Warn("write OpenAPI contract", "error", err)
		return
	}
}

func (h *Handler) reference(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(h.page)))
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src "+h.cspHost+
			"; style-src 'unsafe-inline'; img-src data: https:; font-src https: data:; connect-src 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(h.page); err != nil {
		slog.Warn("write API reference", "error", err)
		return
	}
}

const referencePage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>BPMP Platform API</title>
</head>
<body>
  <script id="api-reference" data-url="{{.OpenAPIPath}}"></script>
  <script src="{{.ScriptURL}}"></script>
</body>
</html>`
