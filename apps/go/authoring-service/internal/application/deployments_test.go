package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type deploymentTestRepository struct {
	Repository
	RuntimeDeploymentRepository
	version       ProcessModelVersion
	artifactSaved RuntimeArtifact
	engineState   EngineDeploymentState
	failedCode    string
	failedMessage string
	releasedUntil time.Time
	releaseReason string
	savedState    EngineDeploymentState
}

func (r *deploymentTestRepository) GetDeploymentVersion(context.Context, RuntimeDeployment) (ProcessModelVersion, error) {
	return r.version, nil
}
func (r *deploymentTestRepository) SaveDeploymentArtifact(_ context.Context, _, _, _, artifactID string, digest []byte, workflowType, workflowVersion string, _ time.Time) error {
	r.artifactSaved = RuntimeArtifact{ArtifactID: artifactID, SHA256: append([]byte(nil), digest...), WorkflowType: workflowType, Valid: true}
	r.version.ID = workflowVersion
	return nil
}
func (r *deploymentTestRepository) SaveEngineDeploymentState(_ context.Context, _, _, _ string, state EngineDeploymentState, _ []byte, _ time.Time) error {
	r.savedState = state
	return nil
}
func (r *deploymentTestRepository) FailRuntimeDeployment(_ context.Context, _, _, _, code, message string, _ time.Time) error {
	r.failedCode, r.failedMessage = code, message
	return nil
}
func (r *deploymentTestRepository) ReleaseDeploymentLease(_ context.Context, _, _, _ string, until time.Time, reason string) error {
	r.releasedUntil, r.releaseReason = until, reason
	return nil
}

type deploymentTestCompiler struct {
	Compiler
	RuntimeDeploymentCompiler
	compileResult RuntimeArtifact
	storedResult  RuntimeArtifact
	compileErr    error
}

func (c *deploymentTestCompiler) CompileRuntimeArtifact(context.Context, RuntimeArtifactRequest) (RuntimeArtifact, error) {
	return c.compileResult, c.compileErr
}
func (c *deploymentTestCompiler) GetRuntimeArtifact(context.Context, string) (RuntimeArtifact, error) {
	return c.storedResult, nil
}

type deploymentTestEngine struct {
	RuntimeDeploymentEngine
	commitCommand EngineDeploymentCommand
	commitState   EngineDeploymentState
	currentState  EngineDeploymentState
	committed     *EngineDeploymentCommand
	commitCalls   int
	commitErrOnce error
}

func (e *deploymentTestEngine) CommitRuntimeDeployment(_ context.Context, command EngineDeploymentCommand) (EngineDeploymentState, error) {
	e.commitCalls++
	if e.committed == nil {
		copy := command
		copy.SignedWIRArtifact = append([]byte(nil), command.SignedWIRArtifact...)
		e.committed = &copy
	} else if !reflect.DeepEqual(*e.committed, command) {
		return EngineDeploymentState{}, errors.New("deployment retry changed the durable Engine intent")
	}
	e.commitCommand = command
	if e.commitErrOnce != nil {
		err := e.commitErrOnce
		e.commitErrOnce = nil
		return EngineDeploymentState{}, err
	}
	return e.commitState, nil
}
func (e *deploymentTestEngine) GetRuntimeDeployment(context.Context, string) (EngineDeploymentState, error) {
	return e.currentState, nil
}

func newDeploymentTestService(t *testing.T, repository *deploymentTestRepository, compiler *deploymentTestCompiler, engine *deploymentTestEngine) (*Service, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	service, err := New(repository, decisionTestActors{}, decisionTestPolicy{}, compiler, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetRuntimeDeploymentEngine(engine); err != nil {
		t.Fatal(err)
	}
	return service, now
}

func TestDeploymentWorkerStagesAndReconcilesEngineReceipt(t *testing.T) {
	digest := make([]byte, 32)
	digest[0] = 7
	repository := &deploymentTestRepository{version: ProcessModelVersion{ID: "version-7", TenantID: "tenant-1", ProcessModelID: "model-1", SourceRevision: 7, BPMNXML: "<definitions/>"}}
	compiler := &deploymentTestCompiler{compileResult: RuntimeArtifact{Valid: true, ArtifactID: "artifact-7", WorkflowType: "workflow-1", SHA256: digest, SignedWIR: []byte("signed-wir")}, storedResult: RuntimeArtifact{ArtifactID: "artifact-7", WorkflowType: "workflow-1", SHA256: digest, SignedWIR: []byte("signed-wir")}}
	engine := &deploymentTestEngine{commitState: EngineDeploymentState{DeploymentID: "deployment-1", Status: "ACTIVATING"}, currentState: EngineDeploymentState{DeploymentID: "deployment-1", Status: deploymentStatusActive}}
	service, _ := newDeploymentTestService(t, repository, compiler, engine)
	deployment := RuntimeDeployment{ID: "deployment-1", TenantID: "tenant-1", ProcessModelID: "model-1", ProcessModelVersionID: "version-7", TargetEnvironment: "prod", DeploymentSequence: 42}

	service.processRuntimeDeployment(context.Background(), repository, "worker-1", deployment)
	if repository.artifactSaved.ArtifactID != "artifact-7" || repository.artifactSaved.WorkflowType != "workflow-1" || len(repository.artifactSaved.SHA256) != 32 {
		t.Fatalf("compiled artifact was not durably staged: %+v", repository.artifactSaved)
	}

	deployment.ArtifactID, deployment.ArtifactSHA256 = "artifact-7", digest
	deployment.WorkflowType, deployment.WorkflowVersion = "workflow-1", "version-7"
	service.processRuntimeDeployment(context.Background(), repository, "worker-1", deployment)
	if engine.commitCommand.DeploymentID != deployment.ID || engine.commitCommand.DeploymentSequence != 42 || string(engine.commitCommand.SignedWIRArtifact) != "signed-wir" {
		t.Fatalf("Engine did not receive the staged deployment artifact: %+v", engine.commitCommand)
	}
	if repository.savedState.Status != "ACTIVATING" {
		t.Fatalf("Engine commit receipt was not persisted: %+v", repository.savedState)
	}

	deployment.EngineAccepted = true
	service.processRuntimeDeployment(context.Background(), repository, "worker-1", deployment)
	if repository.savedState.Status != deploymentStatusActive {
		t.Fatalf("Engine state was not reconciled after commit: %+v", repository.savedState)
	}
}

func TestDeploymentWorkerRecoversWhenEngineCommitReceiptIsLost(t *testing.T) {
	digest := make([]byte, 32)
	digest[0] = 9
	repository := &deploymentTestRepository{}
	compiler := &deploymentTestCompiler{storedResult: RuntimeArtifact{ArtifactID: "artifact-9", SHA256: digest, SignedWIR: []byte("signed-wir-9")}}
	engine := &deploymentTestEngine{
		commitState:   EngineDeploymentState{DeploymentID: "deployment-recovery", Status: "ACTIVATING", PendingNodeIDs: []uint64{2}},
		currentState:  EngineDeploymentState{DeploymentID: "deployment-recovery", Status: deploymentStatusActive, AppliedNodeIDs: []uint64{1, 2}},
		commitErrOnce: errors.New("Engine committed intent but response was lost"),
	}
	service, now := newDeploymentTestService(t, repository, compiler, engine)
	deployment := RuntimeDeployment{
		ID: "deployment-recovery", TenantID: "tenant-1", ProcessModelID: "model-1",
		ProcessModelVersionID: "version-9", TargetEnvironment: "prod", DeploymentSequence: 9,
		ArtifactID: "artifact-9", ArtifactSHA256: digest, WorkflowType: "workflow-1", WorkflowVersion: "version-9",
		AttemptCount: 1,
	}

	service.processRuntimeDeployment(context.Background(), repository, "worker-1", deployment)
	if engine.commitCalls != 1 || engine.committed == nil || repository.savedState.Status != "" {
		t.Fatalf("lost Engine response should release the job without persisting a receipt: calls=%d saved=%+v", engine.commitCalls, repository.savedState)
	}
	if !repository.releasedUntil.Equal(now.Add(500 * time.Millisecond)) {
		t.Fatalf("lost Engine response did not schedule bounded recovery: %s", repository.releasedUntil)
	}

	deployment.AttemptCount++
	service.processRuntimeDeployment(context.Background(), repository, "worker-2", deployment)
	if engine.commitCalls != 2 || repository.savedState.Status != "ACTIVATING" || len(repository.savedState.PendingNodeIDs) != 1 {
		t.Fatalf("retry did not recover the same Engine intent or preserve partial ACK state: calls=%d state=%+v", engine.commitCalls, repository.savedState)
	}

	deployment.EngineAccepted = true
	service.processRuntimeDeployment(context.Background(), repository, "worker-2", deployment)
	if repository.savedState.Status != deploymentStatusActive || len(repository.savedState.AppliedNodeIDs) != 2 {
		t.Fatalf("deployment did not reconcile to ACTIVE after all Raft members applied it: %+v", repository.savedState)
	}
}

func TestDeploymentWorkerNeverMarksPartialEngineAcknowledgementActive(t *testing.T) {
	repository := &deploymentTestRepository{}
	engine := &deploymentTestEngine{currentState: EngineDeploymentState{
		DeploymentID: "deployment-partial", Status: "ACTIVATING",
		AppliedNodeIDs: []uint64{1}, PendingNodeIDs: []uint64{2},
	}}
	service, _ := newDeploymentTestService(t, repository, &deploymentTestCompiler{}, engine)
	service.processRuntimeDeployment(context.Background(), repository, "worker-1", RuntimeDeployment{
		ID: "deployment-partial", TenantID: "tenant-1", ArtifactID: "artifact-partial", EngineAccepted: true,
	})
	if repository.savedState.Status != "ACTIVATING" || len(repository.savedState.PendingNodeIDs) != 1 {
		t.Fatalf("partial Raft acknowledgement was incorrectly treated as active: %+v", repository.savedState)
	}
}

func TestDeploymentWorkerReconcilesRollbackAfterEngineCommit(t *testing.T) {
	digest := make([]byte, 32)
	digest[0] = 11
	repository := &deploymentTestRepository{}
	compiler := &deploymentTestCompiler{storedResult: RuntimeArtifact{ArtifactID: "artifact-target", SHA256: digest, SignedWIR: []byte("signed-target")}}
	engine := &deploymentTestEngine{commitState: EngineDeploymentState{DeploymentID: "rollback-job", Status: "ROLLING_BACK"}}
	service, _ := newDeploymentTestService(t, repository, compiler, engine)
	deployment := RuntimeDeployment{
		ID: "rollback-job", TenantID: "tenant-1", ActionType: deploymentActionRollback,
		PreviousDeploymentID: "deployment-target", DeploymentSequence: 12,
		TargetEnvironment: "prod", ArtifactID: "artifact-target", ArtifactSHA256: digest,
		WorkflowType: "workflow-1", WorkflowVersion: "version-target",
	}

	service.processRuntimeDeployment(context.Background(), repository, "worker-1", deployment)
	if engine.commitCommand.RollbackOfDeploymentID != "deployment-target" || repository.savedState.Status != "ROLLING_BACK" {
		t.Fatalf("rollback intent or pending status was not persisted: command=%+v state=%+v", engine.commitCommand, repository.savedState)
	}
	deployment.EngineAccepted = true
	engine.currentState = EngineDeploymentState{DeploymentID: "rollback-job", Status: deploymentStatusActive}
	service.processRuntimeDeployment(context.Background(), repository, "worker-1", deployment)
	if repository.savedState.Status != deploymentStatusRolledBack {
		t.Fatalf("Engine-confirmed rollback was not recorded as rolled back: %+v", repository.savedState)
	}
}

func TestDeploymentWorkerRejectsChangedArtifactAndReleasesLease(t *testing.T) {
	wantDigest, changedDigest := make([]byte, 32), make([]byte, 32)
	wantDigest[0], changedDigest[0] = 1, 2
	repository := &deploymentTestRepository{}
	compiler := &deploymentTestCompiler{storedResult: RuntimeArtifact{SHA256: changedDigest, SignedWIR: []byte("changed")}}
	engine := &deploymentTestEngine{}
	service, now := newDeploymentTestService(t, repository, compiler, engine)
	service.processRuntimeDeployment(context.Background(), repository, "worker-1", RuntimeDeployment{ID: "deployment-1", TenantID: "tenant-1", ArtifactID: "artifact-1", ArtifactSHA256: wantDigest, AttemptCount: 2})
	if repository.failedCode != "" {
		// A changed artifact is retryable until the configured attempt limit.
		t.Fatalf("changed artifact should be retried, not marked terminal: code=%q", repository.failedCode)
	}
	if !repository.releasedUntil.Equal(now.Add(time.Second)) || repository.releaseReason == "" {
		t.Fatalf("lease was not released with bounded backoff: until=%s reason=%q", repository.releasedUntil, repository.releaseReason)
	}
	if engine.commitCommand.DeploymentID != "" {
		t.Fatal("Engine must not receive a changed artifact")
	}
}

func TestDeploymentWorkerFailsInvalidCompileWithoutCallingEngine(t *testing.T) {
	repository := &deploymentTestRepository{version: ProcessModelVersion{ID: "version-1", SourceRevision: 1, BPMNXML: "<invalid/>"}}
	compiler := &deploymentTestCompiler{compileResult: RuntimeArtifact{Valid: false}}
	engine := &deploymentTestEngine{}
	service, _ := newDeploymentTestService(t, repository, compiler, engine)
	service.processRuntimeDeployment(context.Background(), repository, "worker-1", RuntimeDeployment{ID: "deployment-1", TenantID: "tenant-1"})
	if repository.failedCode != "COMPILE_INVALID" {
		t.Fatalf("invalid compile was not terminally recorded: %+v", repository)
	}
	if engine.commitCommand.DeploymentID != "" {
		t.Fatal("invalid compile must never be committed to Engine")
	}
}

func TestDeploymentWorkerRetriesCompilerFailureAndStopsAfterAttemptLimit(t *testing.T) {
	cause := errors.New("compiler unavailable")
	for _, testCase := range []struct {
		name        string
		attempt     int32
		wantFailure bool
	}{
		{name: "retry", attempt: 1},
		{name: "terminal", attempt: deploymentMaxAttempts, wantFailure: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &deploymentTestRepository{version: ProcessModelVersion{ID: "version-1", SourceRevision: 1, BPMNXML: "<definitions/>"}}
			compiler := &deploymentTestCompiler{compileErr: cause}
			service, now := newDeploymentTestService(t, repository, compiler, &deploymentTestEngine{})
			service.processRuntimeDeployment(context.Background(), repository, "worker-1", RuntimeDeployment{ID: "deployment-1", TenantID: "tenant-1", AttemptCount: testCase.attempt})
			if testCase.wantFailure {
				if repository.failedCode != "DEPLOYMENT_FAILED" || repository.releaseReason != "" {
					t.Fatalf("attempt limit did not produce a terminal failure: %+v", repository)
				}
				return
			}
			if repository.failedCode != "" || !repository.releasedUntil.Equal(now.Add(500*time.Millisecond)) {
				t.Fatalf("transient compiler failure did not release the lease for retry: %+v", repository)
			}
		})
	}
}
