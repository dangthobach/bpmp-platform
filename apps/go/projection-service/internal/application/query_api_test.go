package application

import (
	"testing"
	"time"
)

func TestEventCursorIncludesStableInstanceTieBreaker(t *testing.T) {
	query := EventQuery{TenantID: "tenant-a", Sort: []EventSort{{Field: EventSequence, Direction: SortDescending}}}
	cursor, err := EventCursorFor(query, WorkflowEvent{InstanceID: "instance-b", Sequence: 42})
	if err != nil {
		t.Fatal(err)
	}
	if len(cursor.Values) != 2 || cursor.Values[0].Integer != 42 || cursor.Values[1].String != "instance-b" {
		t.Fatalf("unexpected event cursor: %#v", cursor)
	}
}

func TestExportFieldNeutralizesSpreadsheetFormula(t *testing.T) {
	row := CompletedProcess{Instance: Instance{InstanceID: "=WEBSERVICE(\"https://example.invalid\")", UpdatedAt: time.Unix(0, 0)}}
	if got := exportField(row, ProcessInstanceID); got[0] != '\'' {
		t.Fatalf("formula was not neutralized: %q", got)
	}
}
