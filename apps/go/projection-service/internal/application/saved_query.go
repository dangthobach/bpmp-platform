package application

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type SavedQuery struct {
	TenantID, ID, Name, OwnerSubject string
	Version                          uint64
	Query                            ProcessQuery
	CreatedAt, UpdatedAt             time.Time
}

type SavedQueryStore interface {
	SaveQuery(context.Context, SavedQuery, uint64) (SavedQuery, error)
	ListSavedQueries(context.Context, string, string, time.Time, string, int) ([]SavedQuery, error)
	DeleteSavedQuery(context.Context, string, string, string, uint64) error
}

var savedQueryIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (s *Service) SaveQuery(ctx context.Context, item SavedQuery, expectedVersion uint64, credential QueryCredential) (SavedQuery, error) {
	store, ok := s.store.(SavedQueryStore)
	if !ok {
		return SavedQuery{}, fmt.Errorf("saved query store is unavailable")
	}
	if item.TenantID == "" || !savedQueryIDPattern.MatchString(item.ID) || strings.TrimSpace(item.Name) == "" || len([]byte(item.Name)) > 256 {
		return SavedQuery{}, invalidQuery("saved_query", "invalid_saved_query", "saved query id or name is invalid")
	}
	item.Query.TenantID, item.Query.Credential = item.TenantID, credential
	stored := item.Query
	_, requested, normalized, err := s.QueryInstances(ctx, item.Query)
	if err != nil {
		return SavedQuery{}, err
	}
	stored.Sort, stored.Limit = normalized.Sort, requested
	item.Query = stored
	item.Query.Credential = QueryCredential{}
	item.Query.Fields = nil
	item.Query.After = nil
	item.Query.AuthorizationPolicyVersion = ""
	item.Query.CursorSigningKeyID = ""
	policy, err := s.policy.Policy(item.TenantID)
	if err != nil {
		return SavedQuery{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: item.TenantID, Credential: credential, Policy: policy})
	if err != nil {
		return SavedQuery{}, err
	}
	if auth.SubjectID == "" {
		return SavedQuery{}, ErrQueryForbidden
	}
	item.OwnerSubject = auth.SubjectID
	return store.SaveQuery(queryCtx, item, expectedVersion)
}

func (s *Service) ListSavedQueries(ctx context.Context, tenant string, credential QueryCredential, afterTime time.Time, afterID string, limit int) ([]SavedQuery, int, string, error) {
	store, ok := s.store.(SavedQueryStore)
	if !ok {
		return nil, 0, "", fmt.Errorf("saved query store is unavailable")
	}
	policy, err := s.policy.Policy(tenant)
	if err != nil {
		return nil, 0, "", err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: tenant, Credential: credential, Policy: policy})
	if err != nil {
		return nil, 0, "", err
	}
	if auth.SubjectID == "" {
		return nil, 0, "", ErrQueryForbidden
	}
	if limit == 0 {
		limit = int(policy.QueryDefaultPageSize)
	}
	if limit < 1 || uint32(limit) > policy.QueryMaxPageSize {
		return nil, 0, "", invalidQuery("page_size", "page_limit", "page size exceeds tenant policy")
	}
	items, err := store.ListSavedQueries(queryCtx, tenant, auth.SubjectID, afterTime, afterID, limit+1)
	return items, limit, policy.QueryCursorSigningKeyID, err
}

func (s *Service) DeleteSavedQuery(ctx context.Context, tenant, id string, expected uint64, credential QueryCredential) error {
	store, ok := s.store.(SavedQueryStore)
	if !ok {
		return fmt.Errorf("saved query store is unavailable")
	}
	if !savedQueryIDPattern.MatchString(id) || expected == 0 {
		return invalidQuery("saved_query", "invalid_delete", "id and expected version are required")
	}
	policy, err := s.policy.Policy(tenant)
	if err != nil {
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: tenant, Credential: credential, Policy: policy})
	if err != nil {
		return err
	}
	if auth.SubjectID == "" {
		return ErrQueryForbidden
	}
	return store.DeleteSavedQuery(queryCtx, tenant, id, auth.SubjectID, expected)
}
