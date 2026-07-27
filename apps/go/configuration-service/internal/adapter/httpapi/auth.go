package httpapi

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

	"github.com/golang-jwt/jwt/v5"
)

type IdentityConfig struct {
	JWKSPath      string
	Issuers       []string
	Audiences     []string
	Algorithms    []string
	MaxTokenBytes int
	MaxJWKSKeys   int
	ClockSkew     time.Duration
}

type Identity struct {
	ActorID      string
	TenantID     string
	Capabilities map[string]struct{}
}

type claims struct {
	TenantID     string   `json:"tenant_id"`
	Capabilities []string `json:"capabilities"`
	jwt.RegisteredClaims
}

type Verifier struct {
	keys          map[string]crypto.PublicKey
	issuers       map[string]struct{}
	audiences     map[string]struct{}
	methods       []string
	maxTokenBytes int
	skew          time.Duration
}

func NewVerifier(config IdentityConfig) (*Verifier, error) {
	data, err := os.ReadFile(config.JWKSPath)
	if err != nil {
		return nil, err
	}
	keys, err := parseJWKS(data, config.MaxJWKSKeys)
	if err != nil {
		return nil, err
	}
	for _, method := range config.Algorithms {
		if method != "RS256" && method != "EdDSA" {
			return nil, errors.New("JWT algorithm is not allowed")
		}
	}
	if config.MaxTokenBytes <= 0 || len(config.Issuers) == 0 || len(config.Audiences) == 0 {
		return nil, errors.New("identity configuration is invalid")
	}
	return &Verifier{
		keys: keys, issuers: stringSet(config.Issuers), audiences: stringSet(config.Audiences),
		methods: append([]string(nil), config.Algorithms...), maxTokenBytes: config.MaxTokenBytes,
		skew: config.ClockSkew,
	}, nil
}

func (v *Verifier) Verify(raw, tenantID string, now time.Time) (Identity, error) {
	if raw == "" || len(raw) > v.maxTokenBytes || tenantID == "" {
		return Identity{}, errors.New("actor token is missing or oversized")
	}
	value := &claims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(v.methods),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithLeeway(v.skew),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	token, err := parser.ParseWithClaims(raw, value, func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" || v.keys[kid] == nil {
			return nil, errors.New("JWT key is unknown")
		}
		return v.keys[kid], nil
	})
	if err != nil || !token.Valid || value.Subject == "" || value.TenantID != tenantID {
		return Identity{}, errors.New("JWT identity is invalid")
	}
	if _, ok := v.issuers[value.Issuer]; !ok || !audienceAllowed(value.Audience, v.audiences) {
		return Identity{}, errors.New("JWT scope is invalid")
	}
	capabilities := make(map[string]struct{}, len(value.Capabilities))
	for _, capability := range value.Capabilities {
		if capability != "" {
			capabilities[capability] = struct{}{}
		}
	}
	return Identity{ActorID: value.Subject, TenantID: value.TenantID, Capabilities: capabilities}, nil
}

type jwksDocument struct {
	Keys []json.RawMessage `json:"keys"`
}

func parseJWKS(raw []byte, limit int) (map[string]crypto.PublicKey, error) {
	var document jwksDocument
	if err := json.Unmarshal(raw, &document); err != nil ||
		len(document.Keys) == 0 || limit <= 0 || len(document.Keys) > limit {
		return nil, errors.New("JWKS is malformed or outside configured limit")
	}
	keys := make(map[string]crypto.PublicKey, len(document.Keys))
	for _, encoded := range document.Keys {
		var header struct{ Kty, Kid, N, E, Crv, X string }
		if err := json.Unmarshal(encoded, &header); err != nil ||
			header.Kid == "" || keys[header.Kid] != nil {
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
			for _, item := range e {
				exponent = exponent<<8 | int(item)
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
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func audienceAllowed(values jwt.ClaimStrings, allowed map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := allowed[value]; ok {
			return true
		}
	}
	return false
}
