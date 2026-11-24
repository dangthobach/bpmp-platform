package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type recordingRepo struct{ command CreateCommand }

func (r *recordingRepo) Create(_ context.Context, c CreateCommand) (CreateResult, error) {
	r.command = c
	return CreateResult{Case: Case{TenantID: c.Request.TenantID, ID: c.CaseID}}, nil
}
func (*recordingRepo) Get(context.Context, uuid.UUID, uuid.UUID) (Case, error) {
	return Case{}, ErrNotFound
}
func (*recordingRepo) GetView(context.Context, uuid.UUID, uuid.UUID) (CaseView, error) {
	return CaseView{}, ErrNotFound
}
func (*recordingRepo) Timeline(context.Context, uuid.UUID, uuid.UUID, int64, int) ([]TimelineEntry, error) {
	return nil, ErrNotFound
}
func (*recordingRepo) ProcessEvents(context.Context, uuid.UUID, uuid.UUID, int64, int) (ProcessEventPage, error) {
	return ProcessEventPage{}, ErrNotFound
}
func (*recordingRepo) Search(context.Context, uuid.UUID, string, time.Time, uuid.UUID, int) (SearchPage, error) {
	return SearchPage{}, nil
}

func TestCreateNormalizesIntentAndRejectsInvalidScope(t *testing.T) {
	repo := &recordingRepo{}
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	service, err := NewService(repo, func() time.Time { return time.Unix(100, 0) }, func() uuid.UUID {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), CreateRequest{ActorID: "alice"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid tenant scope was accepted: %v", err)
	}
	_, err = service.Create(context.Background(), CreateRequest{TenantID: uuid.New(), ActorID: " alice ", IdempotencyKey: " key ", CorrelationID: "corr", CustomerRef: " customer ", WorkflowType: "approval", WorkflowVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.command.Request.ActorID != "alice" || repo.command.Request.CustomerRef != "customer" || repo.command.Digest == ([32]byte{}) || repo.command.CaseID == repo.command.EventID {
		t.Fatalf("create intent was not normalized: %+v", repo.command)
	}
}
