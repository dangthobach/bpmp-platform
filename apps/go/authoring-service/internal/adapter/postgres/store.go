package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("authoring PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) ListModels(ctx context.Context, tenantID, search, pageToken string, limit int) ([]application.Model, string, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, "", application.ErrInvalidArgument
	}
	cursor, err := decodeCursor(pageToken)
	if err != nil {
		return nil, "", application.ErrInvalidArgument
	}
	var cursorID any
	if cursor != nil {
		cursorID = cursor.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,model_key,name,category,description,status,revision,
		COALESCE(latest_version_id::text,''),COALESCE((SELECT v.source_revision FROM process_model_versions v WHERE v.tenant_id=process_models.tenant_id AND v.id=process_models.latest_version_id),0),created_at,updated_at,is_deleted
		FROM process_models WHERE tenant_id=$1 AND NOT is_deleted
		AND ($2='' OR lower(name) LIKE '%'||lower($2)||'%' OR lower(model_key) LIKE '%'||lower($2)||'%')
		AND ($3::uuid IS NULL OR id>$3)
		ORDER BY id LIMIT $4`, tenant, strings.TrimSpace(search), cursorID, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	models := make([]application.Model, 0, limit+1)
	for rows.Next() {
		model, scanErr := scanModel(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		models = append(models, model)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(models) <= limit {
		return models, "", nil
	}
	models = models[:limit]
	last := models[len(models)-1]
	next, err := encodeCursor(pageCursor{ID: last.ID})
	return models, next, err
}

func (s *Store) GetModel(ctx context.Context, tenantID, modelID string) (application.Model, application.Draft, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.Model{}, application.Draft{}, application.ErrNotFound
	}
	id, err := uuid.Parse(modelID)
	if err != nil {
		return application.Model{}, application.Draft{}, application.ErrNotFound
	}
	model, err := scanModel(s.pool.QueryRow(ctx, `SELECT id,tenant_id,model_key,name,category,description,status,revision,
		COALESCE(latest_version_id::text,''),COALESCE((SELECT v.source_revision FROM process_model_versions v WHERE v.tenant_id=process_models.tenant_id AND v.id=process_models.latest_version_id),0),created_at,updated_at,is_deleted
		FROM process_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Model{}, application.Draft{}, application.ErrNotFound
	}
	if err != nil {
		return application.Model{}, application.Draft{}, err
	}
	draft, err := scanDraft(s.pool.QueryRow(ctx, `SELECT process_model_id,revision,COALESCE(base_version_id::text,''),bpmn_xml,
		validation_status,diagnostics,updated_at FROM process_model_drafts WHERE tenant_id=$1 AND process_model_id=$2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Model{}, application.Draft{}, application.ErrNotFound
	}
	return model, draft, err
}

func (s *Store) ListDecisionModels(ctx context.Context, tenantID, search, pageToken string, limit int) ([]application.DecisionModel, string, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, "", application.ErrInvalidArgument
	}
	cursor, err := decodeCursor(pageToken)
	if err != nil {
		return nil, "", application.ErrInvalidArgument
	}
	var cursorID any
	if cursor != nil {
		cursorID = cursor.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,model_key,name,description,status,revision,COALESCE((SELECT v.source_revision FROM decision_model_versions v WHERE v.tenant_id=decision_models.tenant_id AND v.id=decision_models.latest_version_id),0),created_at,updated_at FROM decision_models WHERE tenant_id=$1 AND NOT is_deleted AND ($2='' OR lower(name) LIKE '%'||lower($2)||'%' OR lower(model_key) LIKE '%'||lower($2)||'%') AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, tenant, strings.TrimSpace(search), cursorID, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	models := make([]application.DecisionModel, 0, limit+1)
	for rows.Next() {
		m, scanErr := scanDecisionModel(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		models = append(models, m)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(models) <= limit {
		return models, "", nil
	}
	models = models[:limit]
	next, err := encodeCursor(pageCursor{ID: models[len(models)-1].ID})
	return models, next, err
}

func (s *Store) GetDecisionModel(ctx context.Context, tenantID, modelID string) (application.DecisionModel, application.DecisionDraft, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, application.ErrNotFound
	}
	id, err := uuid.Parse(modelID)
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, application.ErrNotFound
	}
	model, err := scanDecisionModel(s.pool.QueryRow(ctx, `SELECT id,tenant_id,model_key,name,description,status,revision,COALESCE((SELECT v.source_revision FROM decision_model_versions v WHERE v.tenant_id=decision_models.tenant_id AND v.id=decision_models.latest_version_id),0),created_at,updated_at FROM decision_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModel{}, application.DecisionDraft{}, application.ErrNotFound
	}
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, err
	}
	draft, err := scanDecisionDraft(s.pool.QueryRow(ctx, `SELECT decision_model_id,revision,dmn_xml,validation_status,diagnostics,updated_at FROM decision_model_drafts WHERE tenant_id=$1 AND decision_model_id=$2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModel{}, application.DecisionDraft{}, application.ErrNotFound
	}
	return model, draft, err
}

func (s *Store) CreateDecisionModel(ctx context.Context, model application.DecisionModel, draft application.DecisionDraft, actorID, commandID, idempotencyKey, correlationID string, digest []byte) (application.DecisionModel, application.DecisionDraft, bool, error) {
	tenant, actor, command, err := parseIDs(model.TenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size {
		return application.DecisionModel{}, application.DecisionDraft{}, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, idempotencyKey); err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	var priorDigest, priorResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey).Scan(&priorDigest, &priorResponse)
	if err == nil {
		if subtle.ConstantTimeCompare(priorDigest, digest) != 1 {
			return application.DecisionModel{}, application.DecisionDraft{}, false, application.ErrIdempotencyConflict
		}
		var saved struct {
			Model application.DecisionModel `json:"model"`
			Draft application.DecisionDraft `json:"draft"`
		}
		if err = json.Unmarshal(priorResponse, &saved); err != nil {
			return application.DecisionModel{}, application.DecisionDraft{}, false, err
		}
		return saved.Model, saved.Draft, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	id := uuid.New()
	model.ID, model.TenantID = id.String(), tenant.String()
	draft.DecisionModelID, draft.Revision = model.ID, 1
	draft.UpdatedAt = model.CreatedAt
	_, err = tx.Exec(ctx, `INSERT INTO decision_models(tenant_id,id,model_key,name,description,status,revision,row_version,is_deleted,created_at,created_by,updated_at,updated_by) VALUES($1,$2,$3,$4,$5,'DRAFT',1,1,false,$6,$7,$6,$7)`, tenant, id, model.ModelKey, model.Name, model.Description, model.CreatedAt, actor)
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, mapConflict(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO decision_model_drafts(tenant_id,decision_model_id,revision,dmn_xml,validation_status,diagnostics,updated_at,updated_by) VALUES($1,$2,1,$3,'NOT_VALIDATED','[]'::jsonb,$4,$5)`, tenant, id, draft.DMNXML, draft.UpdatedAt, actor)
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, mapConflict(err)
	}
	raw, err := json.Marshal(struct {
		Model application.DecisionModel `json:"model"`
		Draft application.DecisionDraft `json:"draft"`
	}{model, draft})
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	sum := sha256.Sum256([]byte(draft.DMNXML))
	if err = insertDecisionAudit(ctx, tx, tenant, id, actor, command, correlationID, "DECISION_MODEL_CREATED", nil, sum[:], model.CreatedAt, map[string]string{"model_key": model.ModelKey}); err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'create_decision_model',$4,$5,$6)`, tenant, idempotencyKey, command, digest, raw, model.CreatedAt)
	if err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.DecisionModel{}, application.DecisionDraft{}, false, err
	}
	return model, draft, false, nil
}

func (s *Store) SaveDecisionDraft(ctx context.Context, tenantID string, draft application.DecisionDraft, expected int64, actorID, commandID, key, correlation string, digest []byte) (application.DecisionDraft, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || expected < 1 {
		return application.DecisionDraft{}, false, application.ErrInvalidArgument
	}
	model, err := uuid.Parse(draft.DecisionModelID)
	if err != nil {
		return application.DecisionDraft{}, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.DecisionDraft{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return application.DecisionDraft{}, false, err
	}
	var prev, raw []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&prev, &raw)
	if err == nil {
		if subtle.ConstantTimeCompare(prev, digest) != 1 {
			return application.DecisionDraft{}, false, application.ErrIdempotencyConflict
		}
		var saved struct {
			Draft application.DecisionDraft `json:"draft"`
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return application.DecisionDraft{}, false, err
		}
		return saved.Draft, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionDraft{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.DecisionDraft{}, false, err
	}
	var revision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM decision_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, model).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionDraft{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.DecisionDraft{}, false, err
	}
	if revision != expected {
		return application.DecisionDraft{}, false, application.ErrConflict
	}
	var before string
	err = tx.QueryRow(ctx, `SELECT dmn_xml FROM decision_model_drafts WHERE tenant_id=$1 AND decision_model_id=$2 AND revision=$3 FOR UPDATE`, tenant, model, expected).Scan(&before)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionDraft{}, false, application.ErrConflict
	}
	if err != nil {
		return application.DecisionDraft{}, false, err
	}
	draft.Revision = expected + 1
	result, err := tx.Exec(ctx, `UPDATE decision_model_drafts SET revision=$3,dmn_xml=$4,validation_status='STALE',diagnostics='[]'::jsonb,updated_at=$5,updated_by=$6 WHERE tenant_id=$1 AND decision_model_id=$2 AND revision=$7`, tenant, model, draft.Revision, draft.DMNXML, draft.UpdatedAt, actor, expected)
	if err != nil {
		return application.DecisionDraft{}, false, mapConflict(err)
	}
	if result.RowsAffected() != 1 {
		return application.DecisionDraft{}, false, application.ErrConflict
	}
	result, err = tx.Exec(ctx, `UPDATE decision_models SET revision=$3,row_version=row_version+1,updated_at=$4,updated_by=$5 WHERE tenant_id=$1 AND id=$2 AND revision=$6 AND NOT is_deleted`, tenant, model, draft.Revision, draft.UpdatedAt, actor, expected)
	if err != nil {
		return application.DecisionDraft{}, false, err
	}
	if result.RowsAffected() != 1 {
		return application.DecisionDraft{}, false, application.ErrConflict
	}
	beforeHash, afterHash := sha256.Sum256([]byte(before)), sha256.Sum256([]byte(draft.DMNXML))
	if err = insertDecisionAudit(ctx, tx, tenant, model, actor, command, correlation, "DECISION_MODEL_DRAFT_SAVED", beforeHash[:], afterHash[:], draft.UpdatedAt, map[string]int64{"from_revision": expected, "to_revision": draft.Revision}); err != nil {
		return application.DecisionDraft{}, false, err
	}
	raw, err = json.Marshal(map[string]any{"draft": draft})
	if err != nil {
		return application.DecisionDraft{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'save_decision_model_draft',$4,$5,$6)`, tenant, key, command, digest, raw, draft.UpdatedAt)
	if err != nil {
		return application.DecisionDraft{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.DecisionDraft{}, false, err
	}
	return draft, false, nil
}

func (s *Store) SaveDecisionValidation(ctx context.Context, tenantID, modelID string, expected int64, validation string, diagnostics []application.Diagnostic, actorID, commandID, key, correlation string, digest []byte, at time.Time) error {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || expected < 1 {
		return application.ErrInvalidArgument
	}
	id, err := uuid.Parse(modelID)
	if err != nil {
		return application.ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return err
	}
	var prev []byte
	err = tx.QueryRow(ctx, `SELECT request_digest FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&prev)
	if err == nil {
		if subtle.ConstantTimeCompare(prev, digest) != 1 {
			return application.ErrIdempotencyConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return err
	}
	var rev int64
	err = tx.QueryRow(ctx, `SELECT revision FROM decision_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, id).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	if err != nil {
		return err
	}
	if rev != expected {
		return application.ErrConflict
	}
	rawDiag, err := json.Marshal(diagnostics)
	if err != nil {
		return err
	}
	res, err := tx.Exec(ctx, `UPDATE decision_model_drafts SET validation_status=$4,diagnostics=$5,updated_at=$6,updated_by=$7 WHERE tenant_id=$1 AND decision_model_id=$2 AND revision=$3`, tenant, id, expected, validation, rawDiag, at, actor)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return application.ErrConflict
	}
	if err = insertDecisionAudit(ctx, tx, tenant, id, actor, command, correlation, "DECISION_MODEL_DRAFT_VALIDATED", nil, nil, at, map[string]any{"revision": expected, "validation_status": validation, "diagnostics": diagnostics}); err != nil {
		return err
	}
	res, err = tx.Exec(ctx, `UPDATE decision_models SET row_version=row_version+1,updated_at=$3,updated_by=$4 WHERE tenant_id=$1 AND id=$2 AND revision=$5`, tenant, id, at, actor, expected)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return application.ErrConflict
	}
	response, _ := json.Marshal(map[string]any{"decision_model_id": modelID, "revision": expected, "valid": validation == "VALID", "diagnostics": diagnostics})
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'validate_decision_model_draft',$4,$5,$6)`, tenant, key, command, digest, response, at)
	if err != nil {
		return mapConflict(err)
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateModel(ctx context.Context, model application.Model, draft application.Draft, actorID, commandID, idempotencyKey, correlationID string, digest []byte) (application.Model, application.Draft, bool, error) {
	tenant, actor, command, err := parseIDs(model.TenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size {
		return application.Model{}, application.Draft{}, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.Model{}, application.Draft{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, idempotencyKey); err != nil {
		return application.Model{}, application.Draft{}, false, err
	}
	var storedDigest, storedResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey).Scan(&storedDigest, &storedResponse)
	if err == nil {
		if subtle.ConstantTimeCompare(storedDigest, digest) != 1 {
			return application.Model{}, application.Draft{}, false, application.ErrIdempotencyConflict
		}
		var stored createResponse
		if err = json.Unmarshal(storedResponse, &stored); err != nil {
			return application.Model{}, application.Draft{}, false, err
		}
		return stored.Model, stored.Draft, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.Model{}, application.Draft{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.Model{}, application.Draft{}, false, err
	}
	modelID := uuid.New()
	model.ID, model.TenantID = modelID.String(), tenant.String()
	draft.ProcessModelID, draft.Revision = model.ID, 1
	draft.UpdatedAt = model.CreatedAt
	_, err = tx.Exec(ctx, `INSERT INTO process_models
		(tenant_id,id,model_key,name,category,description,status,revision,row_version,is_deleted,created_at,created_by,updated_at,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,'DRAFT',1,1,false,$7,$8,$7,$8)`, tenant, modelID, model.ModelKey, model.Name, model.Category, model.Description, model.CreatedAt, actor)
	if err != nil {
		return application.Model{}, application.Draft{}, false, mapConflict(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO process_model_drafts
		(tenant_id,process_model_id,revision,base_version_id,bpmn_xml,validation_status,diagnostics,updated_at,updated_by)
		VALUES($1,$2,1,NULLIF($3,'')::uuid,$4,'NOT_VALIDATED','[]'::jsonb,$5,$6)`, tenant, modelID, draft.BaseVersionID, draft.BPMNXML, draft.UpdatedAt, actor)
	if err != nil {
		return application.Model{}, application.Draft{}, false, mapConflict(err)
	}
	response := createResponse{Model: model, Draft: draft}
	rawResponse, err := json.Marshal(response)
	if err != nil {
		return application.Model{}, application.Draft{}, false, err
	}
	contentDigest := sha256.Sum256([]byte(draft.BPMNXML))
	if err = insertAudit(ctx, tx, tenant, modelID, actor, command, correlationID, "PROCESS_MODEL_CREATED", nil, contentDigest[:], model.CreatedAt, map[string]string{"model_key": model.ModelKey}); err != nil {
		return application.Model{}, application.Draft{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at)
		VALUES($1,$2,$3,'create_process_model',$4,$5,$6)`, tenant, idempotencyKey, command, digest, rawResponse, model.CreatedAt)
	if err != nil {
		return application.Model{}, application.Draft{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.Model{}, application.Draft{}, false, err
	}
	return model, draft, false, nil
}

func (s *Store) SaveDraft(ctx context.Context, tenantID string, draft application.Draft, expectedRevision int64, actorID, commandID, idempotencyKey, correlationID string, digest []byte) (application.Draft, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || expectedRevision < 1 {
		return application.Draft{}, false, application.ErrInvalidArgument
	}
	modelID, err := uuid.Parse(draft.ProcessModelID)
	if err != nil {
		return application.Draft{}, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.Draft{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, idempotencyKey); err != nil {
		return application.Draft{}, false, err
	}
	var storedDigest, storedResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey).Scan(&storedDigest, &storedResponse)
	if err == nil {
		if subtle.ConstantTimeCompare(storedDigest, digest) != 1 {
			return application.Draft{}, false, application.ErrIdempotencyConflict
		}
		var stored struct {
			Draft application.Draft `json:"draft"`
		}
		if err = json.Unmarshal(storedResponse, &stored); err != nil {
			return application.Draft{}, false, err
		}
		return stored.Draft, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.Draft{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.Draft{}, false, err
	}
	var currentRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM process_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, modelID).Scan(&currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Draft{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.Draft{}, false, err
	}
	if currentRevision != expectedRevision {
		return application.Draft{}, false, application.ErrConflict
	}
	if draft.BaseVersionID != "" {
		baseVersionID, parseErr := uuid.Parse(draft.BaseVersionID)
		if parseErr != nil {
			return application.Draft{}, false, application.ErrInvalidArgument
		}
		var baseSource string
		err = tx.QueryRow(ctx, `SELECT bpmn_xml FROM process_model_versions WHERE tenant_id=$1 AND process_model_id=$2 AND id=$3`, tenant, modelID, baseVersionID).Scan(&baseSource)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.Draft{}, false, application.ErrNotFound
		}
		if err != nil {
			return application.Draft{}, false, err
		}
		if baseSource != draft.BPMNXML {
			return application.Draft{}, false, application.ErrConflict
		}
	}
	var beforeXML string
	err = tx.QueryRow(ctx, `SELECT bpmn_xml FROM process_model_drafts WHERE tenant_id=$1 AND process_model_id=$2 AND revision=$3 FOR UPDATE`, tenant, modelID, expectedRevision).Scan(&beforeXML)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Draft{}, false, application.ErrConflict
	}
	if err != nil {
		return application.Draft{}, false, err
	}
	draft.Revision = expectedRevision + 1
	result, err := tx.Exec(ctx, `UPDATE process_model_drafts SET revision=$4,base_version_id=NULLIF($5,'')::uuid,bpmn_xml=$6,
		validation_status='STALE',diagnostics='[]'::jsonb,updated_at=$7,updated_by=$8
		WHERE tenant_id=$1 AND process_model_id=$2 AND revision=$3`, tenant, modelID, expectedRevision, draft.Revision, draft.BaseVersionID, draft.BPMNXML, draft.UpdatedAt, actor)
	if err != nil {
		return application.Draft{}, false, mapConflict(err)
	}
	if result.RowsAffected() != 1 {
		return application.Draft{}, false, application.ErrConflict
	}
	result, err = tx.Exec(ctx, `UPDATE process_models SET revision=$3,row_version=row_version+1,updated_at=$4,updated_by=$5
		WHERE tenant_id=$1 AND id=$2 AND revision=$6 AND NOT is_deleted`, tenant, modelID, draft.Revision, draft.UpdatedAt, actor, expectedRevision)
	if err != nil {
		return application.Draft{}, false, err
	}
	if result.RowsAffected() != 1 {
		return application.Draft{}, false, application.ErrConflict
	}
	beforeDigest, afterDigest := sha256.Sum256([]byte(beforeXML)), sha256.Sum256([]byte(draft.BPMNXML))
	if err = insertAudit(ctx, tx, tenant, modelID, actor, command, correlationID, "PROCESS_MODEL_DRAFT_SAVED", beforeDigest[:], afterDigest[:], draft.UpdatedAt, map[string]any{"from_revision": expectedRevision, "to_revision": draft.Revision, "base_version_id": draft.BaseVersionID}); err != nil {
		return application.Draft{}, false, err
	}
	response, err := json.Marshal(struct {
		Draft application.Draft `json:"draft"`
	}{draft})
	if err != nil {
		return application.Draft{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at)
		VALUES($1,$2,$3,'save_process_model_draft',$4,$5,$6)`, tenant, idempotencyKey, command, digest, response, draft.UpdatedAt)
	if err != nil {
		return application.Draft{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.Draft{}, false, err
	}
	return draft, false, nil
}

func (s *Store) SaveValidation(ctx context.Context, tenantID, modelID string, expectedRevision int64, validation string, diagnostics []application.Diagnostic, actorID, commandID, idempotencyKey, correlationID string, digest []byte, at time.Time) error {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || expectedRevision < 1 {
		return application.ErrInvalidArgument
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return application.ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, idempotencyKey); err != nil {
		return err
	}
	var storedDigest []byte
	err = tx.QueryRow(ctx, `SELECT request_digest FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey).Scan(&storedDigest)
	if err == nil {
		if subtle.ConstantTimeCompare(storedDigest, digest) != 1 {
			return application.ErrIdempotencyConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return err
	}
	var modelRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM process_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, model).Scan(&modelRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	if err != nil {
		return err
	}
	if modelRevision != expectedRevision {
		return application.ErrConflict
	}
	var currentRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM process_model_drafts WHERE tenant_id=$1 AND process_model_id=$2 FOR UPDATE`, tenant, model).Scan(&currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	if err != nil {
		return err
	}
	if currentRevision != expectedRevision {
		return application.ErrConflict
	}
	rawDiagnostics, err := json.Marshal(diagnostics)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE process_model_drafts SET validation_status=$4,diagnostics=$5,updated_at=$6,updated_by=$7
		WHERE tenant_id=$1 AND process_model_id=$2 AND revision=$3`, tenant, model, expectedRevision, validation, rawDiagnostics, at, actor)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrConflict
	}
	if err = insertAudit(ctx, tx, tenant, model, actor, command, correlationID, "PROCESS_MODEL_DRAFT_VALIDATED", nil, nil, at, map[string]any{"revision": expectedRevision, "validation_status": validation, "diagnostics": diagnostics}); err != nil {
		return err
	}
	modelUpdate, err := tx.Exec(ctx, `UPDATE process_models SET row_version=row_version+1,updated_at=$3,updated_by=$4 WHERE tenant_id=$1 AND id=$2 AND revision=$5 AND NOT is_deleted`, tenant, model, at, actor, expectedRevision)
	if err != nil {
		return err
	}
	if modelUpdate.RowsAffected() != 1 {
		return application.ErrConflict
	}
	response, err := json.Marshal(map[string]any{"process_model_id": modelID, "revision": expectedRevision, "validation_status": validation, "diagnostics": diagnostics})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at)
		VALUES($1,$2,$3,'validate_process_model_draft',$4,$5,$6)`, tenant, idempotencyKey, command, digest, response, at)
	if err != nil {
		return mapConflict(err)
	}
	return tx.Commit(ctx)
}

func (s *Store) CheckinProcessModel(ctx context.Context, tenantID, modelID string, expectedRevision int64, revisionDigest []byte, versionTag, summary, actorID, commandID, idempotencyKey, correlationID string, digest []byte, at time.Time) (application.ProcessModelVersion, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || len(revisionDigest) != sha256.Size || expectedRevision < 1 || strings.TrimSpace(versionTag) == "" || len(versionTag) > 128 || strings.TrimSpace(summary) == "" || len(summary) > 1000 {
		return application.ProcessModelVersion{}, false, application.ErrInvalidArgument
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return application.ProcessModelVersion{}, false, application.ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, idempotencyKey); err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	var storedDigest, storedResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey).Scan(&storedDigest, &storedResponse)
	if err == nil {
		if subtle.ConstantTimeCompare(storedDigest, digest) != 1 {
			return application.ProcessModelVersion{}, false, application.ErrIdempotencyConflict
		}
		var stored struct {
			Version application.ProcessModelVersion `json:"version"`
		}
		if err = json.Unmarshal(storedResponse, &stored); err != nil {
			return application.ProcessModelVersion{}, false, err
		}
		return stored.Version, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.ProcessModelVersion{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	var modelRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM process_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, model).Scan(&modelRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ProcessModelVersion{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	if modelRevision != expectedRevision {
		return application.ProcessModelVersion{}, false, application.ErrConflict
	}
	var draftRevision int64
	var source, validation string
	err = tx.QueryRow(ctx, `SELECT revision,bpmn_xml,validation_status FROM process_model_drafts WHERE tenant_id=$1 AND process_model_id=$2 FOR UPDATE`, tenant, model).Scan(&draftRevision, &source, &validation)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ProcessModelVersion{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	if draftRevision != expectedRevision {
		return application.ProcessModelVersion{}, false, application.ErrConflict
	}
	if validation != "VALID" {
		return application.ProcessModelVersion{}, false, application.ErrDraftNotValidated
	}
	actualHash := sha256.Sum256([]byte(source))
	if subtle.ConstantTimeCompare(actualHash[:], revisionDigest) != 1 {
		return application.ProcessModelVersion{}, false, application.ErrConflict
	}
	var versionNum int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_num),0)+1 FROM process_model_versions WHERE tenant_id=$1 AND process_model_id=$2`, tenant, model).Scan(&versionNum); err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	versionID := uuid.New()
	version := application.ProcessModelVersion{ID: versionID.String(), TenantID: tenant.String(), ProcessModelID: model.String(), VersionNum: versionNum, SourceRevision: expectedRevision, BPMNXML: source, ContentHash: actualHash[:], CreatedBy: actor.String(), CreatedAt: at, VersionTag: versionTag, Summary: summary}
	if _, err = tx.Exec(ctx, `INSERT INTO process_model_versions(tenant_id,id,process_model_id,version_num,source_revision,bpmn_xml,content_hash,created_by,created_at,version_tag,summary) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, versionID, model, versionNum, expectedRevision, source, actualHash[:], actor, at, versionTag, summary); err != nil {
		return application.ProcessModelVersion{}, false, mapConflict(err)
	}
	updated, err := tx.Exec(ctx, `UPDATE process_models SET latest_version_id=$3,row_version=row_version+1,updated_at=$4,updated_by=$5 WHERE tenant_id=$1 AND id=$2 AND revision=$6 AND NOT is_deleted`, tenant, model, versionID, at, actor, expectedRevision)
	if err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	if updated.RowsAffected() != 1 {
		return application.ProcessModelVersion{}, false, application.ErrConflict
	}
	if err = insertAudit(ctx, tx, tenant, model, actor, command, correlationID, "PROCESS_MODEL_VERSION_CHECKED_IN", nil, actualHash[:], at, map[string]any{"version_id": version.ID, "version_num": versionNum, "source_revision": expectedRevision, "version_tag": versionTag, "summary": summary}); err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	response, err := json.Marshal(struct {
		Version application.ProcessModelVersion `json:"version"`
	}{version})
	if err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'checkin_process_model',$4,$5,$6)`, tenant, idempotencyKey, command, digest, response, at); err != nil {
		return application.ProcessModelVersion{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.ProcessModelVersion{}, false, err
	}
	return version, false, nil
}

func (s *Store) ListProcessModelVersions(ctx context.Context, tenantID, modelID, pageToken string, limit int) ([]application.ModelVersionSummary, string, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, "", application.ErrInvalidArgument
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return nil, "", application.ErrNotFound
	}
	cursor, err := decodeVersionCursor(pageToken, tenantID, modelID)
	if err != nil {
		return nil, "", application.ErrInvalidArgument
	}
	var cursorNum any
	if cursor != nil {
		cursorNum = cursor.VersionNum
	}
	rows, err := s.pool.Query(ctx, `SELECT v.id,v.tenant_id,v.process_model_id,v.version_num,v.source_revision,v.created_by,v.created_at,v.version_tag,v.summary FROM process_model_versions v JOIN process_models m ON m.tenant_id=v.tenant_id AND m.id=v.process_model_id WHERE v.tenant_id=$1 AND v.process_model_id=$2 AND NOT m.is_deleted AND ($3::bigint IS NULL OR v.version_num<$3) ORDER BY v.version_num DESC LIMIT $4`, tenant, model, cursorNum, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]application.ModelVersionSummary, 0, limit+1)
	for rows.Next() {
		var item application.ModelVersionSummary
		var id, tenant, model, actor uuid.UUID
		if err = rows.Scan(&id, &tenant, &model, &item.VersionNum, &item.SourceRevision, &actor, &item.CreatedAt, &item.VersionTag, &item.Summary); err != nil {
			return nil, "", err
		}
		item.ID, item.TenantID, item.ModelID, item.CreatedBy = id.String(), tenant.String(), model.String(), actor.String()
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) <= limit {
		return items, "", nil
	}
	items = items[:limit]
	next, err := encodeVersionCursor(versionPageCursor{TenantID: tenantID, ModelID: modelID, VersionNum: items[len(items)-1].VersionNum})
	return items, next, err
}

func (s *Store) GetProcessModelVersion(ctx context.Context, tenantID, modelID, versionID string) (application.ProcessModelVersion, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.ProcessModelVersion{}, application.ErrNotFound
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return application.ProcessModelVersion{}, application.ErrNotFound
	}
	versionIDParsed, err := uuid.Parse(versionID)
	if err != nil {
		return application.ProcessModelVersion{}, application.ErrNotFound
	}
	var v application.ProcessModelVersion
	var id, tenantResult, modelResult, actor uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT v.id,v.tenant_id,v.process_model_id,v.version_num,v.source_revision,v.bpmn_xml,v.content_hash,v.created_by,v.created_at,v.version_tag,v.summary FROM process_model_versions v JOIN process_models m ON m.tenant_id=v.tenant_id AND m.id=v.process_model_id WHERE v.tenant_id=$1 AND v.process_model_id=$2 AND v.id=$3 AND NOT m.is_deleted`, tenant, model, versionIDParsed).Scan(&id, &tenantResult, &modelResult, &v.VersionNum, &v.SourceRevision, &v.BPMNXML, &v.ContentHash, &actor, &v.CreatedAt, &v.VersionTag, &v.Summary)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ProcessModelVersion{}, application.ErrNotFound
	}
	if err != nil {
		return application.ProcessModelVersion{}, err
	}
	v.ID, v.TenantID, v.ProcessModelID, v.CreatedBy = id.String(), tenantResult.String(), modelResult.String(), actor.String()
	return v, nil
}

func (s *Store) CheckinDecisionModel(ctx context.Context, tenantID, modelID string, expectedRevision int64, revisionDigest []byte, versionTag, summary, actorID, commandID, idempotencyKey, correlationID string, digest []byte, at time.Time) (application.DecisionModelVersion, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || len(revisionDigest) != sha256.Size || expectedRevision < 1 || strings.TrimSpace(versionTag) == "" || len(versionTag) > 128 || strings.TrimSpace(summary) == "" || len(summary) > 1000 {
		return application.DecisionModelVersion{}, false, application.ErrInvalidArgument
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return application.DecisionModelVersion{}, false, application.ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, idempotencyKey); err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	var storedDigest, storedResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey).Scan(&storedDigest, &storedResponse)
	if err == nil {
		if subtle.ConstantTimeCompare(storedDigest, digest) != 1 {
			return application.DecisionModelVersion{}, false, application.ErrIdempotencyConflict
		}
		var stored struct {
			Version application.DecisionModelVersion `json:"version"`
		}
		if err = json.Unmarshal(storedResponse, &stored); err != nil {
			return application.DecisionModelVersion{}, false, err
		}
		return stored.Version, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModelVersion{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	var modelRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM decision_models WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, model).Scan(&modelRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModelVersion{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	if modelRevision != expectedRevision {
		return application.DecisionModelVersion{}, false, application.ErrConflict
	}
	var draftRevision int64
	var source, validation string
	err = tx.QueryRow(ctx, `SELECT revision,dmn_xml,validation_status FROM decision_model_drafts WHERE tenant_id=$1 AND decision_model_id=$2 FOR UPDATE`, tenant, model).Scan(&draftRevision, &source, &validation)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModelVersion{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	if draftRevision != expectedRevision {
		return application.DecisionModelVersion{}, false, application.ErrConflict
	}
	if validation != "VALID" {
		return application.DecisionModelVersion{}, false, application.ErrDraftNotValidated
	}
	actualHash := sha256.Sum256([]byte(source))
	if subtle.ConstantTimeCompare(actualHash[:], revisionDigest) != 1 {
		return application.DecisionModelVersion{}, false, application.ErrConflict
	}
	var versionNum int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_num),0)+1 FROM decision_model_versions WHERE tenant_id=$1 AND decision_model_id=$2`, tenant, model).Scan(&versionNum); err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	versionID := uuid.New()
	version := application.DecisionModelVersion{ID: versionID.String(), TenantID: tenant.String(), DecisionModelID: model.String(), VersionNum: versionNum, SourceRevision: expectedRevision, DMNXML: source, ContentHash: actualHash[:], CreatedBy: actor.String(), CreatedAt: at, VersionTag: versionTag, Summary: summary}
	if _, err = tx.Exec(ctx, `INSERT INTO decision_model_versions(tenant_id,id,decision_model_id,version_num,source_revision,dmn_xml,content_hash,created_by,created_at,version_tag,summary) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, versionID, model, versionNum, expectedRevision, source, actualHash[:], actor, at, versionTag, summary); err != nil {
		return application.DecisionModelVersion{}, false, mapConflict(err)
	}
	updated, err := tx.Exec(ctx, `UPDATE decision_models SET latest_version_id=$3,row_version=row_version+1,updated_at=$4,updated_by=$5 WHERE tenant_id=$1 AND id=$2 AND revision=$6 AND NOT is_deleted`, tenant, model, versionID, at, actor, expectedRevision)
	if err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	if updated.RowsAffected() != 1 {
		return application.DecisionModelVersion{}, false, application.ErrConflict
	}
	if correlationID == "" || len(correlationID) > 256 {
		return application.DecisionModelVersion{}, false, application.ErrInvalidArgument
	}
	auditDetails, err := json.Marshal(map[string]any{"version_id": version.ID, "version_num": versionNum, "source_revision": expectedRevision, "version_tag": versionTag, "summary": summary})
	if err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_decision_audit_evidence(tenant_id,id,decision_model_id,actor_id,action,command_id,correlation_id,before_digest,after_digest,details,occurred_at) VALUES($1,$2,$3,$4,'DECISION_MODEL_VERSION_CHECKED_IN',$5,$6,NULL,$7,$8,$9)`, tenant, uuid.New(), model, actor, command, correlationID, actualHash[:], auditDetails, at); err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	response, err := json.Marshal(struct {
		Version application.DecisionModelVersion `json:"version"`
	}{version})
	if err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'checkin_decision_model',$4,$5,$6)`, tenant, idempotencyKey, command, digest, response, at); err != nil {
		return application.DecisionModelVersion{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.DecisionModelVersion{}, false, err
	}
	return version, false, nil
}

func (s *Store) ListDecisionModelVersions(ctx context.Context, tenantID, modelID, pageToken string, limit int) ([]application.ModelVersionSummary, string, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, "", application.ErrInvalidArgument
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return nil, "", application.ErrNotFound
	}
	cursor, err := decodeVersionCursor(pageToken, tenantID, modelID)
	if err != nil {
		return nil, "", application.ErrInvalidArgument
	}
	var cursorNum any
	if cursor != nil {
		cursorNum = cursor.VersionNum
	}
	rows, err := s.pool.Query(ctx, `SELECT v.id,v.tenant_id,v.decision_model_id,v.version_num,v.source_revision,v.created_by,v.created_at,v.version_tag,v.summary FROM decision_model_versions v JOIN decision_models m ON m.tenant_id=v.tenant_id AND m.id=v.decision_model_id WHERE v.tenant_id=$1 AND v.decision_model_id=$2 AND NOT m.is_deleted AND ($3::bigint IS NULL OR v.version_num<$3) ORDER BY v.version_num DESC LIMIT $4`, tenant, model, cursorNum, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]application.ModelVersionSummary, 0, limit+1)
	for rows.Next() {
		var item application.ModelVersionSummary
		var id, tenantResult, modelResult, actor uuid.UUID
		if err = rows.Scan(&id, &tenantResult, &modelResult, &item.VersionNum, &item.SourceRevision, &actor, &item.CreatedAt, &item.VersionTag, &item.Summary); err != nil {
			return nil, "", err
		}
		item.ID, item.TenantID, item.ModelID, item.CreatedBy = id.String(), tenantResult.String(), modelResult.String(), actor.String()
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) <= limit {
		return items, "", nil
	}
	items = items[:limit]
	next, err := encodeVersionCursor(versionPageCursor{TenantID: tenantID, ModelID: modelID, VersionNum: items[len(items)-1].VersionNum})
	return items, next, err
}

func (s *Store) GetDecisionModelVersion(ctx context.Context, tenantID, modelID, versionID string) (application.DecisionModelVersion, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.DecisionModelVersion{}, application.ErrNotFound
	}
	model, err := uuid.Parse(modelID)
	if err != nil {
		return application.DecisionModelVersion{}, application.ErrNotFound
	}
	versionIDParsed, err := uuid.Parse(versionID)
	if err != nil {
		return application.DecisionModelVersion{}, application.ErrNotFound
	}
	var v application.DecisionModelVersion
	var id, tenantResult, modelResult, actor uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT v.id,v.tenant_id,v.decision_model_id,v.version_num,v.source_revision,v.dmn_xml,v.content_hash,v.created_by,v.created_at,v.version_tag,v.summary FROM decision_model_versions v JOIN decision_models m ON m.tenant_id=v.tenant_id AND m.id=v.decision_model_id WHERE v.tenant_id=$1 AND v.decision_model_id=$2 AND v.id=$3 AND NOT m.is_deleted`, tenant, model, versionIDParsed).Scan(&id, &tenantResult, &modelResult, &v.VersionNum, &v.SourceRevision, &v.DMNXML, &v.ContentHash, &actor, &v.CreatedAt, &v.VersionTag, &v.Summary)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.DecisionModelVersion{}, application.ErrNotFound
	}
	if err != nil {
		return application.DecisionModelVersion{}, err
	}
	v.ID, v.TenantID, v.DecisionModelID, v.CreatedBy = id.String(), tenantResult.String(), modelResult.String(), actor.String()
	return v, nil
}

type createResponse struct {
	Model application.Model `json:"model"`
	Draft application.Draft `json:"draft"`
}
type pageCursor struct {
	ID string `json:"id"`
}

type versionPageCursor struct {
	TenantID   string `json:"tenant_id"`
	ModelID    string `json:"model_id"`
	VersionNum int64  `json:"version_num"`
}

type rowScanner interface{ Scan(...any) error }

func scanDecisionModel(row rowScanner) (application.DecisionModel, error) {
	var m application.DecisionModel
	var id, tenant uuid.UUID
	err := row.Scan(&id, &tenant, &m.ModelKey, &m.Name, &m.Description, &m.Status, &m.Revision, &m.LatestVersionSourceRevision, &m.CreatedAt, &m.UpdatedAt)
	m.ID, m.TenantID = id.String(), tenant.String()
	return m, err
}

func scanDecisionDraft(row rowScanner) (application.DecisionDraft, error) {
	var d application.DecisionDraft
	var id uuid.UUID
	var diagnostics []byte
	err := row.Scan(&id, &d.Revision, &d.DMNXML, &d.Validation, &diagnostics, &d.UpdatedAt)
	d.DecisionModelID = id.String()
	if err != nil {
		return application.DecisionDraft{}, err
	}
	if err = json.Unmarshal(diagnostics, &d.Diagnostics); err != nil {
		return application.DecisionDraft{}, err
	}
	return d, nil
}

func scanModel(row rowScanner) (application.Model, error) {
	var model application.Model
	var id, tenant uuid.UUID
	err := row.Scan(&id, &tenant, &model.ModelKey, &model.Name, &model.Category, &model.Description, &model.Status, &model.Revision, &model.LatestVersionID, &model.LatestVersionSourceRevision, &model.CreatedAt, &model.UpdatedAt, &model.IsDeleted)
	model.ID, model.TenantID = id.String(), tenant.String()
	return model, err
}

func scanDraft(row rowScanner) (application.Draft, error) {
	var draft application.Draft
	var modelID uuid.UUID
	var rawDiagnostics []byte
	err := row.Scan(&modelID, &draft.Revision, &draft.BaseVersionID, &draft.BPMNXML, &draft.Validation, &rawDiagnostics, &draft.UpdatedAt)
	draft.ProcessModelID = modelID.String()
	if err != nil {
		return application.Draft{}, err
	}
	if err = json.Unmarshal(rawDiagnostics, &draft.Diagnostics); err != nil {
		return application.Draft{}, err
	}
	return draft, nil
}

func parseIDs(tenantID, actorID, commandID string) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, application.ErrInvalidArgument
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, application.ErrUnauthenticated
	}
	command, err := uuid.Parse(commandID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, application.ErrInvalidArgument
	}
	return tenant, actor, command, nil
}

func lockIdempotency(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, key string) error {
	if key == "" || len(key) > 256 {
		return application.ErrInvalidArgument
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenant.String()+"\x00"+key)
	return err
}

func rejectCommandReuse(ctx context.Context, tx pgx.Tx, tenant, command uuid.UUID) error {
	var existing string
	err := tx.QueryRow(ctx, `SELECT idempotency_key FROM authoring_command_dedup WHERE tenant_id=$1 AND command_id=$2`, tenant, command).Scan(&existing)
	if err == nil {
		return application.ErrIdempotencyConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func insertAudit(ctx context.Context, tx pgx.Tx, tenant, modelID, actor, command uuid.UUID, correlationID, action string, before, after []byte, at time.Time, details any) error {
	if correlationID == "" || len(correlationID) > 256 {
		return application.ErrInvalidArgument
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_audit_evidence(tenant_id,id,process_model_id,actor_id,action,command_id,correlation_id,before_digest,after_digest,details,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, uuid.New(), modelID, actor, action, command, correlationID, before, after, raw, at)
	return err
}

func insertDecisionAudit(ctx context.Context, tx pgx.Tx, tenant, modelID, actor, command uuid.UUID, correlationID, action string, before, after []byte, at time.Time, details any) error {
	if correlationID == "" || len(correlationID) > 256 {
		return application.ErrInvalidArgument
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_decision_audit_evidence(tenant_id,id,decision_model_id,actor_id,action,command_id,correlation_id,before_digest,after_digest,details,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, uuid.New(), modelID, actor, action, command, correlationID, before, after, raw, at)
	return err
}

func mapConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return application.ErrConflict
		case "23503":
			return application.ErrConflict
		case "23514":
			return application.ErrInvalidArgument
		case "40001", "40P01":
			return application.ErrConflict
		}
	}
	return err
}

func encodeCursor(cursor pageCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func encodeVersionCursor(cursor versionPageCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeVersionCursor(value, tenantID, modelID string) (*versionPageCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 4096 {
		return nil, application.ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, application.ErrInvalidArgument
	}
	var cursor versionPageCursor
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.VersionNum < 1 || cursor.TenantID != tenantID || cursor.ModelID != modelID {
		return nil, application.ErrInvalidArgument
	}
	return &cursor, nil
}
func decodeCursor(value string) (*pageCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 4096 {
		return nil, application.ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, application.ErrInvalidArgument
	}
	var cursor pageCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.ID == "" {
		return nil, application.ErrInvalidArgument
	}
	if _, err = uuid.Parse(cursor.ID); err != nil {
		return nil, application.ErrInvalidArgument
	}
	return &cursor, nil
}

var _ application.Repository = (*Store)(nil)
