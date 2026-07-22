package gateway

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"os"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"google.golang.org/protobuf/proto"
)

type workloadSigner struct {
	id, keyID string
	key       ed25519.PrivateKey
	ttl       time.Duration
}

func newWorkloadSigner(value config.Workload) (*workloadSigner, error) {
	data, err := os.ReadFile(value.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	var key ed25519.PrivateKey
	switch len(data) {
	case ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(data)
	case ed25519.PrivateKeySize:
		key = ed25519.PrivateKey(data)
	default:
		return nil, errors.New("workload key must contain a 32-byte seed or 64-byte key")
	}
	return &workloadSigner{id: value.ID, keyID: value.SigningKeyID, key: key, ttl: time.Duration(value.ProofTTLMS) * time.Millisecond}, nil
}
func (s *workloadSigner) sign(tenantID, commandID string, now time.Time) ([]byte, error) {
	proof := &authv1.SignedWorkloadContext{SchemaVersion: 1, TenantId: tenantID, WorkloadId: s.id, CommandId: commandID, IssuedAtEpochMs: uint64(now.UnixMilli()), ExpiresAtEpochMs: uint64(now.Add(s.ttl).UnixMilli()), SigningKeyId: s.keyID}
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(s.key, digest[:])
	return proto.MarshalOptions{Deterministic: true}.Marshal(proof)
}
