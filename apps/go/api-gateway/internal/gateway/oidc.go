package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

const (
	oidcStateCookieName = "__Host-bpmp_oidc_state"
	csrfCookieName      = "__Host-bpmp_csrf"
	oidcMaxResponseSize = 1 << 20
	oidcMaxCodeBytes    = 4096
	oidcLoginLimit      = 10
	oidcLoginWindow     = time.Minute
)

func WithOIDCLogin(
	value config.OIDC,
	client httpDoer,
	transactions ports.OIDCTransactionStore,
	issuerJWKS ...[]byte,
) HandlerOption {
	return func(handler *Handler) error {
		if !value.Enabled || client == nil || transactions == nil ||
			handler.sessionStore == nil || handler.authorizationProxy == nil || len(issuerJWKS) > 1 {
			return errors.New("OIDC login dependencies are incomplete")
		}
		if len(issuerJWKS) == 1 {
			verified, err := newOIDCVerifier(value, issuerJWKS[0])
			if err != nil {
				return errors.New("OIDC issuer JWKS is invalid")
			}
			handler.oidcVerifier = verified
		}
		handler.oidcConfig = value
		handler.oidcClient = client
		handler.oidcTransactions = transactions
		return nil
	}
}

func (h *Handler) startOIDCLogin(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	sessionTokens := h.browserSessionTokens(r)
	if len(sessionTokens) > 1 {
		writeError(w, errUnauthorized)
		return
	}
	if len(sessionTokens) == 1 {
		token := sessionTokens[0]
		if token == "" || len(token) > h.sessionConfig.MaxTokenBytes {
			clearSessionCookies(w, h.sessionConfig.CookieName)
		} else {
			session, sessionErr := h.sessionStore.Get(r.Context(), token)
			if sessionErr != nil && !errors.Is(sessionErr, ports.ErrBrowserSessionNotFound) {
				writeError(w, errUpstream)
				return
			}
			if sessionErr == nil && validID(session.TenantID) && validID(session.ActorID) &&
				len(session.CSRFTokenSHA256) == sha256.Size && session.ExpiresAt.After(h.now().UTC()) {
				requestmeta.WriteProblem(w, r, http.StatusConflict, "active_session_exists", "Active session exists", "End the current session before starting a new OIDC login", false)
				return
			}
			clearSessionCookies(w, h.sessionConfig.CookieName)
		}
	}
	if r.URL.RawQuery != "" {
		query := r.URL.Query()
		for key, values := range query {
			if (key != "tenant_id" && key != "return_to") || len(values) != 1 {
				requestmeta.WriteProblem(w, r, http.StatusBadRequest, "invalid_oidc_request", "Invalid login request", "Only tenant_id and return_to are supported", false)
				return
			}
		}
	}
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID != "" && !validID(tenantID) {
		requestmeta.WriteProblem(w, r, http.StatusBadRequest, "invalid_oidc_request", "Invalid login request", "tenant_id is invalid", false)
		return
	}
	returnTo := r.URL.Query().Get("return_to")
	if returnTo != "" && !h.allowedOIDCReturnTarget(returnTo) {
		requestmeta.WriteProblem(w, r, http.StatusBadRequest, "invalid_oidc_request", "Invalid login request", "return_to origin is not allowed", false)
		return
	}
	clientAddress := r.RemoteAddr
	if host, _, splitErr := net.SplitHostPort(clientAddress); splitErr == nil {
		clientAddress = host
	}
	allowed, err := false, error(nil)
	if limiter, dynamic := h.limiter.(dynamicRateLimiter); dynamic {
		allowed, err = limiter.AllowPolicy(r.Context(), "oidc:"+clientAddress, oidcLoginLimit, oidcLoginWindow)
	} else {
		allowed, err = h.limiter.Allow(r.Context(), "oidc:"+clientAddress)
	}
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	if !allowed {
		writeError(w, errRateLimited)
		return
	}
	state, err := randomOIDCToken(32)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	nonce, err := randomOIDCToken(32)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	verifier, err := randomOIDCToken(32)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	now := h.now().UTC()
	expiresAt := now.Add(time.Duration(h.oidcConfig.TransactionTTLMS) * time.Millisecond)
	err = h.oidcTransactions.Save(r.Context(), state, ports.OIDCLoginTransaction{
		CodeVerifier: verifier, Nonce: nonce, TenantID: tenantID, ReturnTo: returnTo, ExpiresAt: expiresAt,
	})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	challenge := sha256.Sum256([]byte(verifier))
	endpoint, err := url.Parse(h.oidcConfig.AuthorizationEndpoint)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", h.oidcConfig.ClientID)
	query.Set("redirect_uri", h.oidcConfig.RedirectURI)
	query.Set("scope", strings.Join(h.oidcConfig.Scopes, " "))
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	endpoint.RawQuery = query.Encode()
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: state, Path: "/", Expires: expiresAt,
		MaxAge: int(h.oidcConfig.TransactionTTLMS / 1000), HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, endpoint.String(), http.StatusFound)
}

func (h *Handler) completeOIDCLogin(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if tokens := h.browserSessionTokens(r); len(tokens) != 0 {
		requestmeta.WriteProblem(w, r, http.StatusConflict, "active_session_exists", "Active session exists", "End the current session before completing a new OIDC login", false)
		return
	}
	query := r.URL.Query()
	stateValues, codeValues := query["state"], query["code"]
	cookieState, cookieOK := singleCookie(r, oidcStateCookieName)
	if query.Get("error") != "" || len(stateValues) != 1 || len(codeValues) != 1 ||
		!cookieOK || len(codeValues[0]) == 0 || len(codeValues[0]) > oidcMaxCodeBytes ||
		len(stateValues[0]) == 0 || len(stateValues[0]) > 128 ||
		subtle.ConstantTimeCompare([]byte(stateValues[0]), []byte(cookieState)) != 1 {
		clearOIDCStateCookie(w)
		writeError(w, errUnauthorized)
		return
	}
	transaction, err := h.oidcTransactions.Consume(r.Context(), stateValues[0])
	clearOIDCStateCookie(w)
	if errors.Is(err, ports.ErrOIDCTransactionNotFound) {
		writeError(w, errUnauthorized)
		return
	}
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	now := h.now().UTC()
	token, err := h.exchangeOIDCCode(r.Context(), codeValues[0], transaction.CodeVerifier)
	if err != nil {
		writeError(w, errUnauthorized)
		return
	}
	idTokenVerifier := h.oidcVerifier
	if idTokenVerifier == nil {
		idTokenVerifier = h.verifier
	}
	subject, err := idTokenVerifier.verifyOIDCIDToken(
		token.IDToken, h.oidcConfig.Issuer, h.oidcConfig.ClientID,
		transaction.Nonce, now,
	)
	if err != nil {
		writeError(w, errUnauthorized)
		return
	}
	policy := RuntimePolicy{
		UpstreamTimeout:     time.Duration(h.oidcConfig.OperationTimeoutMS) * time.Millisecond,
		MaxRequestBodyBytes: 4_096, MaxUpstreamResponseBytes: oidcMaxResponseSize,
	}
	profiles, err := h.authorizationProxy.resolveOIDCSubject(
		r.Context(), h.workload, h.oidcConfig.Issuer, subject, policy, now,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	profile, err := chooseTenantProfile(profiles, transaction.TenantID)
	if err != nil {
		if errors.Is(err, errTenantSelectionRequired) {
			tenantIDs := make([]string, 0, len(profiles))
			for _, candidate := range profiles {
				tenantIDs = append(tenantIDs, candidate.TenantID)
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusConflict, map[string]any{
				"code": "tenant_selection_required", "tenant_ids": tenantIDs,
			})
			return
		}
		writeError(w, errForbidden)
		return
	}
	sessionToken, err := randomOIDCToken(32)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	csrfToken, err := randomOIDCToken(32)
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	expiresAt := now.Add(time.Duration(h.oidcConfig.SessionTTLMS) * time.Millisecond)
	csrfHash := sha256.Sum256([]byte(csrfToken))
	err = h.sessionStore.Create(r.Context(), sessionToken, ports.BrowserSession{
		TenantID: profile.TenantID, ActorID: profile.UserID,
		CSRFTokenSHA256: csrfHash[:], ExpiresAt: expiresAt,
	})
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: h.sessionConfig.CookieName, Value: sessionToken, Path: "/",
		Expires: expiresAt, MaxAge: int(h.oidcConfig.SessionTTLMS / 1000),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: csrfToken, Path: "/", Expires: expiresAt,
		MaxAge: int(h.oidcConfig.SessionTTLMS / 1000), HttpOnly: false, Secure: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	redirect := h.oidcConfig.FrontendURL
	if transaction.ReturnTo != "" {
		redirect = transaction.ReturnTo
	}
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (h *Handler) allowedOIDCReturnTarget(raw string) bool {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme == "" || target.Host == "" || target.User != nil || target.Fragment != "" || len(raw) > 2048 {
		return false
	}
	origin := target.Scheme + "://" + target.Host
	frontend, frontendErr := url.Parse(h.oidcConfig.FrontendURL)
	if frontendErr == nil && origin == frontend.Scheme+"://"+frontend.Host {
		return true
	}
	for _, allowed := range h.oidcConfig.AllowedReturnOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func (h *Handler) exchangeOIDCCode(ctx context.Context, code, verifier string) (oidcTokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.oidcConfig.OperationTimeoutMS)*time.Millisecond)
	defer cancel()
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {h.oidcConfig.ClientID},
		"redirect_uri": {h.oidcConfig.RedirectURI}, "code": {code}, "code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.oidcConfig.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	issuer, err := url.Parse(h.oidcConfig.Issuer)
	if err != nil {
		return oidcTokenResponse{}, errors.New("OIDC issuer is invalid")
	}
	request.Host = issuer.Host
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	requestmeta.InjectHTTP(ctx, request.Header)
	response, err := h.oidcClient.Do(request)
	if err != nil {
		return oidcTokenResponse{}, err
	}
	defer response.Body.Close()
	mediaType, _, mediaTypeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaTypeErr != nil || mediaType != "application/json" {
		return oidcTokenResponse{}, errors.New("OIDC token endpoint rejected the authorization code")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, oidcMaxResponseSize+1))
	if err != nil || len(body) > oidcMaxResponseSize {
		return oidcTokenResponse{}, errors.New("OIDC token response is outside the configured bound")
	}
	var token oidcTokenResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err = decoder.Decode(&token); err != nil {
		return oidcTokenResponse{}, err
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF || token.IDToken == "" ||
		len(token.IDToken) > h.verifier.maxTokenBytes() ||
		!strings.EqualFold(token.TokenType, "Bearer") {
		return oidcTokenResponse{}, errors.New("OIDC token response is malformed")
	}
	return token, nil
}

type oidcTokenResponse struct {
	TokenType string `json:"token_type"`
	IDToken   string `json:"id_token"`
}

var errTenantSelectionRequired = errors.New("tenant selection required")

func chooseTenantProfile(profiles []tenantAccessProfile, tenantID string) (tenantAccessProfile, error) {
	if len(profiles) == 0 {
		return tenantAccessProfile{}, errForbidden
	}
	if tenantID != "" {
		for _, profile := range profiles {
			if profile.TenantID == tenantID {
				return profile, nil
			}
		}
		return tenantAccessProfile{}, errForbidden
	}
	if len(profiles) != 1 {
		return tenantAccessProfile{}, errTenantSelectionRequired
	}
	return profiles[0], nil
}

func (h *Handler) logoutBrowserSession(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	tokens := h.browserSessionTokens(r)
	if len(tokens) > 1 {
		writeError(w, errUnauthorized)
		return
	}
	if len(tokens) == 0 {
		// A cross-site POST may omit the SameSite session cookie. Do not let
		// that unauthenticated response clear the browser's active cookies.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(tokens) == 1 {
		if len(tokens[0]) == 0 || len(tokens[0]) > h.sessionConfig.MaxTokenBytes {
			writeError(w, errUnauthorized)
			return
		}
		session, err := h.sessionStore.Get(r.Context(), tokens[0])
		switch {
		case errors.Is(err, ports.ErrBrowserSessionNotFound):
			// Deleting a missing opaque session is idempotent and safe.
		case err != nil:
			writeError(w, errUpstream)
			return
		case !h.validCSRFToken(r, session.CSRFTokenSHA256):
			writeError(w, errForbidden)
			return
		}
		if err := h.sessionStore.Delete(r.Context(), tokens[0]); err != nil {
			writeError(w, errUpstream)
			return
		}
	}
	clearSessionCookies(w, h.sessionConfig.CookieName)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// getBrowserSession exposes only the authenticated browser identity. The opaque
// session cookie and OIDC tokens remain server-side.
func (h *Handler) getBrowserSession(w http.ResponseWriter, r *http.Request) {
	setCorrelationHeader(w, r)
	w.Header().Set("Cache-Control", "no-store")
	tokens := h.browserSessionTokens(r)
	if len(tokens) != 1 || tokens[0] == "" || len(tokens[0]) > h.sessionConfig.MaxTokenBytes {
		if len(tokens) == 1 {
			clearSessionCookies(w, h.sessionConfig.CookieName)
		}
		writeError(w, errUnauthorized)
		return
	}
	session, err := h.sessionStore.Get(r.Context(), tokens[0])
	if errors.Is(err, ports.ErrBrowserSessionNotFound) {
		clearSessionCookies(w, h.sessionConfig.CookieName)
		writeError(w, errUnauthorized)
		return
	}
	if err != nil {
		writeError(w, errUpstream)
		return
	}
	if !validID(session.TenantID) || !validID(session.ActorID) ||
		len(session.CSRFTokenSHA256) != sha256.Size || !session.ExpiresAt.After(h.now().UTC()) {
		clearSessionCookies(w, h.sessionConfig.CookieName)
		writeError(w, errUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":  session.TenantID,
		"actor_id":   session.ActorID,
		"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func randomOIDCToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func singleCookie(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			count++
			value = cookie.Value
		}
	}
	return value, count == 1 && value != ""
}

func clearOIDCStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookies(w http.ResponseWriter, sessionCookieName string) {
	for _, cookie := range []struct {
		name     string
		httpOnly bool
		sameSite http.SameSite
	}{
		{name: sessionCookieName, httpOnly: true, sameSite: http.SameSiteLaxMode},
		{name: csrfCookieName, sameSite: http.SameSiteStrictMode},
		{name: oidcStateCookieName, httpOnly: true, sameSite: http.SameSiteLaxMode},
	} {
		http.SetCookie(w, &http.Cookie{
			Name: cookie.name, Value: "", Path: "/", MaxAge: -1,
			Expires: time.Unix(1, 0), HttpOnly: cookie.httpOnly,
			Secure: true, SameSite: cookie.sameSite,
		})
	}
}
