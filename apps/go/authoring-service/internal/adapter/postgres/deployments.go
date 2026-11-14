package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const deploymentColumns = `tenant_id,id,process_model_id,process_model_version_id,target_environment,deployment_sequence,action_type,status,COALESCE(artifact_id,''),COALESCE(artifact_sha256,''::bytea),COALESCE(workflow_type,''),COALESCE(workflow_version,''),COALESCE(previous_deployment_id::text,''),COALESCE(failure_code,''),COALESCE(failure_message,''),COALESCE(engine_config_version,''),COALESCE(engine_policy_version,''),COALESCE(engine_config_sha256,''),COALESCE(governance_config_version,''),COALESCE(governance_policy_version,''),attempt_count,(engine_receipt IS NOT NULL),created_at,updated_at,activated_at,rolled_back_at`

func (s *Store) CreateRuntimeDeployment(ctx context.Context, tenantID, actorID, commandID, key, correlation string, digest []byte, item application.RuntimeDeployment, at time.Time) (application.RuntimeDeployment, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil {
		return item, false, err
	}
	model, err := uuid.Parse(item.ProcessModelID)
	if err != nil {
		return item, false, application.ErrInvalidArgument
	}
	version, err := uuid.Parse(item.ProcessModelVersionID)
	if err != nil {
		return item, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return item, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return item, false, err
	}
	if replay, found, e := replayDeployment(ctx, tx, tenant, actor, key, command, "deploy_process_model", digest); e != nil || found {
		if e != nil {
			return item, false, e
		}
		return replay, true, tx.Commit(ctx)
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return item, false, err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM process_model_versions v JOIN process_models m ON m.tenant_id=v.tenant_id AND m.id=v.process_model_id WHERE v.tenant_id=$1 AND v.process_model_id=$2 AND v.id=$3 AND NOT m.is_deleted)`, tenant, model, version).Scan(&valid); err != nil {
		return item, false, err
	}
	if !valid {
		return item, false, application.ErrNotFound
	}
	if err = tx.QueryRow(ctx, `INSERT INTO runtime_deployment_sequences(tenant_id,target_environment,last_sequence,updated_at) VALUES($1,$2,1,$3) ON CONFLICT(tenant_id,target_environment) DO UPDATE SET last_sequence=runtime_deployment_sequences.last_sequence+1,updated_at=EXCLUDED.updated_at RETURNING last_sequence`, tenant, item.TargetEnvironment, at).Scan(&item.DeploymentSequence); err != nil {
		return item, false, err
	}
	item.ID, item.TenantID, item.Status, item.ActionType = uuid.NewString(), tenant.String(), "QUEUED", "DEPLOY"
	return insertDeploymentCommand(ctx, tx, tenant, actor, command, key, correlation, digest, item, "deploy_process_model", at)
}

func (s *Store) CreateRollbackDeployment(ctx context.Context, tenantID, actorID, commandID, key, correlation string, digest []byte, targetID, newID string, at time.Time) (application.RuntimeDeployment, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil {
		return application.RuntimeDeployment{}, false, err
	}
	target, err := uuid.Parse(targetID)
	if err != nil {
		return application.RuntimeDeployment{}, false, application.ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.RuntimeDeployment{}, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return application.RuntimeDeployment{}, false, err
	}
	if replay, found, e := replayDeployment(ctx, tx, tenant, actor, key, command, "rollback_runtime_deployment", digest); e != nil || found {
		if e != nil {
			return replay, false, e
		}
		return replay, true, tx.Commit(ctx)
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.RuntimeDeployment{}, false, err
	}
	item, err := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE tenant_id=$1 AND id=$2 AND artifact_id IS NOT NULL AND status IN ('ACTIVE','SUPERSEDED','ROLLED_BACK')`, tenant, target))
	if errors.Is(err, pgx.ErrNoRows) {
		return item, false, application.ErrConflict
	}
	if err != nil {
		return item, false, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO runtime_deployment_sequences(tenant_id,target_environment,last_sequence,updated_at) VALUES($1,$2,1,$3) ON CONFLICT(tenant_id,target_environment) DO UPDATE SET last_sequence=runtime_deployment_sequences.last_sequence+1,updated_at=EXCLUDED.updated_at RETURNING last_sequence`, tenant, item.TargetEnvironment, at).Scan(&item.DeploymentSequence); err != nil {
		return item, false, err
	}
	item.ID, item.TenantID, item.Status, item.ActionType, item.PreviousDeploymentID = newID, tenant.String(), "ROLLING_BACK", "ROLLBACK", target.String()
	return insertDeploymentCommand(ctx, tx, tenant, actor, command, key, correlation, digest, item, "rollback_runtime_deployment", at)
}

func insertDeploymentCommand(ctx context.Context, tx pgx.Tx, tenant, actor, command uuid.UUID, key, correlation string, digest []byte, item application.RuntimeDeployment, operation string, at time.Time) (application.RuntimeDeployment, bool, error) {
	id, err := uuid.Parse(item.ID)
	if err != nil {
		return item, false, err
	}
	model, _ := uuid.Parse(item.ProcessModelID)
	version, _ := uuid.Parse(item.ProcessModelVersionID)
	_, err = tx.Exec(ctx, `INSERT INTO runtime_deployments(tenant_id,id,process_model_id,process_model_version_id,target_environment,deployment_sequence,status,action_type,previous_deployment_id,actor_id,command_id,idempotency_key,request_digest,correlation_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid,$10,$11,$12,$13,$14,$15,$15)`, tenant, id, model, version, item.TargetEnvironment, item.DeploymentSequence, item.Status, item.ActionType, item.PreviousDeploymentID, actor, command, key, digest, correlation, at)
	if err != nil {
		return item, false, mapConflict(err)
	}
	action := "authoring.deployProcessModel"
	if item.ActionType == "ROLLBACK" {
		action = "authoring.rollbackRuntimeDeployment"
	}
	if err = insertAudit(ctx, tx, tenant, model, actor, command, correlation, action, nil, digest, at, map[string]any{"deployment_id": id.String(), "sequence": item.DeploymentSequence, "environment": item.TargetEnvironment, "action_type": item.ActionType}); err != nil {
		return item, false, err
	}
	if err = insertDeploymentOutbox(ctx, tx, tenant, id, "RuntimeDeploymentQueued", item, at); err != nil {
		return item, false, err
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return item, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, tenant, key, command, operation, digest, raw, at)
	if err != nil {
		return item, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return item, false, err
	}
	item.CreatedAt, item.UpdatedAt = at, at
	return item, false, nil
}

func replayDeployment(ctx context.Context, tx pgx.Tx, tenant, actor uuid.UUID, key string, command uuid.UUID, operation string, digest []byte) (application.RuntimeDeployment, bool, error) {
	var storedCommand uuid.UUID
	var storedOperation string
	var storedDigest, raw []byte
	err := tx.QueryRow(ctx, `SELECT command_id,operation,request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&storedCommand, &storedOperation, &storedDigest, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.RuntimeDeployment{}, false, nil
	}
	if err != nil {
		return application.RuntimeDeployment{}, false, err
	}
	if storedCommand != command || storedOperation != operation || !bytesEqual(storedDigest, digest) {
		return application.RuntimeDeployment{}, false, application.ErrIdempotencyConflict
	}
	var item application.RuntimeDeployment
	if err = json.Unmarshal(raw, &item); err != nil {
		return item, false, err
	}
	if item.TenantID != tenant.String() {
		return item, false, application.ErrNotFound
	}
	return item, true, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}
func insertDeploymentOutbox(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, event string, item application.RuntimeDeployment, at time.Time) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO runtime_deployment_outbox(tenant_id,id,deployment_id,event_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6)`, tenant, uuid.New(), id, event, raw, at)
	return err
}

func scanDeployment(row interface{ Scan(...any) error }) (application.RuntimeDeployment, error) {
	var d application.RuntimeDeployment
	var tenant, id, model, version uuid.UUID
	var previous string
	err := row.Scan(&tenant, &id, &model, &version, &d.TargetEnvironment, &d.DeploymentSequence, &d.ActionType, &d.Status, &d.ArtifactID, &d.ArtifactSHA256, &d.WorkflowType, &d.WorkflowVersion, &previous, &d.FailureCode, &d.FailureMessage, &d.EngineConfigVersion, &d.EnginePolicyVersion, &d.EngineConfigSHA256, &d.GovernanceConfigVersion, &d.GovernancePolicyVersion, &d.AttemptCount, &d.EngineAccepted, &d.CreatedAt, &d.UpdatedAt, &d.ActivatedAt, &d.RolledBackAt)
	d.TenantID, d.ID, d.ProcessModelID, d.ProcessModelVersionID, d.PreviousDeploymentID = tenant.String(), id.String(), model.String(), version.String(), previous
	return d, err
}

func (s *Store) GetRuntimeDeployment(ctx context.Context, tenantID, id string) (application.RuntimeDeployment, error) {
	tenant, e := uuid.Parse(tenantID)
	if e != nil {
		return application.RuntimeDeployment{}, application.ErrNotFound
	}
	deployment, e := uuid.Parse(id)
	if e != nil {
		return application.RuntimeDeployment{}, application.ErrNotFound
	}
	d, err := scanDeployment(s.pool.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE tenant_id=$1 AND id=$2`, tenant, deployment))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, application.ErrNotFound
	}
	return d, err
}

func (s *Store) ListRuntimeDeployments(ctx context.Context, tenantID, modelID, environment string, before int64, limit int) ([]application.RuntimeDeployment, error) {
	tenant, e := uuid.Parse(tenantID)
	if e != nil {
		return nil, application.ErrInvalidArgument
	}
	var model any
	if modelID != "" {
		parsed, err := uuid.Parse(modelID)
		if err != nil {
			return nil, application.ErrInvalidArgument
		}
		model = parsed
	}
	rows, e := s.pool.Query(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE tenant_id=$1 AND ($2::uuid IS NULL OR process_model_id=$2) AND ($3='' OR target_environment=$3) AND ($4<=0 OR deployment_sequence<$4) ORDER BY deployment_sequence DESC LIMIT $5`, tenant, model, environment, before, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := make([]application.RuntimeDeployment, 0, limit)
	for rows.Next() {
		item, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ClaimRuntimeDeployments(ctx context.Context, owner string, limit int, lease time.Duration, now time.Time) ([]application.RuntimeDeployment, error) {
	if owner == "" || limit < 1 || limit > 32 || lease <= 0 {
		return nil, application.ErrInvalidArgument
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE status IN ('QUEUED','COMPILING','STAGING','ACTIVATING','ROLLING_BACK') AND next_attempt_at<=$1 AND (lease_until IS NULL OR lease_until<=$1) ORDER BY next_attempt_at,created_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if e != nil {
		return nil, e
	}
	items := make([]application.RuntimeDeployment, 0, limit)
	for rows.Next() {
		item, err := scanDeployment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for i := range items {
		_, e = tx.Exec(ctx, `UPDATE runtime_deployments SET lease_owner=$3,lease_until=$4,attempt_count=attempt_count+CASE WHEN status='ACTIVATING' THEN 0 ELSE 1 END,status=CASE WHEN status='QUEUED' THEN 'COMPILING' ELSE status END,updated_at=$5 WHERE tenant_id=$1 AND id=$2`, items[i].TenantID, items[i].ID, owner, now.Add(lease), now)
		if e != nil {
			return nil, e
		}
		if items[i].Status != "ACTIVATING" {
			items[i].AttemptCount++
		}
		if items[i].Status == "QUEUED" {
			items[i].Status = "COMPILING"
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return items, nil
}

func (s *Store) GetDeploymentVersion(ctx context.Context, d application.RuntimeDeployment) (application.ProcessModelVersion, error) {
	return s.GetProcessModelVersion(ctx, d.TenantID, d.ProcessModelID, d.ProcessModelVersionID)
}

func (s *Store) SaveDeploymentArtifact(ctx context.Context, tenantID, id, owner, artifactID string, digest []byte, workflowType, workflowVersion string, at time.Time) error {
	tenant, e := uuid.Parse(tenantID)
	if e != nil {
		return application.ErrNotFound
	}
	deployment, e := uuid.Parse(id)
	if e != nil {
		return application.ErrNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE runtime_deployments SET artifact_id=$4,artifact_sha256=$5,workflow_type=$6,workflow_version=$7,status='STAGING',updated_at=$8,lease_owner=NULL,lease_until=NULL WHERE tenant_id=$1 AND id=$2 AND lease_owner=$3 AND status IN ('COMPILING','STAGING')`, tenant, deployment, owner, artifactID, digest, workflowType, workflowVersion, at)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return application.ErrConflict
	}
	d, e := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE tenant_id=$1 AND id=$2`, tenant, deployment))
	if e != nil {
		return e
	}
	if e = insertDeploymentOutbox(ctx, tx, tenant, deployment, "RuntimeDeploymentStaged", d, at); e != nil {
		return e
	}
	if e = insertDeploymentTransitionAudit(ctx, tx, tenant, deployment, d.Status, at); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) SaveEngineDeploymentState(ctx context.Context, tenantID, id, owner string, state application.EngineDeploymentState, receipt []byte, at time.Time) error {
	tenant, e := uuid.Parse(tenantID)
	if e != nil {
		return application.ErrNotFound
	}
	deployment, e := uuid.Parse(id)
	if e != nil {
		return application.ErrNotFound
	}
	raw, _ := json.Marshal(state)
	if len(receipt) > 0 {
		raw = receipt
	}
	status := state.Status
	if state.DeploymentID != id || (status != "ACTIVE" && status != "ACTIVATING" && status != "ROLLING_BACK" && status != "ROLLED_BACK" && status != "SUPERSEDED" && status != "FAILED") {
		return application.ErrConflict
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE runtime_deployments SET status=$4,engine_receipt=$5,engine_config_version=NULLIF($6,''),engine_policy_version=NULLIF($7,''),engine_config_sha256=NULLIF($8,''),governance_config_version=NULLIF($9,''),governance_policy_version=NULLIF($10,''),failure_code=COALESCE(NULLIF($11,''),failure_code),activated_at=CASE WHEN $4='ACTIVE' AND action_type='DEPLOY' THEN $12 ELSE activated_at END,rolled_back_at=CASE WHEN $4='ROLLED_BACK' THEN $12 ELSE rolled_back_at END,lease_owner=NULL,lease_until=NULL,next_attempt_at=$12+CASE WHEN $4 IN ('ACTIVATING','ROLLING_BACK') THEN interval '1 second' ELSE interval '0 seconds' END,updated_at=$12 WHERE tenant_id=$1 AND id=$2 AND lease_owner=$3`, tenant, deployment, owner, status, raw, state.EngineConfigVersion, state.EnginePolicyVersion, state.EngineConfigSHA256, state.GovernanceConfigVersion, state.GovernancePolicyVersion, state.FailureCode, at)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return application.ErrConflict
	}
	d, e := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE tenant_id=$1 AND id=$2`, tenant, deployment))
	if e != nil {
		return e
	}
	if e = insertDeploymentOutbox(ctx, tx, tenant, deployment, "RuntimeDeploymentUpdated", d, at); e != nil {
		return e
	}
	if e = insertDeploymentTransitionAudit(ctx, tx, tenant, deployment, d.Status, at); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleaseDeploymentLease(ctx context.Context, tenantID, id, owner string, next time.Time, message string) error {
	tenant, e := uuid.Parse(tenantID)
	if e != nil {
		return application.ErrNotFound
	}
	deployment, e := uuid.Parse(id)
	if e != nil {
		return application.ErrNotFound
	}
	tag, e := s.pool.Exec(ctx, `UPDATE runtime_deployments SET lease_owner=NULL,lease_until=NULL,next_attempt_at=$4,failure_message=$5,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND lease_owner=$3 AND status NOT IN ('ACTIVE','ROLLED_BACK','SUPERSEDED','FAILED')`, tenant, deployment, owner, next, message)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return application.ErrConflict
	}
	return nil
}

func (s *Store) FailRuntimeDeployment(ctx context.Context, tenantID, id, owner, code, message string, at time.Time) error {
	tenant, e := uuid.Parse(tenantID)
	if e != nil {
		return application.ErrNotFound
	}
	deployment, e := uuid.Parse(id)
	if e != nil {
		return application.ErrNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE runtime_deployments SET status='FAILED',failure_code=$4,failure_message=$5,lease_owner=NULL,lease_until=NULL,updated_at=$6 WHERE tenant_id=$1 AND id=$2 AND lease_owner=$3`, tenant, deployment, owner, code, message, at)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return application.ErrConflict
	}
	d, e := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM runtime_deployments WHERE tenant_id=$1 AND id=$2`, tenant, deployment))
	if e != nil {
		return e
	}
	if e = insertDeploymentOutbox(ctx, tx, tenant, deployment, "RuntimeDeploymentFailed", d, at); e != nil {
		return e
	}
	if e = insertDeploymentTransitionAudit(ctx, tx, tenant, deployment, d.Status, at); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func insertDeploymentTransitionAudit(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, status string, at time.Time) error {
	var model, actor, command uuid.UUID
	var correlation string
	if err := tx.QueryRow(ctx, `SELECT process_model_id,actor_id,command_id,correlation_id FROM runtime_deployments WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&model, &actor, &command, &correlation); err != nil {
		return err
	}
	return insertAudit(ctx, tx, tenant, model, actor, command, correlation, "authoring.runtimeDeploymentStatus", nil, nil, at, map[string]any{"deployment_id": id.String(), "status": status})
}

var _ application.RuntimeDeploymentRepository = (*Store)(nil)
