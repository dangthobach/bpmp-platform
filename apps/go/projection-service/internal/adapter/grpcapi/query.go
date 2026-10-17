package grpcapi

import (
	"context"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxWireExpressionDepth = 64

func (s *Server) GetQueryCapabilities(ctx context.Context, request *projectionv1.GetQueryCapabilitiesRequest) (*projectionv1.GetQueryCapabilitiesResponse, error) {
	if request == nil {
		return nil, mapError(application.ErrInvalidQuery)
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	capabilities, err := s.service.GetQueryCapabilities(ctx, request.GetTenantId(), credential)
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.GetQueryCapabilitiesResponse{
		SchemaVersion: capabilities.SchemaVersion,
		ConfigVersion: capabilities.Policy.ConfigVersion,
		PolicyVersion: capabilities.Policy.PolicyVersion,
		DefaultPageSize: capabilities.Policy.QueryDefaultPageSize,
		MaxPageSize: capabilities.Policy.QueryMaxPageSize,
		MaxFilterDepth: capabilities.Policy.QueryMaxFilterDepth,
		MaxPredicates: capabilities.Policy.QueryMaxPredicates,
		MaxSortFields: capabilities.Policy.QueryMaxSortFields,
		MaxRelationJoins: capabilities.Policy.QueryMaxRelationJoins,
		MaxInValues: capabilities.Policy.QueryMaxInValues,
		MaxStringBytes: capabilities.Policy.QueryMaxStringBytes,
		DefaultSort: toWireSorts(capabilities.DefaultSort),
	}
	for _, scope := range capabilities.Scopes {
		response.Scopes = append(response.Scopes, projectionv1.QueryScope(scope))
	}
	for _, field := range capabilities.Fields {
		item := &projectionv1.ProcessFieldCapability{
			Field: projectionv1.ProcessField(field.Field), ValueKind: projectionv1.QueryValueKind(field.Kind),
			Filterable: field.Filterable, Sortable: field.Sortable, Selectable: field.Selectable,
		}
		for _, operator := range field.Operators { item.Operators = append(item.Operators, projectionv1.ComparisonOperator(operator)) }
		for _, transform := range field.TextTransforms { item.TextTransforms = append(item.TextTransforms, projectionv1.TextTransform(transform)) }
		for _, transform := range field.NumericTransforms { item.NumericTransforms = append(item.NumericTransforms, projectionv1.NumericTransform(transform)) }
		for _, transform := range field.DateTransforms { item.DateTransforms = append(item.DateTransforms, projectionv1.DateTransform(transform)) }
		response.Fields = append(response.Fields, item)
	}
	for _, field := range capabilities.EventFields {
		item := &projectionv1.EventFieldCapability{Field: projectionv1.EventField(field.Field), ValueKind: projectionv1.QueryValueKind(field.Kind)}
		for _, operator := range field.Operators { item.Operators = append(item.Operators, projectionv1.ComparisonOperator(operator)) }
		for _, transform := range field.TextTransforms { item.TextTransforms = append(item.TextTransforms, projectionv1.TextTransform(transform)) }
		for _, transform := range field.NumericTransforms { item.NumericTransforms = append(item.NumericTransforms, projectionv1.NumericTransform(transform)) }
		for _, transform := range field.DateTransforms { item.DateTransforms = append(item.DateTransforms, projectionv1.DateTransform(transform)) }
		response.EventFields = append(response.EventFields, item)
	}
	return response, nil
}

func (s *Server) QueryCompletedProcesses(
	ctx context.Context,
	request *projectionv1.QueryCompletedProcessesRequest,
) (*projectionv1.QueryCompletedProcessesResponse, error) {
	query, err := fromWireQuery(request)
	if err != nil {
		return nil, mapError(err)
	}
	query.After, err = s.cursors.DecodeQuery(request.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	processes, limit, normalized, err := s.service.QueryCompleted(ctx, query)
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.QueryCompletedProcessesResponse{}
	if limit > 0 && len(processes) > limit {
		cursor, cursorErr := application.CursorFor(normalized, processes[limit-1])
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "build completed process cursor")
		}
		response.NextPageToken, cursorErr = s.cursors.EncodeQuery(normalized.CursorSigningKeyID, cursor)
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "encode completed process cursor")
		}
		processes = processes[:limit]
	}
	response.Processes = make([]*projectionv1.CompletedProcessReadModel, 0, len(processes))
	for _, process := range processes {
		instance, masked := toWireSelected(process.Instance, normalized.Fields, normalized.SelectedFields)
		row := &projectionv1.CompletedProcessReadModel{Instance: instance, MaskedFields: masked}
		if selectedAndReadable(normalized, application.ProcessDurationMS, "duration_ms") {
			row.DurationMs = process.DurationMS
		} else if normalized.Fields != nil && !normalized.Fields.CanRead("duration_ms") {
			row.MaskedFields = append(row.MaskedFields, "duration_ms")
		}
		if selectedAndReadable(normalized, application.ProcessEventCount, "event_count") {
			row.EventCount = process.EventCount
		} else if normalized.Fields != nil && !normalized.Fields.CanRead("event_count") {
			row.MaskedFields = append(row.MaskedFields, "event_count")
		}
		response.Processes = append(response.Processes, row)
	}
	response.NormalizedSort = toWireSorts(normalized.Sort)
	response.PolicyVersion = normalized.AuthorizationPolicyVersion
	response.QueryFingerprint, _ = normalized.Fingerprint()
	return response, nil
}

func fromWireQuery(request *projectionv1.QueryCompletedProcessesRequest) (application.ProcessQuery, error) {
	if request == nil {
		return application.ProcessQuery{}, application.ErrInvalidQuery
	}
	filter, err := fromWireProcessExpression(request.GetFilter(), 1)
	if err != nil {
		return application.ProcessQuery{}, err
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return application.ProcessQuery{}, err
	}
	query := application.ProcessQuery{
		TenantID: request.GetTenantId(), Filter: filter,
		IncludeDeleted: request.GetIncludeDeleted(), Limit: int(request.GetPageSize()),
		Credential: credential,
		Sort:       make([]application.ProcessSort, 0, len(request.GetSort())),
		SelectedFields: make([]application.ProcessField, 0, len(request.GetSelect())),
	}
	for _, field := range request.GetSelect() {
		query.SelectedFields = append(query.SelectedFields, application.ProcessField(field))
	}
	for _, sort := range request.GetSort() {
		if sort == nil {
			return application.ProcessQuery{}, application.ErrInvalidQuery
		}
		query.Sort = append(query.Sort, application.ProcessSort{
			Field:            application.ProcessField(sort.GetField()),
			Direction:        application.SortDirection(sort.GetDirection()),
			TextTransform:    application.TextTransform(sort.GetTextTransform()),
			Coalesce:         fromWireOptionalValue(sort.GetCoalesceValue()),
			NumericTransform: application.NumericTransform(sort.GetNumericTransform()),
			DateTransform:    application.DateTransform(sort.GetDateTransform()),
		})
	}
	return query, nil
}

func (s *Server) QueryProcesses(ctx context.Context, request *projectionv1.QueryProcessesRequest) (*projectionv1.QueryProcessesResponse, error) {
	query, err := fromWireGeneralQuery(request)
	if err != nil {
		return nil, mapError(err)
	}
	query.After, err = s.cursors.DecodeQuery(request.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	processes, limit, normalized, err := s.service.QueryInstances(ctx, query)
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.QueryProcessesResponse{
		NormalizedSort: toWireSorts(normalized.Sort),
		PolicyVersion: normalized.AuthorizationPolicyVersion,
	}
	response.QueryFingerprint, _ = normalized.Fingerprint()
	if limit > 0 && len(processes) > limit {
		cursor, cursorErr := application.CursorFor(normalized, processes[limit-1])
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "build process cursor")
		}
		response.NextPageToken, cursorErr = s.cursors.EncodeQuery(normalized.CursorSigningKeyID, cursor)
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "encode process cursor")
		}
		processes = processes[:limit]
	}
	response.Processes = make([]*projectionv1.CompletedProcessReadModel, 0, len(processes))
	for _, process := range processes {
		instance, masked := toWireSelected(process.Instance, normalized.Fields, normalized.SelectedFields)
		row := &projectionv1.CompletedProcessReadModel{Instance: instance, MaskedFields: masked}
		if selectedAndReadable(normalized, application.ProcessDurationMS, "duration_ms") {
			row.DurationMs = process.DurationMS
		} else if normalized.Fields != nil && !normalized.Fields.CanRead("duration_ms") {
			row.MaskedFields = append(row.MaskedFields, "duration_ms")
		}
		if selectedAndReadable(normalized, application.ProcessEventCount, "event_count") {
			row.EventCount = process.EventCount
		} else if normalized.Fields != nil && !normalized.Fields.CanRead("event_count") {
			row.MaskedFields = append(row.MaskedFields, "event_count")
		}
		response.Processes = append(response.Processes, row)
	}
	return response, nil
}

func fromWireGeneralQuery(request *projectionv1.QueryProcessesRequest) (application.ProcessQuery, error) {
	if request == nil {
		return application.ProcessQuery{}, application.ErrInvalidQuery
	}
	filter, err := fromWireProcessExpression(request.GetFilter(), 1)
	if err != nil {
		return application.ProcessQuery{}, err
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return application.ProcessQuery{}, err
	}
	scope := application.QueryScope(request.GetScope())
	query := application.ProcessQuery{
		TenantID: request.GetTenantId(), Filter: filter, Scope: scope,
		IncludeDeleted: request.GetIncludeDeleted(), Limit: int(request.GetPageSize()), Credential: credential,
		Sort: make([]application.ProcessSort, 0, len(request.GetSort())),
		SelectedFields: make([]application.ProcessField, 0, len(request.GetSelect())),
	}
	for _, field := range request.GetSelect() {
		query.SelectedFields = append(query.SelectedFields, application.ProcessField(field))
	}
	for _, sort := range request.GetSort() {
		if sort == nil {
			return application.ProcessQuery{}, application.ErrInvalidQuery
		}
		query.Sort = append(query.Sort, fromWireSort(sort))
	}
	return query, nil
}

func fromWireSort(sort *projectionv1.ProcessSort) application.ProcessSort {
	return application.ProcessSort{
		Field: application.ProcessField(sort.GetField()), Direction: application.SortDirection(sort.GetDirection()),
		TextTransform: application.TextTransform(sort.GetTextTransform()), Coalesce: fromWireOptionalValue(sort.GetCoalesceValue()),
		NumericTransform: application.NumericTransform(sort.GetNumericTransform()), DateTransform: application.DateTransform(sort.GetDateTransform()),
	}
}

func selectedAndReadable(query application.ProcessQuery, field application.ProcessField, name string) bool {
	if query.Fields != nil && !query.Fields.CanRead(name) {
		return false
	}
	if len(query.SelectedFields) == 0 {
		return true
	}
	for _, selected := range query.SelectedFields {
		if selected == field {
			return true
		}
	}
	return false
}

func toWireSorts(sorts []application.ProcessSort) []*projectionv1.ProcessSort {
	result := make([]*projectionv1.ProcessSort, 0, len(sorts))
	for _, sort := range sorts {
		result = append(result, &projectionv1.ProcessSort{
			Field: projectionv1.ProcessField(sort.Field), Direction: projectionv1.SortDirection(sort.Direction),
			TextTransform: projectionv1.TextTransform(sort.TextTransform), NumericTransform: projectionv1.NumericTransform(sort.NumericTransform),
			DateTransform: projectionv1.DateTransform(sort.DateTransform), CoalesceValue: toWireQueryValue(sort.Coalesce),
		})
	}
	return result
}

func toWireQueryValue(value *application.QueryValue) *projectionv1.QueryValue {
	if value == nil {
		return nil
	}
	result := &projectionv1.QueryValue{}
	switch value.Kind {
	case application.ValueString:
		result.Value = &projectionv1.QueryValue_StringValue{StringValue: value.String}
	case application.ValueInteger:
		result.Value = &projectionv1.QueryValue_IntegerValue{IntegerValue: value.Integer}
	case application.ValueBoolean:
		result.Value = &projectionv1.QueryValue_BooleanValue{BooleanValue: value.Boolean}
	case application.ValueTimestamp:
		result.Value = &projectionv1.QueryValue_TimestampEpochMs{TimestampEpochMs: uint64(value.Time.UnixMilli())}
	}
	return result
}

func fromWireActorProof(proof *authorizationv1.ActorProof) (application.QueryCredential, error) {
	if proof == nil || len(proof.GetSignedProof()) == 0 {
		return application.QueryCredential{}, application.ErrQueryForbidden
	}
	credential := application.QueryCredential{SignedProof: append([]byte(nil), proof.GetSignedProof()...)}
	switch proof.GetType() {
	case authorizationv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT:
		credential.Type = application.ActorProofOriginalJWT
	case authorizationv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT:
		credential.Type = application.ActorProofSignedInternalContext
	default:
		return application.QueryCredential{}, application.ErrQueryForbidden
	}
	return credential, nil
}

func fromWireProcessExpression(
	expression *projectionv1.ProcessExpression,
	depth int,
) (*application.ProcessExpression, error) {
	if expression == nil {
		return nil, nil
	}
	if depth > maxWireExpressionDepth {
		return nil, fmt.Errorf("%w: expression exceeds transport depth", application.ErrInvalidQuery)
	}
	result := &application.ProcessExpression{}
	switch value := expression.GetExpression().(type) {
	case *projectionv1.ProcessExpression_Predicate:
		if value.Predicate == nil {
			return nil, application.ErrInvalidQuery
		}
		values, err := fromWireValues(value.Predicate.GetValues())
		if err != nil {
			return nil, err
		}
		result.Predicate = &application.ProcessPredicate{
			Field:    application.ProcessField(value.Predicate.GetField()),
			Operator: application.Comparison(value.Predicate.GetOperator()), Values: values,
			TextTransform:    application.TextTransform(value.Predicate.GetTextTransform()),
			Coalesce:         fromWireOptionalValue(value.Predicate.GetCoalesceValue()),
			NumericTransform: application.NumericTransform(value.Predicate.GetNumericTransform()),
			DateTransform:    application.DateTransform(value.Predicate.GetDateTransform()),
		}
	case *projectionv1.ProcessExpression_Group:
		if value.Group == nil {
			return nil, application.ErrInvalidQuery
		}
		result.Logical = application.Logical(value.Group.GetOperator())
		result.Children = make([]application.ProcessExpression, 0, len(value.Group.GetExpressions()))
		for _, child := range value.Group.GetExpressions() {
			mapped, err := fromWireProcessExpression(child, depth+1)
			if err != nil || mapped == nil {
				return nil, application.ErrInvalidQuery
			}
			result.Children = append(result.Children, *mapped)
		}
	case *projectionv1.ProcessExpression_Negated:
		mapped, err := fromWireProcessExpression(value.Negated, depth+1)
		if err != nil || mapped == nil {
			return nil, application.ErrInvalidQuery
		}
		result.Negated = mapped
	case *projectionv1.ProcessExpression_EventRelation:
		if value.EventRelation == nil {
			return nil, application.ErrInvalidQuery
		}
		mapped, err := fromWireEventExpression(value.EventRelation.GetExpression(), depth+1)
		if err != nil || mapped == nil {
			return nil, application.ErrInvalidQuery
		}
		result.EventRelation = &application.EventRelation{
			Expression: *mapped,
			Quantifier: application.RelationQuantifier(value.EventRelation.GetQuantifier()),
		}
	default:
		return nil, application.ErrInvalidQuery
	}
	return result, nil
}

func fromWireEventExpression(
	expression *projectionv1.EventExpression,
	depth int,
) (*application.EventExpression, error) {
	if expression == nil || depth > maxWireExpressionDepth {
		return nil, application.ErrInvalidQuery
	}
	result := &application.EventExpression{}
	switch value := expression.GetExpression().(type) {
	case *projectionv1.EventExpression_Predicate:
		if value.Predicate == nil {
			return nil, application.ErrInvalidQuery
		}
		values, err := fromWireValues(value.Predicate.GetValues())
		if err != nil {
			return nil, err
		}
		result.Predicate = &application.EventPredicate{
			Field:    application.EventField(value.Predicate.GetField()),
			Operator: application.Comparison(value.Predicate.GetOperator()), Values: values,
			TextTransform:    application.TextTransform(value.Predicate.GetTextTransform()),
			Coalesce:         fromWireOptionalValue(value.Predicate.GetCoalesceValue()),
			NumericTransform: application.NumericTransform(value.Predicate.GetNumericTransform()),
			DateTransform:    application.DateTransform(value.Predicate.GetDateTransform()),
		}
	case *projectionv1.EventExpression_Group:
		if value.Group == nil {
			return nil, application.ErrInvalidQuery
		}
		result.Logical = application.Logical(value.Group.GetOperator())
		result.Children = make([]application.EventExpression, 0, len(value.Group.GetExpressions()))
		for _, child := range value.Group.GetExpressions() {
			mapped, err := fromWireEventExpression(child, depth+1)
			if err != nil || mapped == nil {
				return nil, application.ErrInvalidQuery
			}
			result.Children = append(result.Children, *mapped)
		}
	case *projectionv1.EventExpression_Negated:
		mapped, err := fromWireEventExpression(value.Negated, depth+1)
		if err != nil || mapped == nil {
			return nil, application.ErrInvalidQuery
		}
		result.Negated = mapped
	default:
		return nil, application.ErrInvalidQuery
	}
	return result, nil
}

func fromWireValues(values []*projectionv1.QueryValue) ([]application.QueryValue, error) {
	result := make([]application.QueryValue, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, application.ErrInvalidQuery
		}
		mapped := application.QueryValue{}
		switch item := value.GetValue().(type) {
		case *projectionv1.QueryValue_StringValue:
			mapped.Kind, mapped.String = application.ValueString, item.StringValue
		case *projectionv1.QueryValue_IntegerValue:
			mapped.Kind, mapped.Integer = application.ValueInteger, item.IntegerValue
		case *projectionv1.QueryValue_BooleanValue:
			mapped.Kind, mapped.Boolean = application.ValueBoolean, item.BooleanValue
		case *projectionv1.QueryValue_TimestampEpochMs:
			if item.TimestampEpochMs == 0 || item.TimestampEpochMs > uint64(^uint64(0)>>1) {
				return nil, application.ErrInvalidQuery
			}
			mapped.Kind = application.ValueTimestamp
			mapped.Time = time.UnixMilli(int64(item.TimestampEpochMs)).UTC()
		default:
			return nil, application.ErrInvalidQuery
		}
		result = append(result, mapped)
	}
	return result, nil
}

func fromWireOptionalValue(value *projectionv1.QueryValue) *application.QueryValue {
	if value == nil {
		return nil
	}
	values, err := fromWireValues([]*projectionv1.QueryValue{value})
	if err != nil || len(values) != 1 {
		// Preserve an invalid marker so application validation rejects it using
		// the same typed-query error path as ordinary predicate values.
		return &application.QueryValue{}
	}
	return &values[0]
}
