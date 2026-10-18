package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type EventSort struct {
	Field     EventField    `json:"field"`
	Direction SortDirection `json:"direction"`
}

type EventQuery struct {
	TenantID, AuthorizationPolicyVersion string
	Filter                               *EventExpression
	ProcessFilter                        *ProcessExpression
	Sort                                 []EventSort
	Limit                                int
	After                                *QueryCursor
	Credential                           QueryCredential
	Fields                               *FieldAuthorization
	CursorSigningKeyID                   string
}

type EventQueryStore interface {
	QueryEvents(context.Context, EventQuery) ([]WorkflowEvent, error)
}

func (q EventQuery) Fingerprint() (string, error) {
	copy := q
	copy.Limit, copy.After, copy.Credential, copy.Fields, copy.CursorSigningKeyID = 0, nil, QueryCredential{}, nil, ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func EventCursorFor(query EventQuery, event WorkflowEvent) (*QueryCursor, error) {
	hash, err := query.Fingerprint()
	if err != nil {
		return nil, err
	}
	values := make([]QueryValue, 0, len(query.Sort)+1)
	for _, sort := range query.Sort {
		switch sort.Field {
		case EventSequence:
			values = append(values, QueryValue{Kind: ValueInteger, Integer: int64(event.Sequence)})
		case EventOccurredAt:
			values = append(values, QueryValue{Kind: ValueTimestamp, Time: event.OccurredAt})
		case EventType:
			values = append(values, QueryValue{Kind: ValueString, String: event.EventType})
		case EventNodeID:
			values = append(values, QueryValue{Kind: ValueString, String: event.NodeID})
		case EventActorID:
			values = append(values, QueryValue{Kind: ValueString, String: event.ActorID})
		case EventConfigVersion:
			values = append(values, QueryValue{Kind: ValueString, String: event.ConfigVersion})
		case EventPolicyVersion:
			values = append(values, QueryValue{Kind: ValueString, String: event.PolicyVersion})
		default:
			return nil, ErrInvalidQuery
		}
	}
	values = append(values, QueryValue{Kind: ValueString, String: event.InstanceID})
	return &QueryCursor{QueryHash: hash, Values: values}, nil
}

func (s *Service) QueryEvents(ctx context.Context, query EventQuery) ([]WorkflowEvent, int, EventQuery, error) {
	store, ok := s.store.(EventQueryStore)
	if !ok {
		return nil, 0, query, fmt.Errorf("event query store is unavailable")
	}
	if query.TenantID == "" {
		return nil, 0, query, invalidQuery("tenant_id", "required", "tenant is required")
	}
	policy, err := s.policy.Policy(query.TenantID)
	if err != nil {
		return nil, 0, query, err
	}
	if err = validateQueryPolicy(policy); err != nil {
		return nil, 0, query, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: query.TenantID, Credential: query.Credential, Policy: policy})
	if err != nil {
		return nil, 0, query, err
	}
	if auth.SubjectID == "" || auth.PolicyVersion == "" {
		return nil, 0, query, ErrQueryForbidden
	}
	query.AuthorizationPolicyVersion, query.Fields, query.CursorSigningKeyID = auth.PolicyVersion, auth.Fields, policy.QueryCursorSigningKeyID
	query.ProcessFilter = intersectFilters(query.ProcessFilter, auth.Filter)
	if query.Limit == 0 {
		query.Limit = int(policy.QueryDefaultPageSize)
	}
	if query.Limit < 1 || uint32(query.Limit) > policy.QueryMaxPageSize {
		return nil, 0, query, invalidQuery("page_size", "page_limit", "page size exceeds tenant policy")
	}
	if len(query.Sort) == 0 {
		query.Sort = []EventSort{{Field: EventSequence, Direction: SortDescending}}
	}
	if uint32(len(query.Sort)) > policy.QueryMaxSortFields {
		return nil, 0, query, invalidQuery("sort", "sort_limit", "sort count exceeds tenant policy")
	}
	seen := map[EventField]bool{}
	for i, sort := range query.Sort {
		if sort.Field < EventType || sort.Field > EventPolicyVersion || (sort.Direction != SortAscending && sort.Direction != SortDescending) || seen[sort.Field] {
			return nil, 0, query, invalidQuery(fmt.Sprintf("sort[%d]", i), "invalid_sort", "event sort is invalid or duplicated")
		}
		seen[sort.Field] = true
	}
	budget := queryBudget{}
	if query.Filter != nil {
		if err = normalizeEventExpression(query.Filter); err != nil {
			return nil, 0, query, err
		}
		if err = validateEventExpression(*query.Filter, 1, policy, &budget); err != nil {
			return nil, 0, query, err
		}
	}
	if query.ProcessFilter != nil {
		if err = normalizeProcessExpression(query.ProcessFilter); err != nil {
			return nil, 0, query, err
		}
		if err = validateProcessExpression(*query.ProcessFilter, 1, policy, &budget); err != nil {
			return nil, 0, query, err
		}
	}
	if query.Fields != nil {
		allowed := map[string]bool{}
		for _, name := range query.Fields.QueryFields {
			allowed[name] = true
		}
		if err = validateEventFieldAccess(query.Filter, allowed); err != nil {
			return nil, 0, query, err
		}
		if err = validateFieldAccess(ProcessQuery{Filter: query.ProcessFilter, Fields: query.Fields, Sort: []ProcessSort{{Field: ProcessInstanceID, Direction: SortAscending}}, Scope: QueryScopeAll}); err != nil {
			return nil, 0, query, err
		}
		for _, sort := range query.Sort {
			names := map[EventField]string{EventType: "event_type", EventNodeID: "event_node_id", EventActorID: "event_actor_id", EventSequence: "event_sequence", EventOccurredAt: "event_occurred_at", EventConfigVersion: "event_config_version", EventPolicyVersion: "event_policy_version"}
			if !allowed[names[sort.Field]] {
				return nil, 0, query, ErrQueryForbidden
			}
		}
	}
	hash, err := query.Fingerprint()
	if err != nil {
		return nil, 0, query, err
	}
	if query.After != nil && (query.After.QueryHash != hash || len(query.After.Values) != len(query.Sort)+1) {
		return nil, 0, query, invalidQuery("page_token", "cursor_mismatch", "page token does not match the query")
	}
	requested := query.Limit
	query.Limit++
	events, err := store.QueryEvents(queryCtx, query)
	return events, requested, query, err
}
