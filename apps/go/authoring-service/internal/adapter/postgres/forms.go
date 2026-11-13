package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ListForms(ctx context.Context, tenantID, search, pageToken string, limit int) ([]application.FormDefinition, string, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, "", application.ErrInvalidArgument
	}
	cursor, err := decodeCursor(pageToken)
	if err != nil {
		return nil, "", err
	}
	var cursorID any
	if cursor != nil {
		cursorID = cursor.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,form_key,name,status,revision,COALESCE(latest_version_id::text,''),updated_at FROM form_definitions WHERE tenant_id=$1 AND NOT is_deleted AND ($2='' OR lower(name) LIKE '%'||lower($2)||'%' OR lower(form_key) LIKE '%'||lower($2)||'%') AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, tenant, search, cursorID, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]application.FormDefinition, 0, limit+1)
	for rows.Next() {
		var f application.FormDefinition
		var id, tid uuid.UUID
		if err = rows.Scan(&id, &tid, &f.FormKey, &f.Name, &f.Status, &f.Revision, &f.LatestVersionID, &f.UpdatedAt); err != nil {
			return nil, "", err
		}
		f.ID = id.String()
		f.TenantID = tid.String()
		items = append(items, f)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) <= limit {
		return items, "", nil
	}
	items = items[:limit]
	next, err := encodeCursor(pageCursor{ID: items[len(items)-1].ID})
	return items, next, err
}

func (s *Store) GetForm(ctx context.Context, tenantID, formID string) (application.FormDefinition, application.FormDraft, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.FormDefinition{}, application.FormDraft{}, application.ErrNotFound
	}
	id, err := uuid.Parse(formID)
	if err != nil {
		return application.FormDefinition{}, application.FormDraft{}, application.ErrNotFound
	}
	var f application.FormDefinition
	var fid, tid uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT id,tenant_id,form_key,name,status,revision,COALESCE(latest_version_id::text,''),updated_at FROM form_definitions WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted`, tenant, id).Scan(&fid, &tid, &f.FormKey, &f.Name, &f.Status, &f.Revision, &f.LatestVersionID, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, application.FormDraft{}, application.ErrNotFound
	}
	if err != nil {
		return f, application.FormDraft{}, err
	}
	f.ID = fid.String()
	f.TenantID = tid.String()
	var d application.FormDraft
	d.FormID = f.ID
	err = s.pool.QueryRow(ctx, `SELECT revision,schema_json::text,ui_schema_json::text,validation_status,diagnostics,updated_at FROM form_drafts WHERE tenant_id=$1 AND form_id=$2`, tenant, id).Scan(&d.Revision, &d.SchemaJSON, &d.UISchemaJSON, &d.Validation, &d.Diagnostics, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, d, application.ErrNotFound
	}
	return f, d, err
}

func (s *Store) GetFormByKey(ctx context.Context, tenantID, formKey string) (application.FormDefinition, application.FormVersion, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.FormDefinition{}, application.FormVersion{}, application.ErrNotFound
	}
	var f application.FormDefinition
	var v application.FormVersion
	var fid, tid, vid, created uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT f.id,f.tenant_id,f.form_key,f.name,f.status,f.revision,f.latest_version_id::text,f.updated_at,v.id,v.version_num,v.source_revision,v.schema_json::text,v.ui_schema_json::text,v.content_hash,v.created_by,v.created_at,v.version_tag,v.summary FROM form_definitions f JOIN form_versions v ON v.tenant_id=f.tenant_id AND v.id=f.latest_version_id WHERE f.tenant_id=$1 AND f.form_key=$2 AND f.status='PUBLISHED' AND NOT f.is_deleted`, tenant, formKey).Scan(&fid, &tid, &f.FormKey, &f.Name, &f.Status, &f.Revision, &f.LatestVersionID, &f.UpdatedAt, &vid, &v.VersionNum, &v.SourceRevision, &v.SchemaJSON, &v.UISchemaJSON, &v.ContentHash, &created, &v.CreatedAt, &v.VersionTag, &v.Summary)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, v, application.ErrNotFound
	}
	if err != nil {
		return f, v, err
	}
	f.ID = fid.String()
	f.TenantID = tid.String()
	v.ID = vid.String()
	v.TenantID = tid.String()
	v.FormID = f.ID
	v.CreatedBy = created.String()
	return f, v, nil
}

func (s *Store) CreateForm(ctx context.Context, form application.FormDefinition, draft application.FormDraft, actorID, commandID, key, correlation string, digest []byte) (application.FormDefinition, application.FormDraft, bool, error) {
	tenant, actor, command, err := parseIDs(form.TenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size {
		return form, draft, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return form, draft, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return form, draft, false, err
	}
	var prior, raw []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&prior, &raw)
	if err == nil {
		if subtle.ConstantTimeCompare(prior, digest) != 1 {
			return form, draft, false, application.ErrIdempotencyConflict
		}
		var saved struct {
			Form  application.FormDefinition `json:"form"`
			Draft application.FormDraft      `json:"draft"`
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return form, draft, false, err
		}
		return saved.Form, saved.Draft, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return form, draft, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return form, draft, false, err
	}
	id := uuid.New()
	form.ID = id.String()
	form.Revision = 1
	draft.FormID = form.ID
	draft.Revision = 1
	_, err = tx.Exec(ctx, `INSERT INTO form_definitions(tenant_id,id,form_key,name,status,revision,row_version,is_deleted,created_at,created_by,updated_at,updated_by) VALUES($1,$2,$3,$4,'DRAFT',1,1,false,$5,$6,$5,$6)`, tenant, id, form.FormKey, form.Name, form.UpdatedAt, actor)
	if err != nil {
		return form, draft, false, mapConflict(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO form_drafts(tenant_id,form_id,revision,schema_json,ui_schema_json,validation_status,diagnostics,updated_at,updated_by) VALUES($1,$2,1,$3::jsonb,$4::jsonb,'VALID','[]'::jsonb,$5,$6)`, tenant, id, draft.SchemaJSON, draft.UISchemaJSON, draft.UpdatedAt, actor)
	if err != nil {
		return form, draft, false, mapConflict(err)
	}
	encoded, _ := json.Marshal(struct {
		Form  application.FormDefinition `json:"form"`
		Draft application.FormDraft      `json:"draft"`
	}{form, draft})
	after := sha256.Sum256([]byte(draft.SchemaJSON + draft.UISchemaJSON))
	if err = insertFormAudit(ctx, tx, tenant, id, actor, command, correlation, "FORM_CREATED", nil, after[:], form.UpdatedAt, map[string]string{"form_key": form.FormKey}); err != nil {
		return form, draft, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'create_form',$4,$5,$6)`, tenant, key, command, digest, encoded, form.UpdatedAt); err != nil {
		return form, draft, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return form, draft, false, err
	}
	return form, draft, false, nil
}

func (s *Store) SaveFormDraft(ctx context.Context, tenantID string, draft application.FormDraft, name string, expected int64, actorID, commandID, key, correlation string, digest []byte) (application.FormDraft, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || expected < 1 {
		return draft, false, application.ErrInvalidArgument
	}
	formID, err := uuid.Parse(draft.FormID)
	if err != nil {
		return draft, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return draft, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return draft, false, err
	}
	var oldDigest, raw []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&oldDigest, &raw)
	if err == nil {
		if subtle.ConstantTimeCompare(oldDigest, digest) != 1 {
			return draft, false, application.ErrIdempotencyConflict
		}
		var saved struct {
			Draft application.FormDraft `json:"draft"`
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return draft, false, err
		}
		return saved.Draft, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return draft, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return draft, false, err
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT revision FROM form_definitions WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, formID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return draft, false, application.ErrNotFound
	}
	if err != nil {
		return draft, false, err
	}
	if current != expected {
		return draft, false, application.ErrConflict
	}
	var beforeSchema, beforeUI string
	err = tx.QueryRow(ctx, `SELECT schema_json::text,ui_schema_json::text FROM form_drafts WHERE tenant_id=$1 AND form_id=$2 FOR UPDATE`, tenant, formID).Scan(&beforeSchema, &beforeUI)
	if err != nil {
		return draft, false, err
	}
	draft.Revision = expected + 1
	draft.Validation = "STALE"
	draft.Diagnostics = nil
	_, err = tx.Exec(ctx, `UPDATE form_definitions SET name=$3,revision=$4,row_version=row_version+1,updated_at=$5,updated_by=$6 WHERE tenant_id=$1 AND id=$2`, tenant, formID, name, draft.Revision, draft.UpdatedAt, actor)
	if err != nil {
		return draft, false, mapConflict(err)
	}
	_, err = tx.Exec(ctx, `UPDATE form_drafts SET revision=$3,schema_json=$4::jsonb,ui_schema_json=$5::jsonb,validation_status='STALE',diagnostics='[]'::jsonb,updated_at=$6,updated_by=$7 WHERE tenant_id=$1 AND form_id=$2`, tenant, formID, draft.Revision, draft.SchemaJSON, draft.UISchemaJSON, draft.UpdatedAt, actor)
	if err != nil {
		return draft, false, mapConflict(err)
	}
	before := sha256.Sum256([]byte(beforeSchema + beforeUI))
	after := sha256.Sum256([]byte(draft.SchemaJSON + draft.UISchemaJSON))
	if err = insertFormAudit(ctx, tx, tenant, formID, actor, command, correlation, "FORM_DRAFT_SAVED", before[:], after[:], draft.UpdatedAt, map[string]int64{"from_revision": expected, "to_revision": draft.Revision}); err != nil {
		return draft, false, err
	}
	encoded, _ := json.Marshal(struct {
		Draft application.FormDraft `json:"draft"`
	}{draft})
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'save_form_draft',$4,$5,$6)`, tenant, key, command, digest, encoded, draft.UpdatedAt); err != nil {
		return draft, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return draft, false, err
	}
	return draft, false, nil
}

func (s *Store) SaveFormValidation(ctx context.Context, tenantID, formID string, revision int64, validation string, diagnostics []application.Diagnostic, actorID, commandID, key, correlation string, digest []byte, at time.Time) (bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size {
		return false, application.ErrInvalidArgument
	}
	form, err := uuid.Parse(formID)
	if err != nil {
		return false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return false, err
	}
	var previous, storedResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&previous, &storedResponse)
	if err == nil {
		if subtle.ConstantTimeCompare(previous, digest) != 1 {
			return false, application.ErrIdempotencyConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return false, err
	}
	var current int64
	var schemaJSON, uiSchemaJSON string
	err = tx.QueryRow(ctx, `SELECT revision,schema_json::text,ui_schema_json::text FROM form_drafts WHERE tenant_id=$1 AND form_id=$2 FOR UPDATE`, tenant, form).Scan(&current, &schemaJSON, &uiSchemaJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, application.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if current != revision {
		return false, application.ErrConflict
	}
	raw, err := json.Marshal(diagnostics)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE form_drafts SET validation_status=$4,diagnostics=$5::jsonb,updated_at=$6,updated_by=$7 WHERE tenant_id=$1 AND form_id=$2 AND revision=$3`, tenant, form, revision, validation, raw, at, actor); err != nil {
		return false, err
	}
	before := sha256.Sum256([]byte(schemaJSON + uiSchemaJSON))
	after := sha256.Sum256(append(append([]byte(schemaJSON+uiSchemaJSON), raw...), []byte(validation)...))
	if err = insertFormAudit(ctx, tx, tenant, form, actor, command, correlation, "FORM_DRAFT_VALIDATED", before[:], after[:], at, map[string]any{"revision": revision, "status": validation, "diagnostics": diagnostics}); err != nil {
		return false, err
	}
	response, _ := json.Marshal(map[string]any{"form_id": formID, "revision": revision, "valid": validation == "VALID", "diagnostics": diagnostics})
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'validate_form_draft',$4,$5,$6)`, tenant, key, command, digest, response, at); err != nil {
		return false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) PublishForm(ctx context.Context, tenantID, formID string, expected int64, digest []byte, versionTag, summary, actorID, commandID, key, correlation string, requestDigest []byte, at time.Time) (application.FormVersion, bool, error) {
	tenant, actor, command, err := parseIDs(tenantID, actorID, commandID)
	if err != nil || len(digest) != sha256.Size || len(requestDigest) != sha256.Size {
		return application.FormVersion{}, false, application.ErrInvalidArgument
	}
	form, err := uuid.Parse(formID)
	if err != nil {
		return application.FormVersion{}, false, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.FormVersion{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockIdempotency(ctx, tx, tenant, key); err != nil {
		return application.FormVersion{}, false, err
	}
	var old, raw []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM authoring_command_dedup WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&old, &raw)
	if err == nil {
		if subtle.ConstantTimeCompare(old, requestDigest) != 1 {
			return application.FormVersion{}, false, application.ErrIdempotencyConflict
		}
		var saved struct {
			Version application.FormVersion `json:"version"`
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return application.FormVersion{}, false, err
		}
		return saved.Version, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return application.FormVersion{}, false, err
	}
	if err = rejectCommandReuse(ctx, tx, tenant, command); err != nil {
		return application.FormVersion{}, false, err
	}
	var revision int64
	var status string
	err = tx.QueryRow(ctx, `SELECT revision,status FROM form_definitions WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`, tenant, form).Scan(&revision, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.FormVersion{}, false, application.ErrNotFound
	}
	if err != nil {
		return application.FormVersion{}, false, err
	}
	if revision != expected {
		return application.FormVersion{}, false, application.ErrConflict
	}
	var d application.FormDraft
	d.FormID = formID
	err = tx.QueryRow(ctx, `SELECT revision,schema_json::text,ui_schema_json::text,validation_status FROM form_drafts WHERE tenant_id=$1 AND form_id=$2 FOR UPDATE`, tenant, form).Scan(&d.Revision, &d.SchemaJSON, &d.UISchemaJSON, &d.Validation)
	if err != nil {
		return application.FormVersion{}, false, err
	}
	if d.Revision != expected {
		return application.FormVersion{}, false, application.ErrConflict
	}
	if d.Validation != "VALID" {
		return application.FormVersion{}, false, application.ErrDraftNotValidated
	}
	var next int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(max(version_num),0)+1 FROM form_versions WHERE tenant_id=$1 AND form_id=$2`, tenant, form).Scan(&next)
	if err != nil {
		return application.FormVersion{}, false, err
	}
	id := uuid.New()
	v := application.FormVersion{ID: id.String(), TenantID: tenantID, FormID: formID, VersionNum: next, SourceRevision: expected, SchemaJSON: d.SchemaJSON, UISchemaJSON: d.UISchemaJSON, ContentHash: append([]byte(nil), digest...), CreatedBy: actorID, CreatedAt: at, VersionTag: versionTag, Summary: summary}
	_, err = tx.Exec(ctx, `INSERT INTO form_versions(tenant_id,id,form_id,version_num,source_revision,schema_json,ui_schema_json,content_hash,created_by,created_at,version_tag,summary) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9,$10,$11,$12)`, tenant, id, form, next, expected, d.SchemaJSON, d.UISchemaJSON, digest, actor, at, versionTag, summary)
	if err != nil {
		return application.FormVersion{}, false, mapConflict(err)
	}
	_, err = tx.Exec(ctx, `UPDATE form_definitions SET status='PUBLISHED',latest_version_id=$3,row_version=row_version+1,updated_at=$4,updated_by=$5 WHERE tenant_id=$1 AND id=$2`, tenant, form, id, at, actor)
	if err != nil {
		return application.FormVersion{}, false, mapConflict(err)
	}
	if err = insertFormAudit(ctx, tx, tenant, form, actor, command, correlation, "FORM_PUBLISHED", nil, digest, at, map[string]any{"version_id": id.String(), "version_num": next, "source_revision": expected}); err != nil {
		return application.FormVersion{}, false, err
	}
	encoded, _ := json.Marshal(struct {
		Version application.FormVersion `json:"version"`
	}{v})
	if _, err = tx.Exec(ctx, `INSERT INTO authoring_command_dedup(tenant_id,idempotency_key,command_id,operation,request_digest,response,created_at) VALUES($1,$2,$3,'publish_form',$4,$5,$6)`, tenant, key, command, requestDigest, encoded, at); err != nil {
		return application.FormVersion{}, false, mapConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return application.FormVersion{}, false, err
	}
	return v, false, nil
}

func (s *Store) ListFormVersions(ctx context.Context, tenantID, formID, pageToken string, limit int) ([]application.FormVersionSummary, string, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, "", application.ErrInvalidArgument
	}
	form, err := uuid.Parse(formID)
	if err != nil {
		return nil, "", application.ErrNotFound
	}
	cursor, err := decodeVersionCursor(pageToken, tenantID, formID)
	if err != nil {
		return nil, "", err
	}
	var before any
	if cursor != nil {
		before = cursor.VersionNum
	}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,form_id,version_num,source_revision,created_by,created_at,version_tag,summary FROM form_versions WHERE tenant_id=$1 AND form_id=$2 AND ($3::bigint IS NULL OR version_num<$3) ORDER BY version_num DESC LIMIT $4`, tenant, form, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]application.FormVersionSummary, 0, limit+1)
	for rows.Next() {
		var x application.FormVersionSummary
		var id, tid, fid, actor uuid.UUID
		if err = rows.Scan(&id, &tid, &fid, &x.VersionNum, &x.SourceRevision, &actor, &x.CreatedAt, &x.VersionTag, &x.Summary); err != nil {
			return nil, "", err
		}
		x.ID = id.String()
		x.TenantID = tid.String()
		x.FormID = fid.String()
		x.CreatedBy = actor.String()
		items = append(items, x)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) <= limit {
		return items, "", nil
	}
	items = items[:limit]
	next, err := encodeVersionCursor(versionPageCursor{TenantID: tenantID, ModelID: formID, VersionNum: items[len(items)-1].VersionNum})
	return items, next, err
}
func (s *Store) GetFormVersion(ctx context.Context, tenantID, formID, versionID string) (application.FormVersion, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return application.FormVersion{}, application.ErrNotFound
	}
	form, err := uuid.Parse(formID)
	if err != nil {
		return application.FormVersion{}, application.ErrNotFound
	}
	id, err := uuid.Parse(versionID)
	if err != nil {
		return application.FormVersion{}, application.ErrNotFound
	}
	var v application.FormVersion
	var vid, tid, fid, actor uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT id,tenant_id,form_id,version_num,source_revision,schema_json::text,ui_schema_json::text,content_hash,created_by,created_at,version_tag,summary FROM form_versions WHERE tenant_id=$1 AND form_id=$2 AND id=$3`, tenant, form, id).Scan(&vid, &tid, &fid, &v.VersionNum, &v.SourceRevision, &v.SchemaJSON, &v.UISchemaJSON, &v.ContentHash, &actor, &v.CreatedAt, &v.VersionTag, &v.Summary)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, application.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.ID = vid.String()
	v.TenantID = tid.String()
	v.FormID = fid.String()
	v.CreatedBy = actor.String()
	return v, nil
}

func insertFormAudit(ctx context.Context, tx pgx.Tx, tenant, form, actor, command uuid.UUID, correlation, action string, before, after []byte, at time.Time, details any) error {
	if correlation == "" || len(correlation) > 256 {
		return application.ErrInvalidArgument
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO authoring_form_audit_evidence(tenant_id,id,form_id,actor_id,action,command_id,correlation_id,before_digest,after_digest,details,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, uuid.New(), form, actor, action, command, correlation, before, after, raw, at)
	return err
}
