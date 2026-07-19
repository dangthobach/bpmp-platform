package actorverifier

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/proto"
)

const signedContextSchemaVersion = 1

type Config struct {
	Issuers           map[string]struct{}
	Audiences         map[string]struct{}
	AllowedJWTMethods map[string]struct{}
	WorkloadID        string
	MaxProofBytes     int
	MaxJWKSKeys       int
	MaxRoles          int
	MaxCapabilities   int
	ClockSkew         time.Duration
}

func (c Config) validate() error {
	if len(c.Issuers) == 0 || len(c.Audiences) == 0 || len(c.AllowedJWTMethods) == 0 ||
		c.WorkloadID == "" || c.MaxProofBytes <= 0 || c.MaxJWKSKeys <= 0 || c.MaxRoles <= 0 || c.MaxCapabilities <= 0 || c.ClockSkew < 0 {
		return errors.New("actor verifier configuration is incomplete")
	}
	for method := range c.AllowedJWTMethods {
		if method != jwt.SigningMethodRS256.Alg() && method != jwt.SigningMethodEdDSA.Alg() {
			return fmt.Errorf("JWT signing method %s is not allowed", method)
		}
	}
	return nil
}

type RevokeEpochProvider interface {
	RequiredEpoch(context.Context, string, string) (uint64, error)
}

type MemoryRevokeEpochs struct {
	mu     sync.RWMutex
	epochs map[string]uint64
}

func NewMemoryRevokeEpochs() *MemoryRevokeEpochs {
	return &MemoryRevokeEpochs{epochs: make(map[string]uint64)}
}

func (m *MemoryRevokeEpochs) Set(tenantID, actorID string, epoch uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.epochs[tenantID+"\x00"+actorID] = epoch
}

func (m *MemoryRevokeEpochs) RequiredEpoch(_ context.Context, tenantID, actorID string) (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.epochs[tenantID+"\x00"+actorID], nil
}

type Verifier struct {
	config       Config
	revocations  RevokeEpochProvider
	mu           sync.RWMutex
	jwtKeys      map[string]crypto.PublicKey
	internalKeys map[string]ed25519.PublicKey
}

func New(config Config, jwks []byte, internalKeys map[string]ed25519.PublicKey, revocations RevokeEpochProvider) (*Verifier, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if revocations == nil {
		return nil, errors.New("revoke epoch provider is required")
	}
	keys, err := parseJWKS(jwks, config.MaxJWKSKeys)
	if err != nil {
		return nil, err
	}
	internal, err := cloneInternalKeys(internalKeys, config.MaxJWKSKeys)
	if err != nil {
		return nil, err
	}
	return &Verifier{config: config, revocations: revocations, jwtKeys: keys, internalKeys: internal}, nil
}

func (v *Verifier) ReplaceJWKS(jwks []byte) error {
	keys, err := parseJWKS(jwks, v.config.MaxJWKSKeys)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.jwtKeys = keys
	v.mu.Unlock()
	return nil
}

func (v *Verifier) ReplaceInternalKeys(keys map[string]ed25519.PublicKey) error {
	cloned, err := cloneInternalKeys(keys, v.config.MaxJWKSKeys)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.internalKeys = cloned
	v.mu.Unlock()
	return nil
}

func (v *Verifier) VerifyActor(ctx context.Context, request application.ActorVerificationRequest) (application.ActorIdentity, error) {
	if request.TenantID == "" || request.EvaluatedAt.IsZero() {
		return application.ActorIdentity{}, errors.New("verification scope and time are required")
	}
	credential := request.Credential
	if (len(credential.OriginalSignedToken) == 0) == (len(credential.SignedActorContext) == 0) {
		return application.ActorIdentity{}, application.ErrActorProof
	}
	var identity verifiedIdentity
	var err error
	if len(credential.OriginalSignedToken) > 0 {
		identity, err = v.verifyJWT(credential.OriginalSignedToken, request.TenantID, request.EvaluatedAt)
	} else {
		identity, err = v.verifyInternal(credential.SignedActorContext, request)
	}
	if err != nil {
		return application.ActorIdentity{}, err
	}
	required, err := v.revocations.RequiredEpoch(ctx, request.TenantID, identity.actorID)
	if err != nil {
		return application.ActorIdentity{}, fmt.Errorf("resolve revoke epoch: %w", err)
	}
	if identity.revokeEpoch < required {
		return application.ActorIdentity{}, errors.New("actor proof has been revoked")
	}
	groups := make(map[string]struct{}, len(identity.roles))
	for _, role := range identity.roles {
		groups[role] = struct{}{}
	}
	capabilities := make(map[string]struct{}, len(identity.capabilities))
	for _, capability := range identity.capabilities {
		capabilities[capability] = struct{}{}
	}
	return application.ActorIdentity{ActorID: identity.actorID, Groups: groups, Capabilities: capabilities}, nil
}

type verifiedIdentity struct {
	actorID      string
	roles        []string
	capabilities []string
	revokeEpoch  uint64
}

type actorClaims struct {
	TenantID     string   `json:"tenant_id"`
	Roles        []string `json:"roles"`
	Capabilities []string `json:"capabilities"`
	RevokeEpoch  uint64   `json:"revoke_epoch"`
	jwt.RegisteredClaims
}

func (v *Verifier) verifyJWT(raw []byte, tenantID string, evaluatedAt time.Time) (verifiedIdentity, error) {
	if len(raw) > v.config.MaxProofBytes {
		return verifiedIdentity{}, errors.New("JWT exceeds configured size limit")
	}
	methods := make([]string, 0, len(v.config.AllowedJWTMethods))
	for method := range v.config.AllowedJWTMethods {
		methods = append(methods, method)
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods(methods),
		jwt.WithTimeFunc(func() time.Time { return evaluatedAt }),
		jwt.WithLeeway(v.config.ClockSkew),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	claims := &actorClaims{}
	token, err := parser.ParseWithClaims(string(raw), claims, func(token *jwt.Token) (any, error) {
		keyID, ok := token.Header["kid"].(string)
		if !ok || keyID == "" {
			return nil, errors.New("JWT kid is missing")
		}
		v.mu.RLock()
		key := v.jwtKeys[keyID]
		v.mu.RUnlock()
		if key == nil {
			return nil, errors.New("JWT key is unknown")
		}
		return key, nil
	})
	if err != nil || !token.Valid {
		return verifiedIdentity{}, errors.New("JWT signature or claims are invalid")
	}
	if _, ok := v.config.Issuers[claims.Issuer]; !ok || claims.Subject == "" || claims.TenantID != tenantID || !audienceMatches(claims.Audience, v.config.Audiences) {
		return verifiedIdentity{}, errors.New("JWT identity scope does not match request")
	}
	if err = validateAuthorizationValues(claims.Roles, claims.Capabilities, v.config); err != nil {
		return verifiedIdentity{}, err
	}
	return verifiedIdentity{actorID: claims.Subject, roles: canonical(claims.Roles), capabilities: canonical(claims.Capabilities), revokeEpoch: claims.RevokeEpoch}, nil
}

func (v *Verifier) verifyInternal(raw []byte, request application.ActorVerificationRequest) (verifiedIdentity, error) {
	if len(raw) > v.config.MaxProofBytes {
		return verifiedIdentity{}, errors.New("signed actor context exceeds configured size limit")
	}
	var proof authv1.SignedActorContext
	if err := proto.Unmarshal(raw, &proof); err != nil {
		return verifiedIdentity{}, errors.New("signed actor context is malformed")
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(&proof)
	if err != nil || !bytes.Equal(encoded, raw) {
		return verifiedIdentity{}, errors.New("signed actor context is not canonical")
	}
	if proof.GetSchemaVersion() != signedContextSchemaVersion || proof.GetTenantId() != request.TenantID || proof.GetActorId() == "" ||
		proof.GetAudienceWorkloadId() != v.config.WorkloadID || proof.GetSigningKeyId() == "" ||
		(request.CommandID != "" && proof.GetCommandId() != request.CommandID) {
		return verifiedIdentity{}, errors.New("signed actor context scope does not match request")
	}
	now := uint64(request.EvaluatedAt.UnixMilli())
	skew := uint64(v.config.ClockSkew.Milliseconds())
	if proof.GetIssuedAtEpochMs() > saturatingAdd(now, skew) || now >= saturatingAdd(proof.GetExpiresAtEpochMs(), skew) || proof.GetIssuedAtEpochMs() >= proof.GetExpiresAtEpochMs() {
		return verifiedIdentity{}, errors.New("signed actor context is outside validity window")
	}
	if err = validateAuthorizationValues(proof.GetRoles(), proof.GetCapabilities(), v.config); err != nil {
		return verifiedIdentity{}, err
	}
	if !sort.StringsAreSorted(proof.GetRoles()) || !sort.StringsAreSorted(proof.GetCapabilities()) || hasDuplicates(proof.GetRoles()) || hasDuplicates(proof.GetCapabilities()) {
		return verifiedIdentity{}, errors.New("signed actor context is not canonical")
	}
	contentHash := append([]byte(nil), proof.GetContentHash()...)
	signature := append([]byte(nil), proof.GetSignature()...)
	proof.ContentHash = nil
	proof.Signature = nil
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(&proof)
	if err != nil {
		return verifiedIdentity{}, err
	}
	digest := sha256.Sum256(unsigned)
	if !bytes.Equal(contentHash, digest[:]) {
		return verifiedIdentity{}, errors.New("signed actor context hash does not match")
	}
	v.mu.RLock()
	key := append(ed25519.PublicKey(nil), v.internalKeys[proof.GetSigningKeyId()]...)
	v.mu.RUnlock()
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, digest[:], signature) {
		return verifiedIdentity{}, errors.New("signed actor context signature is invalid")
	}
	return verifiedIdentity{actorID: proof.GetActorId(), roles: append([]string(nil), proof.GetRoles()...), capabilities: append([]string(nil), proof.GetCapabilities()...), revokeEpoch: proof.GetRevokeEpoch()}, nil
}

type jwksDocument struct {
	Keys []json.RawMessage `json:"keys"`
}

func parseJWKS(raw []byte, limit int) (map[string]crypto.PublicKey, error) {
	var document jwksDocument
	if err := json.Unmarshal(raw, &document); err != nil || len(document.Keys) == 0 || len(document.Keys) > limit {
		return nil, errors.New("JWKS is malformed or outside configured key limit")
	}
	keys := make(map[string]crypto.PublicKey, len(document.Keys))
	for _, encoded := range document.Keys {
		var header struct{ Kty, Kid, Alg, N, E, Crv, X string }
		if err := json.Unmarshal(encoded, &header); err != nil || header.Kid == "" {
			return nil, errors.New("JWKS key is malformed")
		}
		if _, exists := keys[header.Kid]; exists {
			return nil, errors.New("JWKS contains duplicate key id")
		}
		switch header.Kty {
		case "RSA":
			n, err := base64.RawURLEncoding.DecodeString(header.N)
			if err != nil {
				return nil, errors.New("JWKS RSA modulus is malformed")
			}
			e, err := base64.RawURLEncoding.DecodeString(header.E)
			if err != nil || len(e) == 0 || len(e) > 4 {
				return nil, errors.New("JWKS RSA exponent is malformed")
			}
			exponent := 0
			for _, value := range e {
				exponent = exponent<<8 | int(value)
			}
			keys[header.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
		case "OKP":
			if header.Crv != "Ed25519" {
				return nil, errors.New("JWKS OKP curve is unsupported")
			}
			x, err := base64.RawURLEncoding.DecodeString(header.X)
			if err != nil || len(x) != ed25519.PublicKeySize {
				return nil, errors.New("JWKS Ed25519 key is malformed")
			}
			keys[header.Kid] = ed25519.PublicKey(x)
		default:
			return nil, errors.New("JWKS key type is unsupported")
		}
	}
	return keys, nil
}

func cloneInternalKeys(keys map[string]ed25519.PublicKey, limit int) (map[string]ed25519.PublicKey, error) {
	if len(keys) == 0 || len(keys) > limit {
		return nil, errors.New("internal key set is outside configured limit")
	}
	cloned := make(map[string]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		if id == "" || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("internal verification key is invalid")
		}
		cloned[id] = append(ed25519.PublicKey(nil), key...)
	}
	return cloned, nil
}

func audienceMatches(actual jwt.ClaimStrings, allowed map[string]struct{}) bool {
	for _, audience := range actual {
		if _, ok := allowed[audience]; ok {
			return true
		}
	}
	return false
}
func validateAuthorizationValues(roles, capabilities []string, config Config) error {
	if len(roles) > config.MaxRoles || len(capabilities) > config.MaxCapabilities {
		return errors.New("actor authorization claims exceed configured limits")
	}
	for _, value := range append(append([]string(nil), roles...), capabilities...) {
		if value == "" {
			return errors.New("actor authorization claims contain an empty value")
		}
	}
	return nil
}
func canonical(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return compact(out)
}
func compact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
func hasDuplicates(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return true
		}
	}
	return false
}
func saturatingAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}

var _ application.ActorVerifier = (*Verifier)(nil)
