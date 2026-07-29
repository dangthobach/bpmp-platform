package gateway

import (
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
)

type actorIdentity struct{ ID string }

type verifier struct {
	value *jwtauth.Verifier
}

func newVerifier(value config.Identity) (*verifier, error) {
	shared, err := jwtauth.New(jwtauth.Config{
		JWKSPath: value.JWKSPath, Issuers: value.Issuers,
		Audiences: value.Audiences, Algorithms: value.Algorithms,
		MaxTokenBytes: value.MaxTokenBytes, MaxJWKSKeys: value.MaxJWKSKeys,
		ClockSkew: time.Duration(value.ClockSkewSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &verifier{value: shared}, nil
}

func (v *verifier) verify(raw, tenantID string, now time.Time) (actorIdentity, error) {
	identity, err := v.value.Verify(raw, tenantID, now)
	if err != nil {
		return actorIdentity{}, err
	}
	return actorIdentity{ID: identity.Subject}, nil
}
