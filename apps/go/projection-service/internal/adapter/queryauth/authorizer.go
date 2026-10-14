package queryauth

import (
	"context"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
)

type Config struct {
	MaxCapabilities int
}

type Authorizer struct {
	verifier        *jwtauth.Verifier
	maxCapabilities int
	now             func() time.Time
	pdp             PDP
	epochs          EpochStore
}
type EpochStore interface {
	Check(context.Context, string, string, uint64) error
}

func WithDurableRevoke(config Config, verifier *jwtauth.Verifier, pdp PDP, epochs EpochStore) (*Authorizer, error) {
	if epochs == nil {
		return nil, errors.New("durable revoke store required")
	}
	a, err := WithPDP(config, verifier, pdp)
	if err != nil {
		return nil, err
	}
	a.epochs = epochs
	return a, nil
}

func WithPDP(config Config, verifier *jwtauth.Verifier, pdp PDP) (*Authorizer, error) {
	if pdp == nil {
		return nil, errors.New("query PDP required")
	}
	authorizer, err := New(config, verifier)
	if err != nil {
		return nil, err
	}
	authorizer.pdp = pdp
	return authorizer, nil
}

func New(config Config, verifier *jwtauth.Verifier) (*Authorizer, error) {
	if verifier == nil || config.MaxCapabilities <= 0 {
		return nil, errors.New("projection query authorizer configuration is invalid")
	}
	return &Authorizer{
		verifier: verifier, maxCapabilities: config.MaxCapabilities,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (a *Authorizer) AuthorizeQuery(
	ctx context.Context,
	request application.QueryAuthorizationRequest,
) (application.QueryAuthorization, error) {
	if request.Credential.Type != application.ActorProofOriginalJWT ||
		len(request.Credential.SignedProof) == 0 || request.TenantID == "" ||
		request.Policy.PolicyVersion == "" {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	now := a.now()
	identity, err := a.verifier.Verify(
		string(request.Credential.SignedProof), request.TenantID, now,
	)
	if err != nil || len(identity.Capabilities) > a.maxCapabilities {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	requiredEpoch := max(request.Policy.QueryMinRevokeEpoch, request.Policy.QueryActorRevokeEpochs[identity.Subject])
	if identity.RevokeEpoch < requiredEpoch || request.Policy.QueryMaxTokenAge <= 0 || identity.IssuedAt.IsZero() || now.Sub(identity.IssuedAt) > request.Policy.QueryMaxTokenAge {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	capabilities, valid := capabilitySet(identity.Capabilities)
	if a.epochs != nil {
		if err := a.epochs.Check(ctx, request.TenantID, identity.Subject, identity.RevokeEpoch); err != nil {
			return application.QueryAuthorization{}, err
		}
	}
	if !valid {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	if a.pdp != nil {
		_, all := capabilities[request.Policy.QueryAllCapability]
		_, own := capabilities[request.Policy.QueryOwnCapability]
		if !all && !own {
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
		authorization, err := a.pdp.Evaluate(ctx, request, identity.Subject)
		if err != nil {
			return application.QueryAuthorization{}, err
		}
		if !all {
			ceiling := application.ProcessExpression{Predicate: &application.ProcessPredicate{Field: application.ProcessInitiatorActorID, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: identity.Subject}}}}
			if authorization.Filter == nil {
				authorization.Filter = &ceiling
			} else {
				authorization.Filter = &application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{ceiling, *authorization.Filter}}
			}
		}
		return authorization, nil
	}
	authorization := application.QueryAuthorization{
		SubjectID: identity.Subject, PolicyVersion: request.Policy.PolicyVersion,
	}
	if _, allowed := capabilities[request.Policy.QueryAllCapability]; allowed {
		return authorization, nil
	}
	if _, allowed := capabilities[request.Policy.QueryOwnCapability]; !allowed {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	authorization.Filter = &application.ProcessExpression{
		Predicate: &application.ProcessPredicate{
			Field: application.ProcessInitiatorActorID, Operator: application.CompareEqual,
			Values: []application.QueryValue{{Kind: application.ValueString, String: identity.Subject}},
		},
	}
	return authorization, nil
}

func capabilitySet(values []string) (map[string]struct{}, bool) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return nil, false
		}
		if _, duplicate := result[value]; duplicate {
			return nil, false
		}
		result[value] = struct{}{}
	}
	return result, true
}

var _ application.QueryAuthorizer = (*Authorizer)(nil)
