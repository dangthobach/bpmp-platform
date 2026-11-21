package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/adapter/redissession"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

type oidcTestSessionStore struct {
	mu       sync.Mutex
	sessions map[string]ports.BrowserSession
	created  string
	deleted  string
}

func (s *oidcTestSessionStore) Get(_ context.Context, token string) (ports.BrowserSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.sessions[token]
	if !ok {
		return ports.BrowserSession{}, ports.ErrBrowserSessionNotFound
	}
	return value, nil
}

func (s *oidcTestSessionStore) Create(_ context.Context, token string, value ports.BrowserSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[token]; exists {
		return errors.New("duplicate session token")
	}
	s.sessions[token] = value
	s.created = token
	return nil
}

func (s *oidcTestSessionStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	s.deleted = token
	return nil
}

type oidcTestTransactions struct {
	mu     sync.Mutex
	values map[string]ports.OIDCLoginTransaction
}

func (s *oidcTestTransactions) Save(_ context.Context, state string, value ports.OIDCLoginTransaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[state]; exists {
		return errors.New("duplicate state")
	}
	s.values[state] = value
	return nil
}

func (s *oidcTestTransactions) Consume(_ context.Context, state string) (ports.OIDCLoginTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[state]
	if !ok {
		return ports.OIDCLoginTransaction{}, ports.ErrOIDCTransactionNotFound
	}
	delete(s.values, state)
	return value, nil
}

func TestOIDCPKCECallbackCreatesTenantBoundOpaqueSession(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	state, nonce, verifier := "state-1", "nonce-1", "pkce-verifier-1"
	var tokenForm url.Values
	var resolution map[string]string
	profilesJSON := `{"profiles":[{"tenant_id":"00000000-0000-0000-0000-000000000010","user_id":"00000000-0000-0000-0000-000000000020","username":"jane","active":true,"version":1,"attributes_version":1,"roles":[]}]}`
	authzClient := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/admin/v1/identity/resolve-subject" {
			t.Errorf("unexpected Authz path: %s", request.URL.Path)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
			t.Error("subject lookup did not use a workload token")
		}
		if err := json.NewDecoder(request.Body).Decode(&resolution); err != nil {
			t.Errorf("decode subject resolution: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(profilesJSON)),
		}, nil
	})
	oidcClient := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		tokenForm, _ = url.ParseQuery(string(body))
		if request.URL.String() != "https://idp.example/token" || request.Method != http.MethodPost {
			t.Errorf("unexpected token request: %s %s", request.Method, request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"access_token":"not-persisted","token_type":"Bearer","id_token":"` + makeOIDCIDToken(t, privateKey, nonce, now) + `"}`)),
		}, nil
	})
	sessionStore := &oidcTestSessionStore{sessions: make(map[string]ports.BrowserSession)}
	transactions := &oidcTestTransactions{values: map[string]ports.OIDCLoginTransaction{
		state: {CodeVerifier: verifier, Nonce: nonce, ExpiresAt: now.Add(time.Minute)},
	}}
	handler := newOIDCTestHandler(t, now, publicKey, sessionStore, transactions, authzClient, oidcClient)
	request := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=authorization-code&state="+state, nil)
	request.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state})
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("OIDC callback failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("code") != "authorization-code" ||
		tokenForm.Get("code_verifier") != verifier || tokenForm.Get("client_id") != "bpmp-portal" {
		t.Fatalf("PKCE token exchange was not bound to stored transaction: %v", tokenForm)
	}
	if resolution["issuer"] != "https://idp.example/realm" || resolution["subject"] != "idp-subject-1" {
		t.Fatalf("Authz lookup did not use verified OIDC identity: %#v", resolution)
	}
	session, err := sessionStore.Get(context.Background(), sessionStore.created)
	if err != nil || session.TenantID != "00000000-0000-0000-0000-000000000010" ||
		session.ActorID != "00000000-0000-0000-0000-000000000020" ||
		len(session.SignedActorContext) != 0 || len(session.CSRFTokenSHA256) != sha256.Size {
		t.Fatalf("unexpected persisted session: %#v err=%v", session, err)
	}
	if strings.Contains(string(mustJSON(session)), "not-persisted") {
		t.Fatal("OIDC tokens were persisted in the browser session")
	}
	cookies := response.Result().Cookies()
	var hasSessionCookie, hasCSRFCookie bool
	for _, cookie := range cookies {
		if cookie.Name == "__Host-bpmp_session" {
			hasSessionCookie = cookie.HttpOnly && cookie.Secure && cookie.SameSite == http.SameSiteLaxMode
		}
		if cookie.Name == csrfCookieName {
			hasCSRFCookie = !cookie.HttpOnly && cookie.Secure && cookie.SameSite == http.SameSiteStrictMode
		}
	}
	if !hasSessionCookie || !hasCSRFCookie {
		t.Fatalf("session/CSRF cookie attributes are incorrect: %#v", cookies)
	}
}

func TestOIDCLoginCreatesBoundStateNonceAndS256Challenge(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := &oidcTestSessionStore{sessions: make(map[string]ports.BrowserSession)}
	transactions := &oidcTestTransactions{values: make(map[string]ports.OIDCLoginTransaction)}
	noCall := doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("login initiation unexpectedly called an upstream")
		return nil, errors.New("unexpected request")
	})
	handler := newOIDCTestHandler(t, now, publicKey, sessionStore, transactions, noCall, noCall)
	tenantID := "00000000-0000-0000-0000-000000000010"
	request := httptest.NewRequest(http.MethodGet, "/auth/oidc/login?tenant_id="+tenantID, nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("login initiation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	redirect, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := redirect.Query()
	if query.Get("response_type") != "code" || query.Get("client_id") != "bpmp-portal" ||
		query.Get("redirect_uri") != "https://portal.example/auth/oidc/callback" ||
		query.Get("code_challenge_method") != "S256" || query.Get("scope") != "openid profile" {
		t.Fatalf("OIDC authorization request is incomplete: %v", query)
	}
	transactions.mu.Lock()
	transaction, exists := transactions.values[query.Get("state")]
	transactions.mu.Unlock()
	if !exists || transaction.TenantID != tenantID || transaction.Nonce == "" || transaction.CodeVerifier == "" || query.Get("nonce") != transaction.Nonce {
		t.Fatalf("login transaction was not bound to state, nonce and tenant: %#v", transaction)
	}
	challenge := sha256.Sum256([]byte(transaction.CodeVerifier))
	if query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) {
		t.Fatal("PKCE challenge does not match the server-stored verifier")
	}
	var stateCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oidcStateCookieName {
			stateCookie = cookie
		}
	}
	if stateCookie == nil || stateCookie.Value != query.Get("state") || !stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("OIDC state cookie is insecure or not bound: %#v", stateCookie)
	}
}

func TestOIDCCallbackRejectsStateMismatchAndTenantAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name        string
		callback    string
		cookieState string
		profiles    string
		wantStatus  int
	}{
		{name: "state mismatch", callback: "state=other&code=auth-code", cookieState: "state-1", profiles: `{"profiles":[]}`, wantStatus: http.StatusUnauthorized},
		{name: "tenant selection required", callback: "state=state-1&code=auth-code", cookieState: "state-1", profiles: `{"profiles":[{"tenant_id":"tenant-a","user_id":"user-a","active":true,"roles":[]},{"tenant_id":"tenant-b","user_id":"user-b","active":true,"roles":[]}]}`, wantStatus: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
			publicKey, privateKey, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			sessionStore := &oidcTestSessionStore{sessions: make(map[string]ports.BrowserSession)}
			transactions := &oidcTestTransactions{values: map[string]ports.OIDCLoginTransaction{
				"state-1": {CodeVerifier: "verifier", Nonce: "nonce-1", ExpiresAt: now.Add(time.Minute)},
			}}
			authzClient := doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(test.profiles))}, nil
			})
			oidcClient := doerFunc(func(*http.Request) (*http.Response, error) {
				body := `{"token_type":"Bearer","id_token":"` + makeOIDCIDToken(t, privateKey, "nonce-1", now) + `"}`
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			handler := newOIDCTestHandler(t, now, publicKey, sessionStore, transactions, authzClient, oidcClient)
			request := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?"+test.callback, nil)
			request.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: test.cookieState})
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("unexpected callback status %d: %s", response.Code, response.Body.String())
			}
			if test.name == "tenant selection required" && sessionStore.created != "" {
				t.Fatal("ambiguous tenant profile created a session")
			}
		})
	}
}

func TestBrowserSessionLogoutRequiresCSRFAndDestroysServerRecord(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	csrf := "csrf-value"
	csrfHash := sha256.Sum256([]byte(csrf))
	store := &oidcTestSessionStore{sessions: map[string]ports.BrowserSession{
		"session-token": {TenantID: "tenant-a", ActorID: "user-a", CSRFTokenSHA256: csrfHash[:], ExpiresAt: now.Add(time.Minute)},
	}}
	handler := &Handler{sessionStore: store, sessionConfig: config.BrowserSession{CookieName: "__Host-bpmp_session", CSRFHeaderName: "X-CSRF-Token", MaxTokenBytes: 256}, now: func() time.Time { return now }}
	noCookie := httptest.NewRecorder()
	handler.Routes().ServeHTTP(noCookie, httptest.NewRequest(http.MethodPost, "/auth/session/logout", nil))
	if noCookie.Code != http.StatusNoContent || len(noCookie.Result().Cookies()) != 0 || store.deleted != "" {
		t.Fatalf("logout without session must not clear an active browser cookie: status=%d cookies=%v", noCookie.Code, noCookie.Result().Cookies())
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/session/logout", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-bpmp_session", Value: "session-token"})
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || store.deleted != "" {
		t.Fatalf("logout without CSRF did not fail closed: status=%d deleted=%q", response.Code, store.deleted)
	}
	request = httptest.NewRequest(http.MethodPost, "/auth/session/logout", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-bpmp_session", Value: "session-token"})
	request.Header.Set("X-CSRF-Token", csrf)
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || store.deleted != "session-token" || len(store.sessions) != 0 {
		t.Fatalf("logout failed to destroy session: status=%d deleted=%q", response.Code, store.deleted)
	}
}

func TestOIDCFullFlowWithRedisSessionAdapter(t *testing.T) {
	now := time.Now().UTC()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	redisAddress := os.Getenv("BPMP_TEST_REDIS_ADDR")
	if redisAddress == "" {
		redisAddress = miniredis.RunT(t).Addr()
	}
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	t.Cleanup(func() { _ = redisClient.Close() })
	store, err := redissession.New(redisClient, redissession.Config{
		Prefix: "bff:session:test:" + strings.ReplaceAll(t.Name(), "/", ":"), OperationTimeout: time.Second, MaxTokenBytes: 256, MaxRecordBytes: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	authzClient := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"profiles":[{"tenant_id":"tenant-a","user_id":"user-a","active":true,"roles":[]}]}`))}, nil
	})
	var exchangedVerifier string
	var nonce string
	oidcClient := doerFunc(func(request *http.Request) (*http.Response, error) {
		if err := request.ParseForm(); err != nil {
			return nil, err
		}
		exchangedVerifier = request.Form.Get("code_verifier")
		body := `{"token_type":"Bearer","id_token":"` + makeOIDCIDToken(t, privateKey, nonce, now) + `"}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	handler := newOIDCTestHandler(t, now, publicKey, store, store, authzClient, oidcClient)
	login := httptest.NewRecorder()
	handler.Routes().ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/oidc/login?tenant_id=tenant-a", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	redirect, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	nonce = redirect.Query().Get("nonce")
	state := redirect.Query().Get("state")
	if nonce == "" || state == "" {
		t.Fatal("login redirect lacks state or nonce")
	}
	callbackRequest := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?state="+state+"&code=authorization-code", nil)
	for _, cookie := range login.Result().Cookies() {
		callbackRequest.AddCookie(cookie)
	}
	callback := httptest.NewRecorder()
	handler.Routes().ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusSeeOther || exchangedVerifier == "" {
		t.Fatalf("callback status=%d verifier=%q body=%s", callback.Code, exchangedVerifier, callback.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == "__Host-bpmp_session" {
			sessionCookie = cookie
		}
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatal("callback did not set session and CSRF cookies")
	}
	session, err := store.Get(context.Background(), sessionCookie.Value)
	if err != nil || session.TenantID != "tenant-a" || session.ActorID != "user-a" {
		t.Fatalf("persisted session=%#v err=%v", session, err)
	}
	replay := httptest.NewRecorder()
	handler.Routes().ServeHTTP(replay, callbackRequest)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("OIDC state replay status=%d", replay.Code)
	}
	logoutRequest := httptest.NewRequest(http.MethodPost, "/auth/session/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutRequest.Header.Set("X-CSRF-Token", csrfCookie.Value)
	logout := httptest.NewRecorder()
	handler.Routes().ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d", logout.Code)
	}
	if _, err = store.Get(context.Background(), sessionCookie.Value); !errors.Is(err, ports.ErrBrowserSessionNotFound) {
		t.Fatalf("session survived logout: %v", err)
	}
}

func newOIDCTestHandler(
	t *testing.T,
	now time.Time,
	publicKey ed25519.PublicKey,
	sessionStore ports.BrowserSessionStore,
	transactions ports.OIDCTransactionStore,
	authzClient httpDoer,
	oidcClient httpDoer,
) *Handler {
	t.Helper()
	_, workloadKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	value := config.OIDC{
		Enabled: true, Issuer: "https://idp.example/realm",
		AuthorizationEndpoint: "https://idp.example/authorize", TokenEndpoint: "https://idp.example/token",
		ClientID: "bpmp-portal", RedirectURI: "https://portal.example/auth/oidc/callback",
		Scopes: []string{"openid", "profile"}, OperationTimeoutMS: 1000,
		TransactionTTLMS: 300_000, SessionTTLMS: 1_800_000,
	}
	handler, err := NewHandler(
		&recordingEngine{}, &recordingHuman{}, testVerifier(t, publicKey),
		&workloadSigner{id: "api-gateway", keyID: "gateway-key", key: workloadKey, ttl: time.Minute},
		newModelRateLimiter(10, time.Minute), staticPolicyProvider{},
		WithBrowserSessions(sessionStore, config.BrowserSession{
			Enabled: true, CookieName: "__Host-bpmp_session", CSRFHeaderName: "X-CSRF-Token",
			RedisKeyPrefix: "bff:session", OperationTimeoutMS: 1000,
			MaxTokenBytes: 256, MaxRecordBytes: 8192,
		}),
		WithAuthorizationAdmin(authzClient, "https://authz.example", "authz-service"),
		WithOIDCLogin(value, oidcClient, transactions),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	return handler
}

func makeOIDCIDToken(t *testing.T, key ed25519.PrivateKey, nonce string, now time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "https://idp.example/realm", "sub": "idp-subject-1", "aud": "bpmp-portal",
		"nonce": nonce, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Minute).Unix(),
	})
	token.Header["kid"] = "actor-key"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
