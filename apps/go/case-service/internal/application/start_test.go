package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStartRepo struct {
	job         StartJob
	retryJob    StartJob
	completeJob StartJob
	next        time.Time
}

func (r *fakeStartRepo) ClaimStart(_ context.Context, _ time.Time, _ time.Duration, token uuid.UUID) (StartJob, bool, error) {
	r.job.LeaseToken = token
	r.job.Attempts++
	return r.job, true, nil
}
func (r *fakeStartRepo) CompleteStart(_ context.Context, job StartJob, _ time.Time, _ uuid.UUID) error {
	r.completeJob = job
	return nil
}
func (r *fakeStartRepo) RetryStart(_ context.Context, job StartJob, _, next time.Time, _ string) error {
	r.retryJob, r.next = job, next
	return nil
}

type fakeStartPort struct {
	jobs []StartJob
	fail bool
}

func (p *fakeStartPort) StartWorkflow(_ context.Context, job StartJob) error {
	p.jobs = append(p.jobs, job)
	if p.fail {
		return errors.New("ambiguous Engine response")
	}
	return nil
}

func TestStartRunnerReusesStableCommandAfterAmbiguousEngineResponse(t *testing.T) {
	caseID := uuid.New()
	repo := &fakeStartRepo{job: StartJob{TenantID: uuid.New(), CaseID: caseID, CommandID: caseID, ActorID: "alice"}}
	engine := &fakeStartPort{fail: true}
	now := time.Unix(100, 0).UTC()
	runner, err := NewStartRunner(repo, engine, func() time.Time { return now }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := runner.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("first attempt: handled=%v err=%v", handled, err)
	}
	if repo.retryJob.CommandID != caseID || !repo.next.Equal(now.Add(time.Second)) {
		t.Fatalf("retry lost stable identity or backoff: %+v %v", repo.retryJob, repo.next)
	}
	engine.fail = false
	if handled, err := runner.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("second attempt: handled=%v err=%v", handled, err)
	}
	if len(engine.jobs) != 2 || engine.jobs[0].CommandID != engine.jobs[1].CommandID || engine.jobs[0].CaseID != engine.jobs[1].CaseID || repo.completeJob.CommandID != caseID {
		t.Fatalf("retry changed Engine identity: calls=%+v complete=%+v", engine.jobs, repo.completeJob)
	}
}
