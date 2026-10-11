package queryauth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
)

func TestAuthorizerProducesTypedRowFilter(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	authorizer, private := testAuthorizer(t, now)
	policy := testPolicy()

	ownToken := signToken(t, private, now, "tenant-a", "actor-1", []string{policy.QueryOwnCapability})
	authorization, err := authorizer.AuthorizeQuery(context.Background(), application.QueryAuthorizationRequest{
		TenantID: "tenant-a", Policy: policy,
		Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: []byte(ownToken)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorization.SubjectID != "actor-1" || authorization.PolicyVersion != policy.PolicyVersion ||
		authorization.Filter == nil || authorization.Filter.Predicate == nil ||
		authorization.Filter.Predicate.Field != application.ProcessInitiatorActorID ||
		authorization.Filter.Predicate.Values[0].String != "actor-1" {
		t.Fatalf("typed ownership filter is invalid: %+v", authorization)
	}

	allToken := signToken(t, private, now, "tenant-a", "actor-2", []string{policy.QueryAllCapability})
	authorization, err = authorizer.AuthorizeQuery(context.Background(), application.QueryAuthorizationRequest{
		TenantID: "tenant-a", Policy: policy,
		Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: []byte(allToken)},
	})
	if err != nil || authorization.Filter != nil {
		t.Fatalf("all capability should not add a row filter: %+v, %v", authorization, err)
	}
}

func TestAuthorizerFailsClosed(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	authorizer, private := testAuthorizer(t, now)
	policy := testPolicy()
	token := signToken(t, private, now, "tenant-a", "actor-1", []string{"unrelated"})
	cases := []application.QueryAuthorizationRequest{
		{TenantID: "tenant-a", Policy: policy, Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: []byte(token)}},
		{TenantID: "tenant-b", Policy: policy, Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: []byte(token)}},
		{TenantID: "tenant-a", Policy: policy, Credential: application.QueryCredential{Type: application.ActorProofSignedInternalContext, SignedProof: []byte(token)}},
	}
	for _, request := range cases {
		if _, err := authorizer.AuthorizeQuery(context.Background(), request); !errors.Is(err, application.ErrQueryForbidden) {
			t.Fatalf("request did not fail closed: %+v, %v", request, err)
		}
	}
}

func TestAuthorizerEnforcesRevocationAndFreshness(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	authorizer, private := testAuthorizer(t, now)
	token := signToken(t, private, now, "tenant-a", "actor-1", []string{"workflow.query.all"})
	base := testPolicy()
	cases := []application.RuntimePolicy{base, base, base}
	cases[0].QueryMinRevokeEpoch = 1
	cases[1].QueryActorRevokeEpochs = map[string]uint64{"actor-1": 1}
	cases[2].QueryMaxTokenAge = 30 * time.Second
	for _, policy := range cases {
		_, err := authorizer.AuthorizeQuery(context.Background(), application.QueryAuthorizationRequest{
			TenantID: "tenant-a", Policy: policy, Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: []byte(token)},
		})
		if !errors.Is(err, application.ErrQueryForbidden) {
			t.Fatalf("stale/revoked token accepted: %v", err)
		}
	}
}

func testAuthorizer(t *testing.T, now time.Time) (*Authorizer, ed25519.PrivateKey) {
	t.Helper()
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
	verifier, err := jwtauth.NewFromJWKS(jwtauth.Config{
		Issuers: []string{"issuer"}, Audiences: []string{"projection"},
		Algorithms: []string{"EdDSA"}, MaxTokenBytes: 4096, MaxJWKSKeys: 2,
	}, rawJWKS)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := New(Config{MaxCapabilities: 8}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	authorizer.now = func() time.Time { return now }
	return authorizer, private
}

func TestAuthorizerRejectsMissingIssuedAt(t *testing.T) {
	now := time.Now().UTC()
	authorizer, private := testAuthorizer(t, now)
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "issuer", "sub": "actor-1", "aud": []string{"projection"},
		"tenant_id": "tenant-a", "capabilities": []string{"workflow.query.all"},
		"revoke_epoch": 0, "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "key-1"
	raw, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	_, err = authorizer.AuthorizeQuery(context.Background(), application.QueryAuthorizationRequest{
		TenantID: "tenant-a", Policy: testPolicy(),
		Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: []byte(raw)},
	})
	if !errors.Is(err, application.ErrQueryForbidden) {
		t.Fatalf("token without iat accepted: %v", err)
	}
}

func signToken(t *testing.T, private ed25519.PrivateKey, now time.Time, tenant, subject string, capabilities []string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "issuer", "sub": subject, "aud": []string{"projection"},
		"tenant_id": tenant, "capabilities": capabilities, "revoke_epoch": 0,
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func testPolicy() application.RuntimePolicy {
	return application.RuntimePolicy{
		QueryMaxTokenAge: time.Hour,
		PolicyVersion:    "policy-v1", QueryAllCapability: "workflow.query.all",
		QueryOwnCapability: "workflow.query.own",
	}
}
