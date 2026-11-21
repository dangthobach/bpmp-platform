package jwtauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifierEnforcesTenantIssuerAudienceAndExpiry(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rawJWKS, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "kid": "key-1", "crv": "Ed25519",
		"x": base64.RawURLEncoding.EncodeToString(public),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewFromJWKS(Config{
		Issuers: []string{"issuer"}, Audiences: []string{"cockpit"},
		Algorithms: []string{"EdDSA"}, MaxTokenBytes: 4096, MaxJWKSKeys: 2,
	}, rawJWKS)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0)
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "issuer", "sub": "actor-1", "aud": []string{"cockpit"},
		"tenant_id": "tenant-a", "iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(), "capabilities": []string{"workflow.query.own"},
		"revoke_epoch": 7,
	})
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Verify(signed, "tenant-a", now)
	if err != nil || identity.Subject != "actor-1" || identity.RevokeEpoch != 7 ||
		len(identity.Capabilities) != 1 || identity.Capabilities[0] != "workflow.query.own" {
		t.Fatalf("valid identity rejected: %+v, %v", identity, err)
	}
	if _, err = verifier.Verify(signed, "tenant-b", now); err == nil {
		t.Fatal("cross-tenant token must be rejected")
	}
	if _, err = verifier.Verify(signed, "tenant-a", now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestVerifyIDTokenRequiresIssuerAudienceAndNonce(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rawJWKS, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "kid": "key-1", "crv": "Ed25519",
		"x": base64.RawURLEncoding.EncodeToString(public),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewFromJWKS(Config{
		Issuers: []string{"https://idp.example/realm"}, Audiences: []string{"unused"},
		Algorithms: []string{"EdDSA"}, MaxTokenBytes: 4096, MaxJWKSKeys: 2,
	}, rawJWKS)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0).UTC()
	makeToken := func(issuer string, audience any, nonce, azp string) string {
		claims := jwt.MapClaims{
			"iss": issuer, "sub": "idp-subject-1", "aud": audience,
			"nonce": nonce, "iat": now.Add(-time.Minute).Unix(),
			"exp": now.Add(time.Minute).Unix(),
		}
		if azp != "" {
			claims["azp"] = azp
		}
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		token.Header["kid"] = "key-1"
		signed, signErr := token.SignedString(private)
		if signErr != nil {
			t.Fatal(signErr)
		}
		return signed
	}
	valid := makeToken("https://idp.example/realm", "bpmp-portal", "nonce-1", "")
	identity, err := verifier.VerifyIDToken(valid, "https://idp.example/realm", "bpmp-portal", "nonce-1", now)
	if err != nil || identity.Subject != "idp-subject-1" {
		t.Fatalf("valid ID token rejected: identity=%+v err=%v", identity, err)
	}
	for name, token := range map[string]string{
		"wrong issuer":                       makeToken("https://evil.example/realm", "bpmp-portal", "nonce-1", ""),
		"wrong audience":                     makeToken("https://idp.example/realm", "other-client", "nonce-1", ""),
		"wrong nonce":                        makeToken("https://idp.example/realm", "bpmp-portal", "nonce-2", ""),
		"missing azp for multiple audiences": makeToken("https://idp.example/realm", []string{"bpmp-portal", "other"}, "nonce-1", ""),
		"wrong azp":                          makeToken("https://idp.example/realm", []string{"bpmp-portal", "other"}, "nonce-1", "other-client"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, verifyErr := verifier.VerifyIDToken(token, "https://idp.example/realm", "bpmp-portal", "nonce-1", now); verifyErr == nil {
				t.Fatal("invalid OIDC ID token was accepted")
			}
		})
	}
}
