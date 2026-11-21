package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/adapter/redissession"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"github.com/redis/go-redis/v9"
	"golang.org/x/net/html"
)

// Run with an isolated HTTPS Keycloak realm and Redis. Set BPMP_TEST_AUTHZ_URL
// and the remaining Authz variables to exercise real subject resolution too.
func TestOIDCWithLiveKeycloakAndRedis(t *testing.T) {
	issuer, caPath := os.Getenv("BPMP_TEST_KEYCLOAK_ISSUER"), os.Getenv("BPMP_TEST_KEYCLOAK_CA")
	redisAddress := os.Getenv("BPMP_TEST_REDIS_ADDR")
	username, password := os.Getenv("BPMP_TEST_KEYCLOAK_USER"), os.Getenv("BPMP_TEST_KEYCLOAK_PASSWORD")
	if issuer == "" || caPath == "" || redisAddress == "" || username == "" || password == "" {
		t.Skip("live Keycloak and Redis test environment is not configured")
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid Keycloak CA")
	}
	if authzCAPath := os.Getenv("BPMP_TEST_AUTHZ_CA"); authzCAPath != "" {
		authzCA, readErr := os.ReadFile(authzCAPath)
		if readErr != nil || !roots.AppendCertsFromPEM(authzCA) {
			t.Fatalf("invalid Authz CA: %v", readErr)
		}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}, Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	clientID := "bpmp-portal-it"
	get := func(path string) []byte {
		t.Helper()
		response, err := client.Get(issuer + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Keycloak %s returned %d", path, response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	shared, err := jwtauth.NewFromJWKS(jwtauth.Config{Issuers: []string{issuer}, Audiences: []string{clientID}, Algorithms: []string{"RS256"}, MaxTokenBytes: 16384, MaxJWKSKeys: 16}, get("/protocol/openid-connect/certs"))
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	t.Cleanup(func() { _ = redisClient.Close() })
	store, err := redissession.New(redisClient, redissession.Config{Prefix: "bff:session:live-it", OperationTimeout: time.Second, MaxTokenBytes: 256, MaxRecordBytes: 8192})
	if err != nil {
		t.Fatal(err)
	}
	_, workloadKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, actorID := "tenant-a", "user-a"
	authzURL := "https://authz.example"
	var authz httpDoer = doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"profiles":[{"tenant_id":"tenant-a","user_id":"user-a","active":true,"roles":[]}]}`))}, nil
	})
	if configuredURL := os.Getenv("BPMP_TEST_AUTHZ_URL"); configuredURL != "" {
		seedPath, subject := os.Getenv("BPMP_TEST_GATEWAY_SIGNING_SEED"), os.Getenv("BPMP_TEST_KEYCLOAK_SUBJECT")
		clientKeyPath, clientCertPath := os.Getenv("BPMP_TEST_AUTHZ_CLIENT_KEY"), os.Getenv("BPMP_TEST_AUTHZ_CLIENT_CERT")
		if seedPath == "" || subject == "" || clientKeyPath == "" || clientCertPath == "" {
			t.Fatal("real Authz test requires Gateway seed, Keycloak subject, and mTLS client certificate/key")
		}
		seed, readErr := os.ReadFile(seedPath)
		if readErr != nil || len(seed) != ed25519.SeedSize {
			t.Fatalf("invalid Gateway signing seed: %v", readErr)
		}
		workloadKey = ed25519.NewKeyFromSeed(seed)
		certificate, certErr := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
		if certErr != nil {
			t.Fatal(certErr)
		}
		client.Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{certificate}
		authz, authzURL = client, configuredURL
		tenantID = "00000000-0000-0000-0000-000000000010"
		payload, marshalErr := json.Marshal(map[string]string{"tenant_id": tenantID, "username": username, "issuer": issuer, "subject": subject})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		manager := &workloadSigner{id: "api-gateway", keyID: "gateway-key", key: workloadKey}
		delegation, signErr := manager.signAuthzDelegation(tenantID, "00000000-0000-0000-0000-000000000020", "identity.profile.manage", "authz-service", time.Now())
		if signErr != nil {
			t.Fatal(signErr)
		}
		request, requestErr := http.NewRequest(http.MethodPost, authzURL+"/admin/v1/identity/profiles", bytes.NewReader(payload))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+delegation)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "bpmp-live-keycloak-profile-provision-v1")
		provisioned, doErr := client.Do(request)
		if doErr != nil {
			t.Fatal(doErr)
		}
		defer provisioned.Body.Close()
		if provisioned.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(io.LimitReader(provisioned.Body, 4096))
			t.Fatalf("Authz profile provisioning status=%d body=%s", provisioned.StatusCode, body)
		}
		var profile struct {
			UserID  string `json:"user_id"`
			Version int64  `json:"version"`
		}
		if decodeErr := json.NewDecoder(io.LimitReader(provisioned.Body, 4096)).Decode(&profile); decodeErr != nil || profile.UserID == "" {
			t.Fatalf("Authz profile response invalid: %v", decodeErr)
		}
		actorID = profile.UserID
		updateBody, marshalErr := json.Marshal(map[string]any{
			"tenant_id": tenantID, "username": username + "-updated", "issuer": issuer,
			"subject": subject, "active": true, "expected_version": profile.Version,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		updateURL := authzURL + "/admin/v1/identity/profiles/" + profile.UserID
		update := func(body []byte, key string) (int, []byte) {
			t.Helper()
			request, requestErr := http.NewRequest(http.MethodPut, updateURL, bytes.NewReader(body))
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			request.Header.Set("Authorization", "Bearer "+delegation)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", key)
			response, doErr := client.Do(request)
			if doErr != nil {
				t.Fatal(doErr)
			}
			defer response.Body.Close()
			result, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			if readErr != nil {
				t.Fatal(readErr)
			}
			return response.StatusCode, result
		}
		status, firstUpdate := update(updateBody, "bpmp-live-profile-update-v1")
		if status != http.StatusOK {
			t.Fatalf("Authz update status=%d body=%s", status, firstUpdate)
		}
		status, replayUpdate := update(updateBody, "bpmp-live-profile-update-v1")
		if status != http.StatusOK || !bytes.Equal(firstUpdate, replayUpdate) {
			t.Fatalf("Authz idempotent replay status=%d first=%s replay=%s", status, firstUpdate, replayUpdate)
		}
		conflictingBody := bytes.Replace(updateBody, []byte(username+"-updated"), []byte(username+"-conflict"), 1)
		status, _ = update(conflictingBody, "bpmp-live-profile-update-v1")
		if status != http.StatusConflict {
			t.Fatalf("Authz reused key with changed payload status=%d", status)
		}
		status, _ = update(updateBody, "bpmp-live-profile-stale-v1")
		if status != http.StatusConflict {
			t.Fatalf("Authz stale version status=%d", status)
		}
		var updatedProfile struct {
			Version int64 `json:"version"`
		}
		if decodeErr := json.Unmarshal(firstUpdate, &updatedProfile); decodeErr != nil || updatedProfile.Version <= profile.Version {
			t.Fatalf("Authz profile version did not advance: %s err=%v", firstUpdate, decodeErr)
		}
		roleID := "00000000-0000-0000-0000-000000000030"
		roleURL := authzURL + "/admin/v1/users/" + profile.UserID + "/roles/" + roleID
		roleMutation := func(method, capability string, expectedVersion int64, key string) (int, []byte) {
			t.Helper()
			roleBody, marshalErr := json.Marshal(map[string]any{"tenant_id": tenantID, "role_id": roleID, "expected_version": expectedVersion})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			proof, signErr := manager.signAuthzDelegation(tenantID, "00000000-0000-0000-0000-000000000020", capability, "authz-service", time.Now())
			if signErr != nil {
				t.Fatal(signErr)
			}
			request, requestErr := http.NewRequest(method, roleURL, bytes.NewReader(roleBody))
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			request.Header.Set("Authorization", "Bearer "+proof)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", key)
			response, doErr := client.Do(request)
			if doErr != nil {
				t.Fatal(doErr)
			}
			defer response.Body.Close()
			result, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			if readErr != nil {
				t.Fatal(readErr)
			}
			return response.StatusCode, result
		}
		status, firstAssign := roleMutation(http.MethodPost, "identity.role.assign", updatedProfile.Version, "bpmp-live-role-assign-v1")
		if status != http.StatusOK {
			t.Fatalf("Authz role assign status=%d body=%s", status, firstAssign)
		}
		status, replayAssign := roleMutation(http.MethodPost, "identity.role.assign", updatedProfile.Version, "bpmp-live-role-assign-v1")
		if status != http.StatusOK || !bytes.Equal(firstAssign, replayAssign) {
			t.Fatalf("Authz role assign replay status=%d body=%s", status, replayAssign)
		}
		var assigned struct {
			Version int64 `json:"version"`
		}
		if decodeErr := json.Unmarshal(firstAssign, &assigned); decodeErr != nil || assigned.Version <= updatedProfile.Version {
			t.Fatalf("Authz role assign version invalid: %s err=%v", firstAssign, decodeErr)
		}
		status, firstRevoke := roleMutation(http.MethodDelete, "identity.role.revoke", assigned.Version, "bpmp-live-role-revoke-v1")
		if status != http.StatusOK {
			t.Fatalf("Authz role revoke status=%d body=%s", status, firstRevoke)
		}
		status, replayRevoke := roleMutation(http.MethodDelete, "identity.role.revoke", assigned.Version, "bpmp-live-role-revoke-v1")
		if status != http.StatusOK || !bytes.Equal(firstRevoke, replayRevoke) {
			t.Fatalf("Authz role revoke replay status=%d body=%s", status, replayRevoke)
		}
	}
	handler, err := NewHandler(&recordingEngine{}, &recordingHuman{}, &verifier{value: shared},
		&workloadSigner{id: "api-gateway", keyID: "gateway-key", key: workloadKey, ttl: time.Minute},
		newModelRateLimiter(10, time.Minute), staticPolicyProvider{},
		WithBrowserSessions(store, config.BrowserSession{Enabled: true, CookieName: "__Host-bpmp_session", CSRFHeaderName: "X-CSRF-Token", RedisKeyPrefix: "bff:session:live-it", OperationTimeoutMS: 1000, MaxTokenBytes: 256, MaxRecordBytes: 8192}),
		WithAuthorizationAdmin(authz, authzURL, "authz-service"),
		WithOIDCLogin(config.OIDC{Enabled: true, Issuer: issuer, AuthorizationEndpoint: issuer + "/protocol/openid-connect/auth", TokenEndpoint: issuer + "/protocol/openid-connect/token", ClientID: clientID, RedirectURI: "https://localhost:18443/auth/oidc/callback", Scopes: []string{"openid", "profile"}, OperationTimeoutMS: 5000, TransactionTTLMS: 300000, SessionTTLMS: 300000}, client, store))
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	handler.Routes().ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/oidc/login?tenant_id="+tenantID, nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	location := login.Header().Get("Location")
	response, err := client.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Keycloak login page status=%d", response.StatusCode)
	}
	page, err := html.Parse(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	action := findLoginFormAction(page)
	if action == "" {
		t.Fatal("Keycloak login form not found")
	}
	form := url.Values{"username": {username}, "password": {password}}
	response, err = client.PostForm(action, form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound && response.StatusCode != http.StatusSeeOther {
		t.Fatalf("Keycloak credential submission status=%d", response.StatusCode)
	}
	callbackURL, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if callbackURL.Query().Get("code") == "" || callbackURL.Query().Get("state") == "" {
		t.Fatalf("Keycloak did not issue an authorization code: %s", callbackURL.String())
	}
	callback := httptest.NewRequest(http.MethodGet, callbackURL.RequestURI(), nil)
	for _, cookie := range login.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	completed := httptest.NewRecorder()
	handler.Routes().ServeHTTP(completed, callback)
	if completed.Code != http.StatusSeeOther {
		t.Fatalf("Gateway callback status=%d body=%s", completed.Code, completed.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range completed.Result().Cookies() {
		if cookie.Name == "__Host-bpmp_session" {
			sessionCookie = cookie
		}
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatal("Gateway did not issue session and CSRF cookies")
	}
	session, err := store.Get(context.Background(), sessionCookie.Value)
	if err != nil || session.TenantID != tenantID || session.ActorID != actorID {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	if _, err = store.Consume(context.Background(), callbackURL.Query().Get("state")); !errors.Is(err, ports.ErrOIDCTransactionNotFound) {
		t.Fatalf("OIDC state was replayable: %v", err)
	}
	logoutRequest := httptest.NewRequest(http.MethodPost, "/auth/session/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutRequest.Header.Set("X-CSRF-Token", csrfCookie.Value)
	logout := httptest.NewRecorder()
	handler.Routes().ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("Gateway logout status=%d", logout.Code)
	}
	if _, err = store.Get(context.Background(), sessionCookie.Value); !errors.Is(err, ports.ErrBrowserSessionNotFound) {
		t.Fatalf("session survived logout: %v", err)
	}
}

func findLoginFormAction(node *html.Node) string {
	if node.Type == html.ElementNode && node.Data == "form" {
		id, action := "", ""
		for _, attr := range node.Attr {
			if attr.Key == "id" {
				id = attr.Val
			}
			if attr.Key == "action" {
				action = attr.Val
			}
		}
		if id == "kc-form-login" {
			return action
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if action := findLoginFormAction(child); action != "" {
			return action
		}
	}
	return ""
}
