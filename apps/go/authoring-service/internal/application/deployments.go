package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/google/uuid"
)

const (
	deploymentActionDeploy      = "DEPLOY"
	deploymentActionRollback    = "ROLLBACK"
	deploymentStatusQueued      = "QUEUED"
	deploymentStatusCompiling   = "COMPILING"
	deploymentStatusStaging     = "STAGING"
	deploymentStatusActivating  = "ACTIVATING"
	deploymentStatusActive      = "ACTIVE"
	deploymentStatusRollingBack = "ROLLING_BACK"
	deploymentStatusRolledBack  = "ROLLED_BACK"
	deploymentStatusSuperseded  = "SUPERSEDED"
	deploymentStatusFailed      = "FAILED"
	deploymentMaxAttempts       = int32(20)
)

type CreateDeploymentRequest struct {
	TenantID, ProcessModelID, ProcessModelVersionID, TargetEnvironment string
	CommandID, IdempotencyKey, CorrelationID                           string
	ActorProof                                                         *authv1.ActorProof
}

type RollbackDeploymentRequest struct {
	TenantID, TargetDeploymentID, CommandID, IdempotencyKey, CorrelationID string
	ActorProof                                                             *authv1.ActorProof
}

func (s *Service) SetRuntimeDeploymentEngine(engine RuntimeDeploymentEngine) error {
	if engine == nil {
		return errors.New("runtime deployment Engine client is required")
	}
	s.deploymentEngine = engine
	return nil
}

func (s *Service) CreateRuntimeDeployment(ctx context.Context, request CreateDeploymentRequest) (RuntimeDeployment, bool, error) {
	request.TenantID, request.ProcessModelID = strings.TrimSpace(request.TenantID), strings.TrimSpace(request.ProcessModelID)
	request.ProcessModelVersionID, request.TargetEnvironment = strings.TrimSpace(request.ProcessModelVersionID), strings.TrimSpace(request.TargetEnvironment)
	if request.TenantID == "" || request.ProcessModelID == "" || request.ProcessModelVersionID == "" || len(request.TargetEnvironment) == 0 || len(request.TargetEnvironment) > 128 || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" {
		return RuntimeDeployment{}, false, ErrInvalidArgument
	}
	repository, ok := s.repository.(RuntimeDeploymentRepository)
	if !ok || s.deploymentCompiler == nil || s.deploymentEngine == nil {
		return RuntimeDeployment{}, false, ErrDeploymentUnavailable
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return RuntimeDeployment{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.deployProcessModel", request.ProcessModelID, AuthorizationRequirements{ReadFields: []string{"id", "version_num", "source_revision", "bpmn_xml"}, WriteFields: []string{"runtime_deployment"}}); err != nil {
		return RuntimeDeployment{}, false, ErrForbidden
	}
	digest, err := json.Marshal(struct{ Op, Tenant, Model, Version, Environment string }{"deploy", request.TenantID, request.ProcessModelID, request.ProcessModelVersionID, request.TargetEnvironment})
	if err != nil {
		return RuntimeDeployment{}, false, err
	}
	requestDigest := sha256.Sum256(digest)
	deployment := RuntimeDeployment{ID: uuid.NewString(), TenantID: request.TenantID, ProcessModelID: request.ProcessModelID, ProcessModelVersionID: request.ProcessModelVersionID, TargetEnvironment: request.TargetEnvironment, ActionType: deploymentActionDeploy, Status: deploymentStatusQueued, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()}
	return repository.CreateRuntimeDeployment(ctx, request.TenantID, actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, requestDigest[:], deployment, s.now().UTC())
}

func (s *Service) RollbackRuntimeDeployment(ctx context.Context, request RollbackDeploymentRequest) (RuntimeDeployment, bool, error) {
	if request.TenantID == "" || request.TargetDeploymentID == "" || request.CommandID == "" || request.IdempotencyKey == "" || request.CorrelationID == "" {
		return RuntimeDeployment{}, false, ErrInvalidArgument
	}
	repository, ok := s.repository.(RuntimeDeploymentRepository)
	if !ok || s.deploymentEngine == nil {
		return RuntimeDeployment{}, false, ErrDeploymentUnavailable
	}
	actor, err := s.actor(ctx, request.TenantID, request.CommandID, request.ActorProof)
	if err != nil {
		return RuntimeDeployment{}, false, err
	}
	target, err := repository.GetRuntimeDeployment(ctx, request.TenantID, request.TargetDeploymentID)
	if err != nil {
		return RuntimeDeployment{}, false, err
	}
	if err = s.authorizer.Authorize(ctx, request.TenantID, actor, "authoring.rollbackRuntimeDeployment", target.ProcessModelID, AuthorizationRequirements{ReadFields: []string{"id", "status", "artifact_id"}, WriteFields: []string{"runtime_deployment"}}); err != nil {
		return RuntimeDeployment{}, false, ErrForbidden
	}
	digest, _ := json.Marshal(struct{ Op, Tenant, Target string }{"rollback", request.TenantID, request.TargetDeploymentID})
	requestDigest := sha256.Sum256(digest)
	return repository.CreateRollbackDeployment(ctx, request.TenantID, actor.ID, request.CommandID, request.IdempotencyKey, request.CorrelationID, requestDigest[:], request.TargetDeploymentID, uuid.NewString(), s.now().UTC())
}

func (s *Service) GetRuntimeDeployment(ctx context.Context, tenantID, deploymentID string, proof *authv1.ActorProof) (RuntimeDeployment, error) {
	if tenantID == "" || deploymentID == "" {
		return RuntimeDeployment{}, ErrInvalidArgument
	}
	repository, ok := s.repository.(RuntimeDeploymentRepository)
	if !ok {
		return RuntimeDeployment{}, ErrDeploymentUnavailable
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return RuntimeDeployment{}, err
	}
	deployment, err := repository.GetRuntimeDeployment(ctx, tenantID, deploymentID)
	if err != nil {
		return RuntimeDeployment{}, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.getRuntimeDeployment", deployment.ProcessModelID, AuthorizationRequirements{ReadFields: []string{"id", "status", "artifact_id", "failure_code", "failure_message", "deployment_sequence"}}); err != nil {
		return RuntimeDeployment{}, ErrNotFound
	}
	return deployment, nil
}

func (s *Service) ListRuntimeDeployments(ctx context.Context, tenantID, modelID, environment string, beforeSequence int64, limit int, proof *authv1.ActorProof) ([]RuntimeDeployment, error) {
	if tenantID == "" || limit < 1 || limit > 100 || beforeSequence < 0 {
		return nil, ErrInvalidArgument
	}
	repository, ok := s.repository.(RuntimeDeploymentRepository)
	if !ok {
		return nil, ErrDeploymentUnavailable
	}
	actor, err := s.actor(ctx, tenantID, "", proof)
	if err != nil {
		return nil, err
	}
	if err = s.authorizer.Authorize(ctx, tenantID, actor, "authoring.listRuntimeDeployments", modelID, AuthorizationRequirements{ReadFields: []string{"id", "status", "deployment_sequence", "created_at"}}); err != nil {
		return nil, ErrForbidden
	}
	return repository.ListRuntimeDeployments(ctx, tenantID, modelID, environment, beforeSequence, limit)
}

// RunRuntimeDeploymentWorker recovers queued and expired-lease jobs from the
// owning PostgreSQL database. Each job stage commits before external I/O.
func (s *Service) RunRuntimeDeploymentWorker(ctx context.Context, workerID string, pollInterval, leaseDuration time.Duration) error {
	if strings.TrimSpace(workerID) == "" || pollInterval <= 0 || leaseDuration <= pollInterval {
		return errors.New("runtime deployment worker settings are invalid")
	}
	repository, ok := s.repository.(RuntimeDeploymentRepository)
	if !ok || s.deploymentCompiler == nil || s.deploymentEngine == nil {
		return ErrDeploymentUnavailable
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		jobs, err := repository.ClaimRuntimeDeployments(ctx, workerID, 1, leaseDuration, s.now().UTC())
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Error("claim runtime deployment job", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if len(jobs) > 0 {
			attemptCtx, cancel := context.WithTimeout(ctx, leaseDuration*4/5)
			s.processRuntimeDeployment(attemptCtx, repository, workerID, jobs[0])
			cancel()
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) processRuntimeDeployment(ctx context.Context, repository RuntimeDeploymentRepository, workerID string, deployment RuntimeDeployment) {
	if deployment.ArtifactID == "" {
		version, err := repository.GetDeploymentVersion(ctx, deployment)
		if err != nil {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
			return
		}
		artifact, err := s.deploymentCompiler.CompileRuntimeArtifact(ctx, RuntimeArtifactRequest{
			TenantID: deployment.TenantID, ProcessModelID: deployment.ProcessModelID,
			SourceRevision: version.SourceRevision, WorkflowVersion: version.ID,
			BPMNSourceName: version.ID + ".bpmn", BPMNXML: version.BPMNXML,
		})
		if err != nil {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
			return
		}
		if !artifact.Valid {
			_ = repository.FailRuntimeDeployment(ctx, deployment.TenantID, deployment.ID, workerID, "COMPILE_INVALID", "compiler rejected the process model", s.now().UTC())
			return
		}
		if artifact.WorkflowType == "" || artifact.ArtifactID == "" || len(artifact.SHA256) != 32 || len(artifact.SignedWIR) == 0 {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, errors.New("compiler returned an incomplete signed runtime artifact"))
			return
		}
		if err = repository.SaveDeploymentArtifact(ctx, deployment.TenantID, deployment.ID, workerID, artifact.ArtifactID, artifact.SHA256, artifact.WorkflowType, version.ID, s.now().UTC()); err != nil {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
			return
		}
		deployment.ArtifactID, deployment.ArtifactSHA256 = artifact.ArtifactID, append([]byte(nil), artifact.SHA256...)
		deployment.WorkflowType, deployment.WorkflowVersion = artifact.WorkflowType, version.ID
		deployment.Status = deploymentStatusStaging
		return
	}

	if !deployment.EngineAccepted {
		artifact, err := s.deploymentCompiler.GetRuntimeArtifact(ctx, deployment.ArtifactID)
		if err != nil {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
			return
		}
		if len(deployment.ArtifactSHA256) != 32 || !equalBytes(artifact.SHA256, deployment.ArtifactSHA256) {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, errors.New("compiler artifact changed after deployment staging"))
			return
		}
		rollbackOf := ""
		if deployment.ActionType == deploymentActionRollback {
			rollbackOf = deployment.PreviousDeploymentID
		}
		engineState, err := s.deploymentEngine.CommitRuntimeDeployment(ctx, EngineDeploymentCommand{
			DeploymentID: deployment.ID, TenantID: deployment.TenantID,
			WorkflowType: deployment.WorkflowType, WorkflowVersion: deployment.WorkflowVersion,
			EnvironmentReference: deployment.TargetEnvironment, DeploymentSequence: uint64(deployment.DeploymentSequence),
			RollbackOfDeploymentID: rollbackOf, SignedWIRArtifact: artifact.SignedWIR,
			ArtifactSHA256: deployment.ArtifactSHA256,
		})
		if err != nil {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
			return
		}
		if engineState.DeploymentID != deployment.ID || engineState.Status == "" {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, errors.New("Engine returned an invalid commit receipt"))
			return
		}
		if deployment.ActionType == deploymentActionRollback && engineState.Status == deploymentStatusActive {
			engineState.Status = deploymentStatusRolledBack
		}
		if err = repository.SaveEngineDeploymentState(ctx, deployment.TenantID, deployment.ID, workerID, engineState, nil, s.now().UTC()); err != nil {
			s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
		}
		return
	}

	engineState, err := s.deploymentEngine.GetRuntimeDeployment(ctx, deployment.ID)
	if err != nil {
		s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
		return
	}
	if engineState.Status == "" || engineState.DeploymentID != deployment.ID {
		s.handleDeploymentFailure(ctx, repository, workerID, deployment, errors.New("Engine returned a mismatched deployment status"))
		return
	}
	if deployment.ActionType == deploymentActionRollback && engineState.Status == deploymentStatusActive {
		engineState.Status = deploymentStatusRolledBack
	}
	if err = repository.SaveEngineDeploymentState(ctx, deployment.TenantID, deployment.ID, workerID, engineState, nil, s.now().UTC()); err != nil {
		s.handleDeploymentFailure(ctx, repository, workerID, deployment, err)
	}
}

func (s *Service) handleDeploymentFailure(ctx context.Context, repository RuntimeDeploymentRepository, workerID string, deployment RuntimeDeployment, cause error) {
	slog.Error("runtime deployment attempt failed", "deployment_id", deployment.ID, "attempt", deployment.AttemptCount, "error", cause)
	if deployment.AttemptCount >= deploymentMaxAttempts || isPermanentDeploymentError(cause) {
		if err := repository.FailRuntimeDeployment(ctx, deployment.TenantID, deployment.ID, workerID, "DEPLOYMENT_FAILED", safeFailureMessage(cause), s.now().UTC()); err != nil {
			slog.Error("persist terminal deployment failure", "deployment_id", deployment.ID, "error", err)
		}
		return
	}
	delay := time.Duration(1<<min(deployment.AttemptCount, 8)) * 250 * time.Millisecond
	if delay > time.Minute {
		delay = time.Minute
	}
	if err := repository.ReleaseDeploymentLease(ctx, deployment.TenantID, deployment.ID, workerID, s.now().UTC().Add(delay), safeFailureMessage(cause)); err != nil {
		slog.Error("release runtime deployment lease", "deployment_id", deployment.ID, "error", err)
	}
}

func isPermanentDeploymentError(err error) bool {
	var permanent interface{ Permanent() bool }
	return errors.As(err, &permanent) && permanent.Permanent()
}

func safeFailureMessage(err error) string {
	if err == nil {
		return "deployment failed"
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	return message
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
