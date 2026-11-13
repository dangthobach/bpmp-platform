package identity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"sort"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"google.golang.org/protobuf/proto"
)

const signedContextSchemaVersion = 1

type Config struct {
	JWKSPath          string
	Issuers           []string
	Audiences         []string
	Algorithms        []string
	MaxTokenBytes     int
	MaxJWKSKeys       int
	MaxRoles          int
	MaxCapabilities   int
	ClockSkew         time.Duration
	WorkloadID        string
	InternalProofKeys map[string]ed25519.PublicKey
}

type Verifier struct {
	jwt          *jwtauth.Verifier
	internalKeys map[string]ed25519.PublicKey
	workloadID   string
	maxRoles     int
	maxCaps      int
	maxBytes     int
	skew         time.Duration
}

func New(config Config) (*Verifier, error) {
	if config.MaxRoles <= 0 || config.MaxCapabilities <= 0 || config.MaxTokenBytes <= 0 || config.MaxJWKSKeys <= 0 || config.ClockSkew < 0 || config.WorkloadID == "" || len(config.InternalProofKeys) == 0 {
		return nil, errors.New("authoring actor verifier configuration is incomplete")
	}
	jwtVerifier, err := jwtauth.New(jwtauth.Config{JWKSPath: config.JWKSPath, Issuers: config.Issuers, Audiences: config.Audiences, Algorithms: config.Algorithms, MaxTokenBytes: config.MaxTokenBytes, MaxJWKSKeys: config.MaxJWKSKeys, ClockSkew: config.ClockSkew})
	if err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey, len(config.InternalProofKeys))
	for keyID, key := range config.InternalProofKeys {
		if keyID == "" || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid internal actor proof trust key")
		}
		keys[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	return &Verifier{jwt: jwtVerifier, internalKeys: keys, workloadID: config.WorkloadID, maxRoles: config.MaxRoles, maxCaps: config.MaxCapabilities, maxBytes: config.MaxTokenBytes, skew: config.ClockSkew}, nil
}

func (v *Verifier) Verify(_ context.Context, tenantID, commandID string, proof *authv1.ActorProof, now time.Time) (application.Actor, error) {
	if tenantID == "" || now.IsZero() || proof == nil || len(proof.GetSignedProof()) == 0 || len(proof.GetSignedProof()) > v.maxBytes {
		return application.Actor{}, application.ErrUnauthenticated
	}
	var actor application.Actor
	switch proof.GetType() {
	case authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT:
		identity, err := v.jwt.Verify(string(proof.GetSignedProof()), tenantID, now)
		if err != nil {
			return application.Actor{}, application.ErrUnauthenticated
		}
		actor = application.Actor{ID: identity.Subject, Roles: identity.Roles, Capabilities: identity.Capabilities, RevokeEpoch: identity.RevokeEpoch}
	case authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT:
		var err error
		actor, err = v.verifyInternal(proof.GetSignedProof(), tenantID, commandID, now)
		if err != nil {
			return application.Actor{}, application.ErrUnauthenticated
		}
	default:
		return application.Actor{}, application.ErrUnauthenticated
	}
	if actor.ID == "" || len(actor.Roles) > v.maxRoles || len(actor.Capabilities) > v.maxCaps || !validSet(actor.Roles) || !validSet(actor.Capabilities) {
		return application.Actor{}, application.ErrUnauthenticated
	}
	return actor, nil
}

func (v *Verifier) verifyInternal(raw []byte, tenantID, commandID string, now time.Time) (application.Actor, error) {
	var proof authv1.SignedActorContext
	if proto.Unmarshal(raw, &proof) != nil {
		return application.Actor{}, errors.New("malformed signed actor context")
	}
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&proof)
	if err != nil || !bytes.Equal(canonical, raw) {
		return application.Actor{}, errors.New("noncanonical signed actor context")
	}
	if proof.GetSchemaVersion() != signedContextSchemaVersion || proof.GetTenantId() != tenantID || proof.GetActorId() == "" || proof.GetAudienceWorkloadId() != v.workloadID || proof.GetSigningKeyId() == "" || (commandID != "" && proof.GetCommandId() != commandID) || !canonicalSet(proof.GetRoles()) || !canonicalSet(proof.GetCapabilities()) || len(proof.GetRoles()) > v.maxRoles || len(proof.GetCapabilities()) > v.maxCaps {
		return application.Actor{}, errors.New("signed actor context scope is invalid")
	}
	nowMS := uint64(now.UnixMilli())
	skewMS := uint64(v.skew.Milliseconds())
	if proof.GetIssuedAtEpochMs() > saturatingAdd(nowMS, skewMS) || nowMS >= saturatingAdd(proof.GetExpiresAtEpochMs(), skewMS) || proof.GetIssuedAtEpochMs() >= proof.GetExpiresAtEpochMs() {
		return application.Actor{}, errors.New("signed actor context expired")
	}
	contentHash, signature := append([]byte(nil), proof.GetContentHash()...), append([]byte(nil), proof.GetSignature()...)
	proof.ContentHash, proof.Signature = nil, nil
	unsigned, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&proof)
	if err != nil {
		return application.Actor{}, err
	}
	digest := sha256.Sum256(unsigned)
	key := v.internalKeys[proof.GetSigningKeyId()]
	if !bytes.Equal(contentHash, digest[:]) || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, digest[:], signature) {
		return application.Actor{}, errors.New("signed actor context signature is invalid")
	}
	return application.Actor{ID: proof.GetActorId(), Roles: append([]string(nil), proof.GetRoles()...), Capabilities: append([]string(nil), proof.GetCapabilities()...), RevokeEpoch: proof.GetRevokeEpoch()}, nil
}

func canonicalSet(values []string) bool {
	if !sort.StringsAreSorted(values) {
		return false
	}
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] == value) {
			return false
		}
	}
	return true
}

func validSet(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func saturatingAdd(left, right uint64) uint64 {
	result := left + right
	if result < left {
		return ^uint64(0)
	}
	return result
}

var _ application.ActorVerifier = (*Verifier)(nil)
