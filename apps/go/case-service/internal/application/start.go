package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrLeaseLost = errors.New("workflow start lease was lost")
var ErrBeforeEngineSubmit = errors.New("workflow start failed before Engine submission")

type StartJob struct {
	TenantID        uuid.UUID
	CaseID          uuid.UUID
	CommandID       uuid.UUID
	LeaseToken      uuid.UUID
	ActorID         string
	CorrelationID   string
	WorkflowType    string
	WorkflowVersion string
	Attempts        int
}

type StartRepository interface {
	ClaimStart(context.Context, time.Time, time.Duration, uuid.UUID) (StartJob, bool, error)
	CompleteStart(context.Context, StartJob, time.Time, uuid.UUID) error
	RetryStart(context.Context, StartJob, time.Time, time.Time, string) error
}

// StartPort must resolve a fresh trusted actor proof for job.ActorID and submit
// the same tenant, instance (= CaseID), command and idempotency keys on every retry.
// A nil error means the Engine committed or returned its prior receipt.
type StartPort interface {
	StartWorkflow(context.Context, StartJob) error
}

type StartRunner struct {
	repo   StartRepository
	engine StartPort
	now    func() time.Time
	newID  func() uuid.UUID
}

func NewStartRunner(repo StartRepository, engine StartPort, now func() time.Time, newID func() uuid.UUID) (*StartRunner, error) {
	if repo == nil || engine == nil || now == nil || newID == nil {
		return nil, ErrInvalid
	}
	return &StartRunner{repo: repo, engine: engine, now: now, newID: newID}, nil
}

func (r *StartRunner) RunOnce(ctx context.Context) (bool, error) {
	now := r.now().UTC()
	leaseID := r.newID()
	if leaseID == uuid.Nil {
		return false, ErrInvalid
	}
	job, found, err := r.repo.ClaimStart(ctx, now, 30*time.Second, leaseID)
	if err != nil || !found {
		return found, err
	}
	if job.CaseID == uuid.Nil || job.CommandID != job.CaseID || job.LeaseToken != leaseID || job.Attempts < 1 {
		return true, ErrInvalid
	}
	if err = r.engine.StartWorkflow(ctx, job); err != nil {
		// An error may be an ambiguous commit. Reuse the exact Engine command on retry.
		backoff := time.Second << min(job.Attempts-1, 6)
		retryAt := r.now().UTC()
		code := "ENGINE_OUTCOME_UNKNOWN"
		if errors.Is(err, ErrBeforeEngineSubmit) {
			code = "START_INPUT_UNAVAILABLE"
		}
		if retryErr := r.repo.RetryStart(ctx, job, retryAt, retryAt.Add(backoff), code); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, nil
	}
	if err = r.repo.CompleteStart(ctx, job, r.now().UTC(), r.newID()); err != nil {
		return true, err
	}
	return true, nil
}
