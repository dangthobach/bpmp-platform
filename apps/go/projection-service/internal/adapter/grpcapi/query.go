package grpcapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxQueryPageTokenBytes = 8192
	maxWireExpressionDepth = 64
)

func (s *Server) QueryCompletedProcesses(
	ctx context.Context,
	request *projectionv1.QueryCompletedProcessesRequest,
) (*projectionv1.QueryCompletedProcessesResponse, error) {
	query, err := fromWireQuery(request)
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
		response.NextPageToken, cursorErr = encodeQueryCursor(cursor)
		if cursorErr != nil {
			return nil, status.Error(codes.Internal, "encode completed process cursor")
		}
		processes = processes[:limit]
	}
	response.Processes = make([]*projectionv1.CompletedProcessReadModel, 0, len(processes))
	for _, process := range processes {
		response.Processes = append(response.Processes, &projectionv1.CompletedProcessReadModel{
			Instance: toWire(process.Instance), DurationMs: process.DurationMS,
			EventCount: process.EventCount,
		})
	}
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
	after, err := decodeQueryCursor(request.GetPageToken())
	if err != nil {
		return application.ProcessQuery{}, err
	}
	query := application.ProcessQuery{
		TenantID: request.GetTenantId(), Filter: filter,
		IncludeDeleted: request.GetIncludeDeleted(), Limit: int(request.GetPageSize()), After: after,
		Sort: make([]application.ProcessSort, 0, len(request.GetSort())),
	}
	for _, sort := range request.GetSort() {
		if sort == nil {
			return application.ProcessQuery{}, application.ErrInvalidQuery
		}
		query.Sort = append(query.Sort, application.ProcessSort{
			Field:     application.ProcessField(sort.GetField()),
			Direction: application.SortDirection(sort.GetDirection()),
		})
	}
	return query, nil
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
			Expression: *mapped, MustExist: value.EventRelation.GetMustExist(),
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

func encodeQueryCursor(cursor *application.QueryCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeQueryCursor(token string) (*application.QueryCursor, error) {
	if token == "" {
		return nil, nil
	}
	if len(token) > maxQueryPageTokenBytes {
		return nil, errors.New("completed process page token exceeds size limit")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errors.New("completed process page token is malformed")
	}
	var cursor application.QueryCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cursor); err != nil || cursor.QueryHash == "" || len(cursor.Values) == 0 {
		return nil, errors.New("completed process page token is invalid")
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("completed process page token contains trailing data")
	}
	return &cursor, nil
}
