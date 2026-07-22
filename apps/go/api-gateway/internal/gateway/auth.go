package gateway

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

type actorIdentity struct{ ID string }
type actorClaims struct {
	TenantID string `json:"tenant_id"`
	jwt.RegisteredClaims
}

type verifier struct {
	keys          map[string]crypto.PublicKey
	issuers       map[string]struct{}
	audiences     map[string]struct{}
	methods       []string
	maxTokenBytes int
	skew          time.Duration
}

func newVerifier(value config.Identity) (*verifier, error) {
	data, err := os.ReadFile(value.JWKSPath)
	if err != nil {
		return nil, err
	}
	keys, err := parseJWKS(data, value.MaxJWKSKeys)
	if err != nil {
		return nil, err
	}
	methods := append([]string(nil), value.Algorithms...)
	for _, method := range methods {
		if method != "RS256" && method != "EdDSA" {
			return nil, errors.New("JWT algorithm is not allowed")
		}
	}
	return &verifier{keys: keys, issuers: stringSet(value.Issuers), audiences: stringSet(value.Audiences), methods: methods, maxTokenBytes: value.MaxTokenBytes, skew: time.Duration(value.ClockSkewSeconds) * time.Second}, nil
}

func (v *verifier) verify(raw, tenantID string, now time.Time) (actorIdentity, error) {
	if raw == "" || len(raw) > v.maxTokenBytes || tenantID == "" {
		return actorIdentity{}, errors.New("actor token is missing or oversized")
	}
	claims := &actorClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods(v.methods), jwt.WithTimeFunc(func() time.Time { return now }), jwt.WithLeeway(v.skew), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	token, err := parser.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errors.New("JWT kid is missing")
		}
		key := v.keys[kid]
		if key == nil {
			return nil, errors.New("JWT key is unknown")
		}
		return key, nil
	})
	if err != nil || !token.Valid || claims.Subject == "" || claims.TenantID != tenantID {
		return actorIdentity{}, errors.New("JWT identity is invalid")
	}
	if _, ok := v.issuers[claims.Issuer]; !ok || !audienceAllowed(claims.Audience, v.audiences) {
		return actorIdentity{}, errors.New("JWT scope is invalid")
	}
	return actorIdentity{ID: claims.Subject}, nil
}

type jwksDocument struct {
	Keys []json.RawMessage `json:"keys"`
}

func parseJWKS(raw []byte, limit int) (map[string]crypto.PublicKey, error) {
	var document jwksDocument
	if err := json.Unmarshal(raw, &document); err != nil || len(document.Keys) == 0 || len(document.Keys) > limit {
		return nil, errors.New("JWKS is malformed or outside configured limit")
	}
	keys := make(map[string]crypto.PublicKey, len(document.Keys))
	for _, encoded := range document.Keys {
		var header struct{ Kty, Kid, N, E, Crv, X string }
		if err := json.Unmarshal(encoded, &header); err != nil || header.Kid == "" || keys[header.Kid] != nil {
			return nil, errors.New("JWKS key is malformed or duplicated")
		}
		switch header.Kty {
		case "RSA":
			n, nerr := base64.RawURLEncoding.DecodeString(header.N)
			e, eerr := base64.RawURLEncoding.DecodeString(header.E)
			if nerr != nil || eerr != nil || len(e) == 0 || len(e) > 4 {
				return nil, errors.New("JWKS RSA key is malformed")
			}
			exponent := 0
			for _, value := range e {
				exponent = exponent<<8 | int(value)
			}
			keys[header.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
		case "OKP":
			x, xerr := base64.RawURLEncoding.DecodeString(header.X)
			if xerr != nil || header.Crv != "Ed25519" || len(x) != ed25519.PublicKeySize {
				return nil, errors.New("JWKS Ed25519 key is malformed")
			}
			keys[header.Kid] = ed25519.PublicKey(x)
		default:
			return nil, errors.New("JWKS key type is unsupported")
		}
	}
	return keys, nil
}
func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
func audienceAllowed(values jwt.ClaimStrings, allowed map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := allowed[value]; ok {
			return true
		}
	}
	return false
}
