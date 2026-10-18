package application

import (
	"context"
	"fmt"
	"regexp"
)

type AggregateOperation int32

const (
	AggregateCount AggregateOperation = iota + 1
	AggregateSum
	AggregateAverage
	AggregateMin
	AggregateMax
)

type AggregateMetric struct {
	Alias     string
	Operation AggregateOperation
	Field     ProcessField
}

type AggregateQuery struct {
	TenantID, AuthorizationPolicyVersion string
	Scope                                QueryScope
	Filter                               *ProcessExpression
	GroupBy                              []ProcessField
	Metrics                              []AggregateMetric
	Limit                                int
	Credential                           QueryCredential
	Fields                               *FieldAuthorization
}

type AggregateCell struct {
	Name  string
	Value QueryValue
}

type AggregateRow struct {
	Dimensions []AggregateCell
	Metrics    []AggregateCell
}

type AggregateStore interface {
	AggregateProcesses(context.Context, AggregateQuery) ([]AggregateRow, error)
}

var aggregateAliasPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func (s *Service) AggregateProcesses(ctx context.Context, query AggregateQuery) ([]AggregateRow, AggregateQuery, error) {
	store, ok := s.store.(AggregateStore)
	if !ok {
		return nil, query, fmt.Errorf("aggregate store is unavailable")
	}
	if query.TenantID == "" || query.Scope < QueryScopeAll || query.Scope > QueryScopeDeleted {
		return nil, query, invalidQuery("scope", "required", "tenant and query scope are required")
	}
	policy, err := s.policy.Policy(query.TenantID)
	if err != nil {
		return nil, query, err
	}
	if err = validateQueryPolicy(policy); err != nil {
		return nil, query, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	authorization, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: query.TenantID, Credential: query.Credential, Policy: policy})
	if err != nil {
		return nil, query, err
	}
	if authorization.SubjectID == "" || authorization.PolicyVersion == "" {
		return nil, query, ErrQueryForbidden
	}
	query.AuthorizationPolicyVersion, query.Fields = authorization.PolicyVersion, authorization.Fields
	query.Filter = intersectFilters(query.Filter, authorization.Filter)
	if query.Limit == 0 {
		query.Limit = int(policy.QueryDefaultPageSize)
	}
	if query.Limit < 1 || uint32(query.Limit) > policy.QueryMaxPageSize {
		return nil, query, invalidQuery("limit", "aggregate_limit", "aggregate row limit exceeds tenant policy")
	}
	if len(query.GroupBy) == 0 || uint32(len(query.GroupBy)) > policy.QueryMaxSortFields {
		return nil, query, invalidQuery("group_by", "group_limit", "one or more bounded group fields are required")
	}
	if len(query.Metrics) == 0 || uint32(len(query.Metrics)) > policy.QueryMaxPredicates {
		return nil, query, invalidQuery("metrics", "metric_limit", "one or more bounded aggregate metrics are required")
	}
	allowed := func(field ProcessField) bool {
		name := ProcessFieldName(field)
		return validProcessField(field) && (query.Fields == nil || (containsField(query.Fields.QueryFields, name) && query.Fields.CanRead(name)))
	}
	seenGroups := map[ProcessField]struct{}{}
	for index, field := range query.GroupBy {
		if !allowed(field) {
			return nil, query, ErrQueryForbidden
		}
		if _, duplicate := seenGroups[field]; duplicate {
			return nil, query, invalidQuery(fmt.Sprintf("group_by[%d]", index), "duplicate_group", "group field is duplicated")
		}
		seenGroups[field] = struct{}{}
	}
	seenAliases := map[string]struct{}{}
	for index, metric := range query.Metrics {
		if !aggregateAliasPattern.MatchString(metric.Alias) {
			return nil, query, invalidQuery(fmt.Sprintf("metrics[%d].alias", index), "invalid_alias", "metric alias is invalid")
		}
		if _, duplicate := seenAliases[metric.Alias]; duplicate {
			return nil, query, invalidQuery(fmt.Sprintf("metrics[%d].alias", index), "duplicate_alias", "metric alias is duplicated")
		}
		seenAliases[metric.Alias] = struct{}{}
		if metric.Operation < AggregateCount || metric.Operation > AggregateMax || (metric.Field != 0 && !allowed(metric.Field)) {
			return nil, query, invalidQuery(fmt.Sprintf("metrics[%d]", index), "invalid_metric", "aggregate metric is unsupported")
		}
		if metric.Operation != AggregateCount && metric.Field == 0 {
			return nil, query, invalidQuery(fmt.Sprintf("metrics[%d].field", index), "required", "aggregate field is required")
		}
		if (metric.Operation == AggregateSum || metric.Operation == AggregateAverage) && processFieldKind(metric.Field) != ValueInteger {
			return nil, query, invalidQuery(fmt.Sprintf("metrics[%d].field", index), "metric_type", "SUM and AVG require an integer field")
		}
	}
	if query.Filter != nil {
		if err = normalizeProcessExpression(query.Filter); err != nil {
			return nil, query, err
		}
		budget := queryBudget{}
		if err = validateProcessExpression(*query.Filter, 1, policy, &budget); err != nil {
			return nil, query, err
		}
		if err = validateProcessFilterFieldAccess(query.Filter, query.Fields); err != nil {
			return nil, query, err
		}
	}
	rows, err := store.AggregateProcesses(queryCtx, query)
	return rows, query, err
}
