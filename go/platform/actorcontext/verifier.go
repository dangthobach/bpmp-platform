package actorcontext

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"sort"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"google.golang.org/protobuf/proto"
)

const schemaVersion = 1

type Identity struct {
	TenantID     string
	ActorID      string
	Roles        []string
	Capabilities []string
	RevokeEpoch  uint64
	ExpiresAt    time.Time
}

type Config struct {
	WorkloadID string
	MaxBytes   int
	MaxRoles   int
	MaxCaps    int
	ClockSkew  time.Duration
	Keys       map[string]ed25519.PublicKey
}

type Verifier struct {
	workloadID string
	maxBytes   int
	maxRoles   int
	maxCaps    int
	skew       time.Duration
	keys       map[string]ed25519.PublicKey
}

func New(config Config) (*Verifier, error) {
	if config.WorkloadID == "" || config.MaxBytes <= 0 || config.MaxRoles <= 0 || config.MaxCaps <= 0 || config.ClockSkew < 0 || len(config.Keys) == 0 {
		return nil, errors.New("actor context verifier configuration is incomplete")
	}
	keys := make(map[string]ed25519.PublicKey, len(config.Keys))
	for keyID, key := range config.Keys {
		if keyID == "" || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("actor context trust key is invalid")
		}
		keys[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	return &Verifier{workloadID: config.WorkloadID, maxBytes: config.MaxBytes, maxRoles: config.MaxRoles, maxCaps: config.MaxCaps, skew: config.ClockSkew, keys: keys}, nil
}

func (v *Verifier) Verify(raw []byte, tenantID, commandID string, now time.Time) (Identity, error) {
	if len(raw) == 0 || len(raw) > v.maxBytes || tenantID == "" || now.IsZero() {
		return Identity{}, errors.New("actor context is missing or outside configured bounds")
	}
	var proof authv1.SignedActorContext
	if err := proto.Unmarshal(raw, &proof); err != nil {
		return Identity{}, errors.New("actor context is malformed")
	}
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&proof)
	if err != nil || !bytes.Equal(canonical, raw) || proof.GetSchemaVersion() != schemaVersion || proof.GetTenantId() != tenantID || proof.GetActorId() == "" || proof.GetAudienceWorkloadId() != v.workloadID || proof.GetSigningKeyId() == "" || (commandID != "" && proof.GetCommandId() != commandID) || len(proof.GetRoles()) > v.maxRoles || len(proof.GetCapabilities()) > v.maxCaps || !canonicalSet(proof.GetRoles()) || !canonicalSet(proof.GetCapabilities()) {
		return Identity{}, errors.New("actor context scope is invalid")
	}
	nowMS := uint64(now.UnixMilli())
	skewMS := uint64(v.skew.Milliseconds())
	issued, expires := proof.GetIssuedAtEpochMs(), proof.GetExpiresAtEpochMs()
	if issued > addSaturated(nowMS, skewMS) || nowMS >= addSaturated(expires, skewMS) || issued >= expires || expires-issued > uint64((5*time.Minute).Milliseconds()) {
		return Identity{}, errors.New("actor context is outside its validity window")
	}
	hash, signature := append([]byte(nil), proof.GetContentHash()...), append([]byte(nil), proof.GetSignature()...)
	proof.ContentHash, proof.Signature = nil, nil
	unsigned, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&proof)
	if err != nil {
		return Identity{}, errors.New("actor context cannot be canonicalized")
	}
	digest := sha256.Sum256(unsigned)
	key := v.keys[proof.GetSigningKeyId()]
	if !bytes.Equal(hash, digest[:]) || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, digest[:], signature) {
		return Identity{}, errors.New("actor context signature is invalid")
	}
	return Identity{TenantID: proof.GetTenantId(), ActorID: proof.GetActorId(), Roles: append([]string(nil), proof.GetRoles()...), Capabilities: append([]string(nil), proof.GetCapabilities()...), RevokeEpoch: proof.GetRevokeEpoch(), ExpiresAt: time.UnixMilli(int64(expires)).UTC()}, nil
}

func canonicalSet(values []string) bool {
	if !sort.StringsAreSorted(values) {
		return false
	}
	for i, value := range values {
		if value == "" || (i > 0 && values[i-1] == value) {
			return false
		}
	}
	return true
}

func addSaturated(left, right uint64) uint64 {
	result := left + right
	if result < left {
		return ^uint64(0)
	}
	return result
}
