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
		SchemaVersion:    capabilities.SchemaVersion,
		ConfigVersion:    capabilities.Policy.ConfigVersion,
		PolicyVersion:    capabilities.Policy.PolicyVersion,
		DefaultPageSize:  capabilities.Policy.QueryDefaultPageSize,
		MaxPageSize:      capabilities.Policy.QueryMaxPageSize,
		MaxFilterDepth:   capabilities.Policy.QueryMaxFilterDepth,
		MaxPredicates:    capabilities.Policy.QueryMaxPredicates,
		MaxSortFields:    capabilities.Policy.QueryMaxSortFields,
		MaxRelationJoins: capabilities.Policy.QueryMaxRelationJoins,
		MaxInValues:      capabilities.Policy.QueryMaxInValues,
		MaxStringBytes:   capabilities.Policy.QueryMaxStringBytes,
		DefaultSort:      toWireSorts(capabilities.DefaultSort),
	}
	for _, scope := range capabilities.Scopes {
		response.Scopes = append(response.Scopes, projectionv1.QueryScope(scope))
	}
	for _, field := range capabilities.Fields {
		item := &projectionv1.ProcessFieldCapability{
			Field: projectionv1.ProcessField(field.Field), ValueKind: projectionv1.QueryValueKind(field.Kind),
			Filterable: field.Filterable, Sortable: field.Sortable, Selectable: field.Selectable,
		}
		for _, operator := range field.Operators {
			item.Operators = append(item.Operators, projectionv1.ComparisonOperator(operator))
		}
		for _, transform := range field.TextTransforms {
			item.TextTransforms = append(item.TextTransforms, projectionv1.TextTransform(transform))
		}
		for _, transform := range field.NumericTransforms {
			item.NumericTransforms = append(item.NumericTransforms, projectionv1.NumericTransform(transform))
		}
		for _, transform := range field.DateTransforms {
			item.DateTransforms = append(item.DateTransforms, projectionv1.DateTransform(transform))
		}
		response.Fields = append(response.Fields, item)
	}
	for _, field := range capabilities.EventFields {
		item := &projectionv1.EventFieldCapability{Field: projectionv1.EventField(field.Field), ValueKind: projectionv1.QueryValueKind(field.Kind)}
		for _, operator := range field.Operators {
			item.Operators = append(item.Operators, projectionv1.ComparisonOperator(operator))
		}
		for _, transform := range field.TextTransforms {
			item.TextTransforms = append(item.TextTransforms, projectionv1.TextTransform(transform))
		}
		for _, transform := range field.NumericTransforms {
			item.NumericTransforms = append(item.NumericTransforms, projectionv1.NumericTransform(transform))
		}
		for _, transform := range field.DateTransforms {
			item.DateTransforms = append(item.DateTransforms, projectionv1.DateTransform(transform))
		}
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
		row.Events = toWireEvents(process.Events, normalized.Fields)
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
		Credential:     credential,
		Sort:           make([]application.ProcessSort, 0, len(request.GetSort())),
		SelectedFields: make([]application.ProcessField, 0, len(request.GetSelect())),
		Collapse:       application.ProcessField(request.GetCollapse()),
	}
	for _, field := range request.GetSelect() {
		query.SelectedFields = append(query.SelectedFields, application.ProcessField(field))
	}
	if request.GetExpandEvents() != nil {
		var eventFilter *application.EventExpression
		if request.GetExpandEvents().GetFilter() != nil {
			var filterErr error
			eventFilter, filterErr = fromWireEventExpression(request.GetExpandEvents().GetFilter(), 1)
			if filterErr != nil {
				return application.ProcessQuery{}, filterErr
			}
		}
		query.ExpandEvents = &application.EventExpansion{Filter: eventFilter, Limit: request.GetExpandEvents().GetLimit()}
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
		PolicyVersion:  normalized.AuthorizationPolicyVersion,
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
		row.Events = toWireEvents(process.Events, normalized.Fields)
		response.Processes = append(response.Processes, row)
	}
	return response, nil
}

func (s *Server) AggregateProcesses(ctx context.Context, request *projectionv1.AggregateProcessesRequest) (*projectionv1.AggregateProcessesResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	filter, err := fromWireProcessExpression(request.GetFilter(), 1)
	if err != nil {
		return nil, mapError(err)
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	query := application.AggregateQuery{
		TenantID: request.GetTenantId(), Scope: application.QueryScope(request.GetScope()), Filter: filter,
		Limit: int(request.GetLimit()), Credential: credential,
		GroupBy: make([]application.ProcessField, 0, len(request.GetGroupBy())),
		Metrics: make([]application.AggregateMetric, 0, len(request.GetMetrics())),
	}
	for _, field := range request.GetGroupBy() {
		query.GroupBy = append(query.GroupBy, application.ProcessField(field))
	}
	for index, metric := range request.GetMetrics() {
		if metric == nil {
			return nil, mapError(invalidWireQuery(fmt.Sprintf("metrics[%d]", index), "required", "metric is required"))
		}
		query.Metrics = append(query.Metrics, application.AggregateMetric{Alias: metric.GetAlias(), Operation: application.AggregateOperation(metric.GetOperation()), Field: application.ProcessField(metric.GetField())})
	}
	rows, normalized, err := s.service.AggregateProcesses(ctx, query)
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.AggregateProcessesResponse{PolicyVersion: normalized.AuthorizationPolicyVersion, Rows: make([]*projectionv1.AggregateRow, 0, len(rows))}
	for _, row := range rows {
		item := &projectionv1.AggregateRow{}
		for _, cell := range row.Dimensions {
			value := cell.Value
			item.Dimensions = append(item.Dimensions, &projectionv1.AggregateCell{Name: cell.Name, Value: toWireQueryValue(&value)})
		}
		for _, cell := range row.Metrics {
			value := cell.Value
			item.Metrics = append(item.Metrics, &projectionv1.AggregateCell{Name: cell.Name, Value: toWireQueryValue(&value)})
		}
		response.Rows = append(response.Rows, item)
	}
	return response, nil
}

func (s *Server) QueryEvents(ctx context.Context, request *projectionv1.QueryEventsRequest) (*projectionv1.QueryEventsResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	filter, err := fromWireEventExpression(request.GetFilter(), 1)
	if err != nil && request.GetFilter() != nil {
		return nil, mapError(err)
	}
	processFilter, err := fromWireProcessExpression(request.GetProcessFilter(), 1)
	if err != nil {
		return nil, mapError(err)
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	after, err := s.cursors.DecodeQuery(request.GetPageToken())
	if err != nil {
		return nil, mapError(invalidWireQuery("page_token", "invalid_cursor", "page token is invalid"))
	}
	query := application.EventQuery{TenantID: request.GetTenantId(), Filter: filter, ProcessFilter: processFilter, Limit: int(request.GetPageSize()), Credential: credential, After: after, Sort: make([]application.EventSort, 0, len(request.GetSort()))}
	for index, sort := range request.GetSort() {
		if sort == nil {
			return nil, mapError(invalidWireQuery(fmt.Sprintf("sort[%d]", index), "required", "sort is required"))
		}
		query.Sort = append(query.Sort, application.EventSort{Field: application.EventField(sort.GetField()), Direction: application.SortDirection(sort.GetDirection())})
	}
	events, limit, normalized, err := s.service.QueryEvents(ctx, query)
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.QueryEventsResponse{PolicyVersion: normalized.AuthorizationPolicyVersion}
	if limit > 0 && len(events) > limit {
		cursor, cursorErr := application.EventCursorFor(normalized, events[limit-1])
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "build event cursor")
		}
		response.NextPageToken, cursorErr = s.cursors.EncodeQuery(normalized.CursorSigningKeyID, cursor)
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "encode event cursor")
		}
		events = events[:limit]
	}
	response.Events = toWireEvents(events, normalized.Fields)
	return response, nil
}

func (s *Server) SaveQuery(ctx context.Context, request *projectionv1.SaveQueryRequest) (*projectionv1.SaveQueryResponse, error) {
	if request == nil || request.GetQuery() == nil {
		return nil, mapError(invalidWireQuery("query", "required", "query is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	request.Query.TenantId, request.Query.ActorProof = request.GetTenantId(), request.GetActorProof()
	query, err := fromWireGeneralQuery(request.GetQuery())
	if err != nil {
		return nil, mapError(err)
	}
	query.TenantID = request.GetTenantId()
	query.Credential = credential
	item, err := s.service.SaveQuery(ctx, application.SavedQuery{TenantID: request.GetTenantId(), ID: request.GetId(), Name: request.GetName(), Query: query}, request.GetExpectedVersion(), credential)
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.SaveQueryResponse{SavedQuery: toWireSavedQuery(item)}, nil
}

func (s *Server) ListSavedQueries(ctx context.Context, request *projectionv1.ListSavedQueriesRequest) (*projectionv1.ListSavedQueriesResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	var afterTime time.Time
	afterID := ""
	if request.GetPageToken() != "" {
		cursor, decodeErr := s.cursors.DecodeQuery(request.GetPageToken())
		if decodeErr != nil || cursor.QueryHash != "saved-query-list-v1" || len(cursor.Values) != 2 || cursor.Values[0].Kind != application.ValueTimestamp || cursor.Values[1].Kind != application.ValueString {
			return nil, mapError(invalidWireQuery("page_token", "invalid_cursor", "page token is invalid"))
		}
		afterTime, afterID = cursor.Values[0].Time, cursor.Values[1].String
	}
	items, limit, keyID, err := s.service.ListSavedQueries(ctx, request.GetTenantId(), credential, afterTime, afterID, int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &projectionv1.ListSavedQueriesResponse{}
	if len(items) > limit {
		last := items[limit-1]
		response.NextPageToken, err = s.cursors.EncodeQuery(keyID, &application.QueryCursor{QueryHash: "saved-query-list-v1", Values: []application.QueryValue{{Kind: application.ValueTimestamp, Time: last.UpdatedAt}, {Kind: application.ValueString, String: last.ID}}})
		if err != nil {
			return nil, status.Error(codes.Internal, "encode saved query cursor")
		}
		items = items[:limit]
	}
	for _, item := range items {
		response.SavedQueries = append(response.SavedQueries, toWireSavedQuery(item))
	}
	return response, nil
}

func (s *Server) DeleteSavedQuery(ctx context.Context, request *projectionv1.DeleteSavedQueryRequest) (*projectionv1.DeleteSavedQueryResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	if err = s.service.DeleteSavedQuery(ctx, request.GetTenantId(), request.GetId(), request.GetExpectedVersion(), credential); err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.DeleteSavedQueryResponse{}, nil
}

func (s *Server) CreateQueryExport(ctx context.Context, request *projectionv1.CreateQueryExportRequest) (*projectionv1.CreateQueryExportResponse, error) {
	if request == nil || request.GetQuery() == nil {
		return nil, mapError(invalidWireQuery("query", "required", "query is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	request.Query.TenantId, request.Query.ActorProof = request.GetTenantId(), request.GetActorProof()
	query, err := fromWireGeneralQuery(request.GetQuery())
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.service.CreateQueryExport(ctx, request.GetTenantId(), query, int(request.GetMaxRows()), credential)
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.CreateQueryExportResponse{Export: toWireExport(item)}, nil
}
func (s *Server) GetQueryExport(ctx context.Context, request *projectionv1.GetQueryExportRequest) (*projectionv1.GetQueryExportResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.service.GetQueryExport(ctx, request.GetTenantId(), request.GetId(), credential)
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.GetQueryExportResponse{Export: toWireExport(item)}, nil
}
func (s *Server) CancelQueryExport(ctx context.Context, request *projectionv1.CancelQueryExportRequest) (*projectionv1.CancelQueryExportResponse, error) {
	if request == nil {
		return nil, mapError(invalidWireQuery("request", "required", "request is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.service.CancelQueryExport(ctx, request.GetTenantId(), request.GetId(), credential)
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.CancelQueryExportResponse{Export: toWireExport(item)}, nil
}
func (s *Server) ExplainQueryPlan(ctx context.Context, request *projectionv1.ExplainQueryPlanRequest) (*projectionv1.ExplainQueryPlanResponse, error) {
	if request == nil || request.GetQuery() == nil {
		return nil, mapError(invalidWireQuery("query", "required", "query is required"))
	}
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return nil, mapError(err)
	}
	request.Query.TenantId, request.Query.ActorProof = request.GetTenantId(), request.GetActorProof()
	query, err := fromWireGeneralQuery(request.GetQuery())
	if err != nil {
		return nil, mapError(err)
	}
	query.TenantID, query.Credential = request.GetTenantId(), credential
	plan, fingerprint, err := s.service.ExplainQueryPlan(ctx, query, request.GetAnalyze())
	if err != nil {
		return nil, mapError(err)
	}
	return &projectionv1.ExplainQueryPlanResponse{RedactedPlanJson: plan, QueryFingerprint: fingerprint}, nil
}
func toWireExport(item application.QueryExport) *projectionv1.QueryExport {
	statusValue := projectionv1.QueryExportStatus(item.Status)
	if item.Status < application.QueryExportPending || item.Status > application.QueryExportCancelled {
		statusValue = projectionv1.QueryExportStatus_QUERY_EXPORT_STATUS_PENDING
	}
	return &projectionv1.QueryExport{Id: item.ID, Status: statusValue, RowCount: item.RowCount, Csv: item.CSV, ErrorCode: item.ErrorCode, CreatedAtEpochMs: uint64(item.CreatedAt.UnixMilli()), ExpiresAtEpochMs: uint64(item.ExpiresAt.UnixMilli())}
}

func toWireSavedQuery(item application.SavedQuery) *projectionv1.SavedQuery {
	return &projectionv1.SavedQuery{Id: item.ID, Name: item.Name, Version: item.Version, Query: toWireGeneralQuery(item.Query), CreatedAtEpochMs: uint64(item.CreatedAt.UnixMilli()), UpdatedAtEpochMs: uint64(item.UpdatedAt.UnixMilli())}
}

func toWireGeneralQuery(query application.ProcessQuery) *projectionv1.QueryProcessesRequest {
	result := &projectionv1.QueryProcessesRequest{Scope: projectionv1.QueryScope(query.Scope), Filter: toWireProcessExpression(query.Filter), IncludeDeleted: query.IncludeDeleted, PageSize: uint32(query.Limit), Collapse: projectionv1.ProcessField(query.Collapse), Sort: toWireSorts(query.Sort)}
	for _, field := range query.SelectedFields {
		result.Select = append(result.Select, projectionv1.ProcessField(field))
	}
	if query.ExpandEvents != nil {
		result.ExpandEvents = &projectionv1.EventExpansion{Filter: toWireEventExpression(query.ExpandEvents.Filter), Limit: query.ExpandEvents.Limit}
	}
	return result
}

func toWireProcessExpression(expression *application.ProcessExpression) *projectionv1.ProcessExpression {
	if expression == nil {
		return nil
	}
	result := &projectionv1.ProcessExpression{}
	switch {
	case expression.Predicate != nil:
		p := expression.Predicate
		values := make([]*projectionv1.QueryValue, 0, len(p.Values))
		for i := range p.Values {
			values = append(values, toWireQueryValue(&p.Values[i]))
		}
		result.Expression = &projectionv1.ProcessExpression_Predicate{Predicate: &projectionv1.ProcessPredicate{Field: projectionv1.ProcessField(p.Field), Operator: projectionv1.ComparisonOperator(p.Operator), Values: values, TextTransform: projectionv1.TextTransform(p.TextTransform), NumericTransform: projectionv1.NumericTransform(p.NumericTransform), DateTransform: projectionv1.DateTransform(p.DateTransform), CoalesceValue: toWireQueryValue(p.Coalesce)}}
	case expression.Negated != nil:
		result.Expression = &projectionv1.ProcessExpression_Negated{Negated: toWireProcessExpression(expression.Negated)}
	case expression.EventRelation != nil:
		result.Expression = &projectionv1.ProcessExpression_EventRelation{EventRelation: &projectionv1.EventRelationPredicate{Quantifier: projectionv1.RelationQuantifier(expression.EventRelation.Quantifier), Expression: toWireEventExpression(&expression.EventRelation.Expression)}}
	default:
		group := &projectionv1.ProcessExpressionGroup{Operator: projectionv1.LogicalOperator(expression.Logical)}
		for i := range expression.Children {
			group.Expressions = append(group.Expressions, toWireProcessExpression(&expression.Children[i]))
		}
		result.Expression = &projectionv1.ProcessExpression_Group{Group: group}
	}
	return result
}

func toWireEventExpression(expression *application.EventExpression) *projectionv1.EventExpression {
	if expression == nil {
		return nil
	}
	result := &projectionv1.EventExpression{}
	switch {
	case expression.Predicate != nil:
		p := expression.Predicate
		values := make([]*projectionv1.QueryValue, 0, len(p.Values))
		for i := range p.Values {
			values = append(values, toWireQueryValue(&p.Values[i]))
		}
		result.Expression = &projectionv1.EventExpression_Predicate{Predicate: &projectionv1.EventPredicate{Field: projectionv1.EventField(p.Field), Operator: projectionv1.ComparisonOperator(p.Operator), Values: values, TextTransform: projectionv1.TextTransform(p.TextTransform), NumericTransform: projectionv1.NumericTransform(p.NumericTransform), DateTransform: projectionv1.DateTransform(p.DateTransform), CoalesceValue: toWireQueryValue(p.Coalesce)}}
	case expression.Negated != nil:
		result.Expression = &projectionv1.EventExpression_Negated{Negated: toWireEventExpression(expression.Negated)}
	default:
		group := &projectionv1.EventExpressionGroup{Operator: projectionv1.LogicalOperator(expression.Logical)}
		for i := range expression.Children {
			group.Expressions = append(group.Expressions, toWireEventExpression(&expression.Children[i]))
		}
		result.Expression = &projectionv1.EventExpression_Group{Group: group}
	}
	return result
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
	if request.GetScope() == projectionv1.QueryScope_QUERY_SCOPE_UNSPECIFIED {
		return application.ProcessQuery{}, invalidWireQuery("scope", "required", "query scope is required")
	}
	query := application.ProcessQuery{
		TenantID: request.GetTenantId(), Filter: filter, Scope: scope,
		IncludeDeleted: request.GetIncludeDeleted(), Limit: int(request.GetPageSize()), Credential: credential,
		Sort:           make([]application.ProcessSort, 0, len(request.GetSort())),
		SelectedFields: make([]application.ProcessField, 0, len(request.GetSelect())),
		Collapse:       application.ProcessField(request.GetCollapse()),
	}
	for _, field := range request.GetSelect() {
		query.SelectedFields = append(query.SelectedFields, application.ProcessField(field))
	}
	if request.GetExpandEvents() != nil {
		var eventFilter *application.EventExpression
		if request.GetExpandEvents().GetFilter() != nil {
			var filterErr error
			eventFilter, filterErr = fromWireEventExpression(request.GetExpandEvents().GetFilter(), 1)
			if filterErr != nil {
				return application.ProcessQuery{}, filterErr
			}
		}
		query.ExpandEvents = &application.EventExpansion{Filter: eventFilter, Limit: request.GetExpandEvents().GetLimit()}
	}
	for _, sort := range request.GetSort() {
		if sort == nil {
			return application.ProcessQuery{}, application.ErrInvalidQuery
		}
		query.Sort = append(query.Sort, fromWireSort(sort))
	}
	return query, nil
}

func invalidWireQuery(path, rule, message string) error {
	return &application.QueryValidationError{Violations: []application.QueryViolation{{Path: path, Rule: rule, Message: message}}}
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
	case application.ValueDecimal:
		result.Value = &projectionv1.QueryValue_DecimalValue{DecimalValue: value.Decimal}
	}
	return result
}

func toWireEvents(events []application.WorkflowEvent, fields *application.FieldAuthorization) []*projectionv1.WorkflowEventReadModel {
	result := make([]*projectionv1.WorkflowEventReadModel, 0, len(events))
	for _, event := range events {
		row := &projectionv1.WorkflowEventReadModel{
			InstanceId: event.InstanceID, Sequence: event.Sequence, EventType: event.EventType,
			NodeId: event.NodeID, ActorId: event.ActorID, OccurredAtEpochMs: uint64(event.OccurredAt.UnixMilli()),
			ConfigVersion: event.ConfigVersion, PolicyVersion: event.PolicyVersion, IsDeleted: event.IsDeleted,
		}
		values := []struct {
			name  string
			clear func()
		}{
			{"instance_id", func() { row.InstanceId = "" }}, {"event_sequence", func() { row.Sequence = 0 }},
			{"event_type", func() { row.EventType = "" }}, {"event_node_id", func() { row.NodeId = "" }},
			{"event_actor_id", func() { row.ActorId = "" }}, {"event_occurred_at", func() { row.OccurredAtEpochMs = 0 }},
			{"event_config_version", func() { row.ConfigVersion = "" }}, {"event_policy_version", func() { row.PolicyVersion = "" }},
		}
		for _, value := range values {
			if !fields.CanRead(value.name) {
				value.clear()
				row.MaskedFields = append(row.MaskedFields, value.name)
			}
		}
		result = append(result, row)
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
		case *projectionv1.QueryValue_DecimalValue:
			mapped.Kind, mapped.Decimal = application.ValueDecimal, item.DecimalValue
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
