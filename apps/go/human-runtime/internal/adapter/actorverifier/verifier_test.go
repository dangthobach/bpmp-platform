package actorverifier

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/proto"
)

func TestVerifierAcceptsBothProofTypesAndEnforcesRevokeEpoch(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier := newTestVerifier(t, publicKey)
	now := time.Unix(1_000, 0).UTC()
	identity, err := verifier.VerifyActor(context.Background(), application.ActorVerificationRequest{
		TenantID: "tenant-a", EvaluatedAt: now,
		Credential: application.ActorCredential{OriginalSignedToken: signedJWT(t, privateKey, now)},
	})
	if err != nil || identity.ActorID != "alice" {
		t.Fatalf("JWT verification failed: %#v err=%v", identity, err)
	}
	internalProof := signedInternalContext(t, privateKey, now)
	identity, err = verifier.VerifyActor(context.Background(), application.ActorVerificationRequest{
		TenantID: "tenant-a", CommandID: "command-1", EvaluatedAt: now,
		Credential: application.ActorCredential{SignedActorContext: internalProof},
	})
	if err != nil || identity.ActorID != "alice" {
		t.Fatalf("internal context verification failed: %#v err=%v", identity, err)
	}
	verifier.revocations.(*MemoryRevokeEpochs).Set("tenant-a", "alice", 3)
	if _, err = verifier.VerifyActor(context.Background(), application.ActorVerificationRequest{
		TenantID: "tenant-a", CommandID: "command-1", EvaluatedAt: now,
		Credential: application.ActorCredential{SignedActorContext: internalProof},
	}); err == nil {
		t.Fatal("revoked actor proof was accepted")
	}
}

func TestSignedContextRejectsCommandMismatchAndTampering(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	verifier := newTestVerifier(t, publicKey)
	now := time.Unix(1_000, 0).UTC()
	proof := signedInternalContext(t, privateKey, now)
	request := application.ActorVerificationRequest{TenantID: "tenant-a", CommandID: "different", EvaluatedAt: now, Credential: application.ActorCredential{SignedActorContext: proof}}
	if _, err := verifier.VerifyActor(context.Background(), request); err == nil {
		t.Fatal("command scope mismatch was accepted")
	}
	proof[len(proof)-1] ^= 1
	request.CommandID = "command-1"
	request.Credential.SignedActorContext = proof
	if _, err := verifier.VerifyActor(context.Background(), request); err == nil {
		t.Fatal("tampered signed context was accepted")
	}
}

func newTestVerifier(t *testing.T, publicKey ed25519.PublicKey) *Verifier {
	t.Helper()
	jwks := []byte(fmt.Sprintf(`{"keys":[{"kty":"OKP","kid":"jwt-key","alg":"EdDSA","crv":"Ed25519","x":"%s"}]}`, base64.RawURLEncoding.EncodeToString(publicKey)))
	revocations := NewMemoryRevokeEpochs()
	verifier, err := New(Config{
		Issuers: map[string]struct{}{"https://identity.example": {}}, Audiences: map[string]struct{}{"bpmp": {}},
		AllowedJWTMethods: map[string]struct{}{jwt.SigningMethodEdDSA.Alg(): {}}, WorkloadID: "human-runtime",
		MaxProofBytes: 8192, MaxJWKSKeys: 8, MaxRoles: 16, MaxCapabilities: 32, ClockSkew: time.Second,
	}, jwks, map[string]ed25519.PublicKey{"internal-key": publicKey}, revocations)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func signedJWT(t *testing.T, privateKey ed25519.PrivateKey, now time.Time) []byte {
	t.Helper()
	claims := actorClaims{
		TenantID: "tenant-a", Roles: []string{"reviewers"}, RevokeEpoch: 2,
		RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://identity.example", Subject: "alice", Audience: jwt.ClaimStrings{"bpmp"}, IssuedAt: jwt.NewNumericDate(now.Add(-time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "jwt-key"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(signed)
}

func signedInternalContext(t *testing.T, privateKey ed25519.PrivateKey, now time.Time) []byte {
	t.Helper()
	proof := &authv1.SignedActorContext{
		SchemaVersion: signedContextSchemaVersion, TenantId: "tenant-a", ActorId: "alice",
		Roles: []string{"reviewers"}, Capabilities: []string{"task.complete"}, RevokeEpoch: 2,
		IssuedAtEpochMs: uint64(now.Add(-time.Second).UnixMilli()), ExpiresAtEpochMs: uint64(now.Add(time.Minute).UnixMilli()),
		AudienceWorkloadId: "human-runtime", CommandId: "command-1", SigningKeyId: "internal-key",
	}
	sort.Strings(proof.Roles)
	sort.Strings(proof.Capabilities)
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(privateKey, digest[:])
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
