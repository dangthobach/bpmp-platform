package workloadsecurity

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"google.golang.org/protobuf/proto"
)

type scopes struct{}

func (scopes) KeyScope(context.Context, string) (string, error) { return "tenant-a/workflow", nil }

func TestProviderSignsCommandBoundWorkloadProof(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	now := time.Unix(100, 0).UTC()
	provider, err := New(Config{WorkloadID: "human-runtime", SigningKeyID: "workload-1", PrivateKey: privateKey, ProofTTL: time.Minute}, scopes{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.ForTenant(context.Background(), "tenant-a", "command-1")
	if err != nil {
		t.Fatal(err)
	}
	var proof authv1.SignedWorkloadContext
	if err = proto.Unmarshal(snapshot.WorkloadProof, &proof); err != nil {
		t.Fatal(err)
	}
	signature := append([]byte(nil), proof.Signature...)
	proof.ContentHash = nil
	proof.Signature = nil
	unsigned, _ := proto.MarshalOptions{Deterministic: true}.Marshal(&proof)
	digest := sha256.Sum256(unsigned)
	if proof.CommandId != "command-1" || !ed25519.Verify(privateKey.Public().(ed25519.PublicKey), digest[:], signature) {
		t.Fatal("proof is not bound to the requested command")
	}
}
