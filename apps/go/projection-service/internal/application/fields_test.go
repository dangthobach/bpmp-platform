package application

import (
	"errors"
	"testing"
)

func TestFieldAuthorizationBlocksInferenceAndBindsCursor(t *testing.T) {
	fields := &FieldAuthorization{ReadFields: []string{"instance_id"}, QueryFields: []string{"instance_id", "updated_at_epoch_ms"}}
	query := ProcessQuery{Fields: fields, Scope: QueryScopeInstances, Sort: []ProcessSort{{Field: ProcessUpdatedAt, Direction: SortDescending}}, Filter: &ProcessExpression{Predicate: &ProcessPredicate{Field: ProcessInitiatorActorID, Operator: CompareEqual, Values: []QueryValue{{Kind: ValueString, String: "secret"}}}}}
	if !errors.Is(validateFieldAccess(query), ErrQueryForbidden) {
		t.Fatal("hidden filter accepted")
	}
	query.Filter = nil
	if err := validateFieldAccess(query); err != nil {
		t.Fatal(err)
	}
	first, _ := query.Fingerprint()
	query.Fields = &FieldAuthorization{ReadFields: []string{"instance_id", "status"}, QueryFields: fields.QueryFields}
	second, _ := query.Fingerprint()
	if first == second {
		t.Fatal("field policy not bound into cursor")
	}
}
