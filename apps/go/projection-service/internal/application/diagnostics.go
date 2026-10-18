package application

import (
	"context"
	"fmt"
)

type QueryPlanStore interface {
	ExplainQueryPlan(context.Context, ProcessQuery, bool) (string, error)
}

func (s *Service) ExplainQueryPlan(ctx context.Context, query ProcessQuery, analyze bool) (string, string, error) {
	store, ok := s.store.(QueryPlanStore)
	if !ok {
		return "", "", fmt.Errorf("query plan store is unavailable")
	}
	if query.TenantID == "" {
		return "", "", invalidQuery("tenant_id", "required", "tenant is required")
	}
	policy, err := s.policy.Policy(query.TenantID)
	if err != nil {
		return "", "", err
	}
	if policy.QueryDiagnosticsCapability == "" {
		return "", "", ErrQueryForbidden
	}
	auth, err := s.authorizer.AuthorizeQuery(ctx, QueryAuthorizationRequest{TenantID: query.TenantID, Credential: query.Credential, Policy: policy})
	if err != nil {
		return "", "", err
	}
	allowed := false
	for _, capability := range auth.Capabilities {
		if capability == policy.QueryDiagnosticsCapability {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", "", ErrQueryForbidden
	}
	queryCtx,cancel,normalized,limit,err:=s.prepareProcessQuery(ctx,query)
	if err != nil {
		return "", "", err
	}
	defer cancel()
	normalized.Limit = limit
	normalized.ExpandEvents = nil
	fingerprint, err := normalized.Fingerprint()
	if err != nil {
		return "", "", err
	}
	plan, err := store.ExplainQueryPlan(queryCtx, normalized, analyze)
	return plan, fingerprint, err
}
