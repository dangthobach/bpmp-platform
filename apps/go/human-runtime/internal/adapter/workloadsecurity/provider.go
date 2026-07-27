package workloadsecurity

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/enginegrpc"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"google.golang.org/protobuf/proto"
)

const proofSchemaVersion = 1

type TenantKeyScope interface {
	KeyScope(context.Context, string) (string, error)
}

type Config struct {
	WorkloadID   string
	SigningKeyID string
	PrivateKey   ed25519.PrivateKey
	ProofTTL     time.Duration
}

type Provider struct {
	config Config
	scopes TenantKeyScope
}

func New(config Config, scopes TenantKeyScope) (*Provider, error) {
	if config.WorkloadID == "" || config.SigningKeyID == "" || len(config.PrivateKey) != ed25519.PrivateKeySize || config.ProofTTL <= 0 || scopes == nil {
		return nil, errors.New("workload security configuration is incomplete")
	}
	return &Provider{config: config, scopes: scopes}, nil
}

func (p *Provider) ForTenant(ctx context.Context, tenantID, commandID string, evaluatedAt time.Time) (enginegrpc.SecuritySnapshot, error) {
	if tenantID == "" || commandID == "" || evaluatedAt.IsZero() {
		return enginegrpc.SecuritySnapshot{}, errors.New("tenant, command, and evaluation time are required")
	}
	keyScope, err := p.scopes.KeyScope(ctx, tenantID)
	if err != nil {
		return enginegrpc.SecuritySnapshot{}, err
	}
	issuedAt := evaluatedAt.UTC()
	expiresAt := issuedAt.Add(p.config.ProofTTL)
	proof := &authv1.SignedWorkloadContext{
		SchemaVersion:    proofSchemaVersion,
		TenantId:         tenantID,
		WorkloadId:       p.config.WorkloadID,
		CommandId:        commandID,
		IssuedAtEpochMs:  uint64(issuedAt.UnixMilli()),
		ExpiresAtEpochMs: uint64(expiresAt.UnixMilli()),
		SigningKeyId:     p.config.SigningKeyID,
	}
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		return enginegrpc.SecuritySnapshot{}, err
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(p.config.PrivateKey, digest[:])
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		return enginegrpc.SecuritySnapshot{}, err
	}
	return enginegrpc.SecuritySnapshot{EncryptionKeyScope: keyScope, WorkloadProof: encoded}, nil
}

var _ enginegrpc.SecurityProvider = (*Provider)(nil)
