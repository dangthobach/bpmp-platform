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
		"exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Verify(signed, "tenant-a", now)
	if err != nil || identity.Subject != "actor-1" {
		t.Fatalf("valid identity rejected: %+v, %v", identity, err)
	}
	if _, err = verifier.Verify(signed, "tenant-b", now); err == nil {
		t.Fatal("cross-tenant token must be rejected")
	}
	if _, err = verifier.Verify(signed, "tenant-a", now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired token must be rejected")
	}
}
