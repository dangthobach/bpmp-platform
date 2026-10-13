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
		row := &projectionv1.CompletedProcessReadModel{Instance: toWireAuthorized(process.Instance, normalized.Fields)}
		if normalized.Fields.CanRead("duration_ms") {
			row.DurationMs = process.DurationMS
		}
		if normalized.Fields.CanRead("event_count") {
			row.EventCount = process.EventCount
		}
		response.Processes = append(response.Processes, row)
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
	credential, err := fromWireActorProof(request.GetActorProof())
	if err != nil {
		return application.ProcessQuery{}, err
	}
	query := application.ProcessQuery{
		TenantID: request.GetTenantId(), Filter: filter,
		IncludeDeleted: request.GetIncludeDeleted(), Limit: int(request.GetPageSize()),
		Credential: credential,
		Sort:       make([]application.ProcessSort, 0, len(request.GetSort())),
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
