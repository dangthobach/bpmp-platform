package application

import (
	"context"
	"fmt"
)

const QuerySchemaVersion = "bpmp.projection.query.v1"

type FieldCapability struct {
	Field             ProcessField
	Kind              ValueKind
	Filterable        bool
	Sortable          bool
	Selectable        bool
	Operators         []Comparison
	TextTransforms    []TextTransform
	NumericTransforms []NumericTransform
	DateTransforms    []DateTransform
}

type QueryCapabilities struct {
	SchemaVersion string
	Policy        RuntimePolicy
	Fields        []FieldCapability
	EventFields   []EventFieldCapability
	Scopes        []QueryScope
	DefaultSort   []ProcessSort
}

type EventFieldCapability struct {
	Field             EventField
	Kind              ValueKind
	Operators         []Comparison
	TextTransforms    []TextTransform
	NumericTransforms []NumericTransform
	DateTransforms    []DateTransform
}

func (s *Service) GetQueryCapabilities(ctx context.Context, tenantID string, credential QueryCredential) (QueryCapabilities, error) {
	if tenantID == "" {
		return QueryCapabilities{}, fmt.Errorf("%w: tenant is required", ErrInvalidQuery)
	}
	policy, err := s.policy.Policy(tenantID)
	if err != nil {
		return QueryCapabilities{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	authorization, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{
		TenantID: tenantID, Credential: credential, Policy: policy,
	})
	if err != nil {
		return QueryCapabilities{}, err
	}
	if authorization.SubjectID == "" || authorization.PolicyVersion == "" {
		return QueryCapabilities{}, ErrQueryForbidden
	}
	capabilities := QueryCapabilities{
		SchemaVersion: QuerySchemaVersion,
		Policy:        policy,
		Scopes:        []QueryScope{QueryScopeAll, QueryScopeActive, QueryScopeCompleted, QueryScopeDeleted},
		DefaultSort: []ProcessSort{
			{Field: ProcessUpdatedAt, Direction: SortDescending},
			{Field: ProcessInstanceID, Direction: SortDescending},
		},
	}
	for field := ProcessInstanceID; field <= ProcessIsDeleted; field++ {
		name := ProcessFieldName(field)
		queryable := authorization.Fields == nil || containsField(authorization.Fields.QueryFields, name)
		readable := authorization.Fields == nil || authorization.Fields.CanRead(name)
		kind := processFieldKind(field)
		item := FieldCapability{
			Field: field, Kind: kind, Filterable: queryable, Sortable: queryable,
			Selectable: readable,
		}
		if queryable {
			item.Operators = operatorsForKind(kind)
			switch kind {
			case ValueString:
				item.TextTransforms = []TextTransform{TextTransformNone, TextTransformTrim, TextTransformLower, TextTransformUpper, TextTransformTrimLower, TextTransformTrimUpper}
			case ValueInteger:
				item.NumericTransforms = []NumericTransform{NumericTransformNone, NumericTransformAbs}
			case ValueTimestamp:
				item.DateTransforms = []DateTransform{DateTransformNone, DateTransformUTCDay, DateTransformUTCWeek, DateTransformUTCMonth, DateTransformUTCYear}
			}
		}
		capabilities.Fields = append(capabilities.Fields, item)
	}
	for field := EventType; field <= EventPolicyVersion; field++ {
		if authorization.Fields == nil || containsField(authorization.Fields.QueryFields, eventFieldName(field)) {
			kind := eventFieldKind(field)
			item := EventFieldCapability{Field: field, Kind: kind, Operators: operatorsForKind(kind)}
			switch kind {
			case ValueString:
				item.TextTransforms = []TextTransform{TextTransformNone, TextTransformTrim, TextTransformLower, TextTransformUpper, TextTransformTrimLower, TextTransformTrimUpper}
			case ValueInteger:
				item.NumericTransforms = []NumericTransform{NumericTransformNone, NumericTransformAbs}
			case ValueTimestamp:
				item.DateTransforms = []DateTransform{DateTransformNone, DateTransformUTCDay, DateTransformUTCWeek, DateTransformUTCMonth, DateTransformUTCYear}
			}
			capabilities.EventFields = append(capabilities.EventFields, item)
		}
	}
	return capabilities, nil
}

func operatorsForKind(kind ValueKind) []Comparison {
	result := make([]Comparison, 0, int(CompareNotBetween))
	for operator := CompareEqual; operator <= CompareNotBetween; operator++ {
		if operatorAllowed(kind, operator) {
			result = append(result, operator)
		}
	}
	return result
}

func containsField(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func eventFieldName(field EventField) string {
	return map[EventField]string{
		EventType: "event_type", EventNodeID: "event_node_id", EventActorID: "event_actor_id",
		EventSequence: "event_sequence", EventOccurredAt: "event_occurred_at",
		EventConfigVersion: "event_config_version", EventPolicyVersion: "event_policy_version",
	}[field]
}
