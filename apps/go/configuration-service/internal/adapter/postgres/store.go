package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("configuration PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Create(ctx context.Context, actor domain.Actor, profile domain.Profile, version domain.Version) (domain.Profile, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Profile{}, err
	}
	defer tx.Rollback(ctx)
	duplicateID, duplicate, err := claimIdempotency(ctx, tx, actor, "configuration.create")
	if err != nil {
		return domain.Profile{}, err
	}
	if duplicate {
		_ = tx.Rollback(ctx)
		return loadProfile(ctx, s.pool, actor.TenantID, duplicateID)
	}
	_, err = tx.Exec(ctx, `INSERT INTO configuration_profiles
		(id,tenant_id,owner,name,scope_type,scope_reference,aggregate_version,is_deleted,created_at,created_by,updated_at,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,false,$8,$9,$8,$9)`,
		profile.ID, profile.TenantID, profile.Owner, profile.Name, profile.Scope.Type,
		profile.Scope.Reference, profile.AggregateVersion, profile.CreatedAt, actor.ActorID)
	if err != nil {
		return domain.Profile{}, mapError(err)
	}
	if err = insertVersion(ctx, tx, version); err != nil {
		return domain.Profile{}, mapError(err)
	}
	if err = insertAudit(ctx, tx, actor, profile.ID, version.ID, "CONFIGURATION_DRAFT_CREATED", profile.AggregateVersion, version.ConfigVersion, version.PolicyVersion, version.Reason, version.ContentHash[:], profile.CreatedAt); err != nil {
		return domain.Profile{}, err
	}
	if err = completeIdempotency(ctx, tx, actor, profile.ID, version.ID); err != nil {
		return domain.Profile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Profile{}, err
	}
	profile.Latest = &version
	return profile, nil
}

func (s *Store) List(ctx context.Context, tenantID, afterName, afterID string, limit int) ([]domain.Profile, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id::text,p.tenant_id,p.owner,p.name,p.scope_type,p.scope_reference,
		p.aggregate_version,COALESCE(p.current_published_version_id::text,''),p.is_deleted,p.created_at,p.updated_at,
		COALESCE(v.id::text,''),COALESCE(v.ordinal,0),COALESCE(v.config_version,''),COALESCE(v.policy_version,''),
		COALESCE(v.schema_version,0),COALESCE(v.status,''),COALESCE(v.reason,''),v.created_at,COALESCE(v.created_by,'')
		FROM configuration_profiles p
		LEFT JOIN LATERAL (
			SELECT * FROM configuration_versions x WHERE x.profile_id=p.id ORDER BY x.ordinal DESC LIMIT 1
		) v ON true
		WHERE p.tenant_id=$1 AND NOT p.is_deleted
		  AND ($2='' OR (p.name,p.id) > ($2,$3::uuid))
		ORDER BY p.name,p.id LIMIT $4`, tenantID, afterName, nullableUUID(afterID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Profile, 0, limit)
	for rows.Next() {
		profile, err := scanProfileWithLatest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, profile)
	}
	return out, rows.Err()
}

func (s *Store) ProfileOwner(ctx context.Context, tenantID, profileID string) (domain.Owner, error) {
	var owner domain.Owner
	err := s.pool.QueryRow(ctx, `SELECT owner FROM configuration_profiles
		WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted`, tenantID, profileID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return owner, err
}

func (s *Store) Get(ctx context.Context, tenantID, profileID string) (domain.Profile, []domain.Version, error) {
	profile, err := loadProfile(ctx, s.pool, tenantID, profileID)
	if err != nil {
		return domain.Profile{}, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,profile_id::text,tenant_id,ordinal,config_version,policy_version,
		schema_version,status,values_json::text,content_hash,reason,created_at,created_by,published_at,COALESCE(published_by,'')
		FROM configuration_versions WHERE tenant_id=$1 AND profile_id=$2
		ORDER BY ordinal DESC`, tenantID, profileID)
	if err != nil {
		return domain.Profile{}, nil, err
	}
	defer rows.Close()
	versions := make([]domain.Version, 0, 8)
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return domain.Profile{}, nil, err
		}
		versions = append(versions, version)
	}
	return profile, versions, rows.Err()
}

func (s *Store) Resolve(
	ctx context.Context,
	lookup domain.ResolutionLookup,
) (domain.ResolvedConfiguration, error) {
	row := s.pool.QueryRow(ctx, `SELECT
		p.id::text,p.tenant_id,p.owner,p.name,p.scope_type,p.scope_reference,p.aggregate_version,
		COALESCE(p.current_published_version_id::text,''),p.is_deleted,p.created_at,p.updated_at,
		v.id::text,v.profile_id::text,v.tenant_id,v.ordinal,v.config_version,v.policy_version,
		v.schema_version,v.status,v.values_json::text,v.content_hash,v.reason,v.created_at,v.created_by,
		v.published_at,COALESCE(v.published_by,'')
		FROM configuration_active_scopes a
		JOIN configuration_profiles p ON p.id=a.profile_id AND p.tenant_id=a.tenant_id
		JOIN configuration_versions v ON v.id=a.version_id AND v.profile_id=p.id
		WHERE a.tenant_id=$1 AND a.owner=$2 AND NOT p.is_deleted AND v.status='PUBLISHED' AND (
			(a.scope_type='APPROVED_INSTANCE_OVERRIDE' AND $7<>'' AND a.scope_reference=$7) OR
			(a.scope_type='WORKFLOW_VERSION' AND a.scope_reference=$3 || ':' || $4) OR
			(a.scope_type='WORKFLOW_TYPE' AND a.scope_reference=$3) OR
			(a.scope_type='TENANT' AND a.scope_reference=$1) OR
			(a.scope_type='ENVIRONMENT' AND a.scope_reference=$6) OR
			(a.scope_type='PLATFORM' AND a.scope_reference=$5)
		)
		ORDER BY CASE a.scope_type
			WHEN 'APPROVED_INSTANCE_OVERRIDE' THEN 6
			WHEN 'WORKFLOW_VERSION' THEN 5
			WHEN 'WORKFLOW_TYPE' THEN 4
			WHEN 'TENANT' THEN 3
			WHEN 'ENVIRONMENT' THEN 2
			WHEN 'PLATFORM' THEN 1
			ELSE 0 END DESC
		LIMIT 1`,
		lookup.TenantID, lookup.Owner, lookup.WorkflowType, lookup.WorkflowVersion,
		lookup.PlatformReference, lookup.EnvironmentReference, lookup.InstanceID)
	var resolved domain.ResolvedConfiguration
	var scopeType, status, values string
	var hash []byte
	err := row.Scan(
		&resolved.Profile.ID, &resolved.Profile.TenantID, &resolved.Profile.Owner, &resolved.Profile.Name,
		&scopeType, &resolved.Profile.Scope.Reference, &resolved.Profile.AggregateVersion,
		&resolved.Profile.CurrentPublishedVersionID, &resolved.Profile.IsDeleted,
		&resolved.Profile.CreatedAt, &resolved.Profile.UpdatedAt,
		&resolved.Version.ID, &resolved.Version.ProfileID, &resolved.Version.TenantID,
		&resolved.Version.Ordinal, &resolved.Version.ConfigVersion,
		&resolved.Version.PolicyVersion, &resolved.Version.SchemaVersion, &status,
		&values, &hash, &resolved.Version.Reason, &resolved.Version.CreatedAt,
		&resolved.Version.CreatedBy, &resolved.Version.PublishedAt,
		&resolved.Version.PublishedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ResolvedConfiguration{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ResolvedConfiguration{}, err
	}
	resolved.Profile.Scope.Type = domain.ScopeType(scopeType)
	resolved.Version.Status = domain.VersionStatus(status)
	resolved.Version.ValuesJSON = []byte(values)
	copy(resolved.Version.ContentHash[:], hash)
	return resolved, nil
}

func (s *Store) AddDraft(ctx context.Context, actor domain.Actor, profileID string, expected int64, version domain.Version) (domain.Profile, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Profile{}, err
	}
	defer tx.Rollback(ctx)
	duplicateID, duplicate, err := claimIdempotency(ctx, tx, actor, "configuration.draft")
	if err != nil {
		return domain.Profile{}, err
	}
	if duplicate {
		_ = tx.Rollback(ctx)
		return loadProfile(ctx, s.pool, actor.TenantID, duplicateID)
	}
	current, ordinal, err := lockProfile(ctx, tx, actor.TenantID, profileID)
	if err != nil {
		return domain.Profile{}, err
	}
	if current != expected {
		return domain.Profile{}, domain.ErrConflict
	}
	version.Ordinal = ordinal + 1
	if err = insertVersion(ctx, tx, version); err != nil {
		return domain.Profile{}, mapError(err)
	}
	next := current + 1
	tag, err := tx.Exec(ctx, `UPDATE configuration_profiles SET aggregate_version=$1,updated_at=$2,updated_by=$3
		WHERE tenant_id=$4 AND id=$5 AND aggregate_version=$6 AND NOT is_deleted`,
		next, version.CreatedAt, actor.ActorID, actor.TenantID, profileID, current)
	if err != nil || tag.RowsAffected() != 1 {
		return domain.Profile{}, domain.ErrConflict
	}
	if err = insertAudit(ctx, tx, actor, profileID, version.ID, "CONFIGURATION_DRAFT_CREATED", next, version.ConfigVersion, version.PolicyVersion, version.Reason, version.ContentHash[:], version.CreatedAt); err != nil {
		return domain.Profile{}, err
	}
	if err = completeIdempotency(ctx, tx, actor, profileID, version.ID); err != nil {
		return domain.Profile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Profile{}, err
	}
	return s.profileAfterWrite(ctx, actor.TenantID, profileID)
}

func (s *Store) Publish(ctx context.Context, actor domain.Actor, profileID, versionID string, expected int64, reason string, now time.Time) (domain.Profile, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Profile{}, err
	}
	defer tx.Rollback(ctx)
	duplicateID, duplicate, err := claimIdempotency(ctx, tx, actor, "configuration.publish")
	if err != nil {
		return domain.Profile{}, err
	}
	if duplicate {
		_ = tx.Rollback(ctx)
		return loadProfile(ctx, s.pool, actor.TenantID, duplicateID)
	}
	current, _, err := lockProfile(ctx, tx, actor.TenantID, profileID)
	if err != nil {
		return domain.Profile{}, err
	}
	if current != expected {
		return domain.Profile{}, domain.ErrConflict
	}
	var hash []byte
	var configVersion string
	var policyVersion string
	err = tx.QueryRow(ctx, `SELECT content_hash,config_version,policy_version FROM configuration_versions
		WHERE tenant_id=$1 AND profile_id=$2 AND id=$3 AND status='DRAFT' FOR UPDATE`,
		actor.TenantID, profileID, versionID).Scan(&hash, &configVersion, &policyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Profile{}, err
	}
	if err = claimActiveScope(ctx, tx, actor.TenantID, profileID, versionID, now); err != nil {
		return domain.Profile{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE configuration_versions SET status='RETIRED'
		WHERE tenant_id=$1 AND profile_id=$2 AND status='PUBLISHED'`, actor.TenantID, profileID); err != nil {
		return domain.Profile{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE configuration_versions SET status='PUBLISHED',published_at=$1,published_by=$2
		WHERE tenant_id=$3 AND profile_id=$4 AND id=$5`, now, actor.ActorID, actor.TenantID, profileID, versionID); err != nil {
		return domain.Profile{}, err
	}
	next := current + 1
	if _, err = tx.Exec(ctx, `UPDATE configuration_profiles SET aggregate_version=$1,current_published_version_id=$2,
		updated_at=$3,updated_by=$4 WHERE tenant_id=$5 AND id=$6`,
		next, versionID, now, actor.ActorID, actor.TenantID, profileID); err != nil {
		return domain.Profile{}, err
	}
	if err = insertAudit(ctx, tx, actor, profileID, versionID, "CONFIGURATION_PUBLISHED", next, configVersion, policyVersion, reason, hash, now); err != nil {
		return domain.Profile{}, err
	}
	if err = insertOutbox(ctx, tx, actor.TenantID, profileID, versionID, configVersion, hash, "configuration.published", now); err != nil {
		return domain.Profile{}, err
	}
	if err = refreshTenantReadiness(ctx, tx, actor.TenantID, now); err != nil {
		return domain.Profile{}, err
	}
	if err = completeIdempotency(ctx, tx, actor, profileID, versionID); err != nil {
		return domain.Profile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Profile{}, err
	}
	return s.profileAfterWrite(ctx, actor.TenantID, profileID)
}

func (s *Store) Rollback(ctx context.Context, actor domain.Actor, profileID, targetID string, expected int64, version domain.Version, now time.Time) (domain.Profile, error) {
	return s.restoreVersion(
		ctx,
		actor,
		profileID,
		targetID,
		expected,
		version,
		now,
		"configuration.rollback",
		"CONFIGURATION_ROLLED_BACK",
		"configuration.rolled_back",
	)
}

func (s *Store) Restore(ctx context.Context, actor domain.Actor, profileID, targetID string, expected int64, version domain.Version, now time.Time) (domain.Profile, error) {
	return s.restoreVersion(
		ctx,
		actor,
		profileID,
		targetID,
		expected,
		version,
		now,
		"configuration.restore",
		"CONFIGURATION_RESTORED",
		"configuration.restored",
	)
}

func (s *Store) restoreVersion(
	ctx context.Context,
	actor domain.Actor,
	profileID string,
	targetID string,
	expected int64,
	version domain.Version,
	now time.Time,
	operation string,
	auditAction string,
	eventType string,
) (domain.Profile, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Profile{}, err
	}
	defer tx.Rollback(ctx)
	duplicateID, duplicate, err := claimIdempotency(ctx, tx, actor, operation)
	if err != nil {
		return domain.Profile{}, err
	}
	if duplicate {
		_ = tx.Rollback(ctx)
		return loadProfile(ctx, s.pool, actor.TenantID, duplicateID)
	}
	current, ordinal, err := lockProfile(ctx, tx, actor.TenantID, profileID)
	if err != nil {
		return domain.Profile{}, err
	}
	if current != expected {
		return domain.Profile{}, domain.ErrConflict
	}
	var status string
	var values string
	var hash []byte
	err = tx.QueryRow(ctx, `SELECT status,values_json::text,content_hash,schema_version
		FROM configuration_versions WHERE tenant_id=$1 AND profile_id=$2 AND id=$3 FOR UPDATE`,
		actor.TenantID, profileID, targetID).Scan(&status, &values, &hash, &version.SchemaVersion)
	if errors.Is(err, pgx.ErrNoRows) || status == string(domain.StatusDraft) {
		return domain.Profile{}, domain.ErrInvalid
	}
	if err != nil {
		return domain.Profile{}, err
	}
	version.Ordinal = ordinal + 1
	version.ValuesJSON = []byte(values)
	copy(version.ContentHash[:], hash)
	version.PublishedAt = &now
	version.PublishedBy = actor.ActorID
	if _, err = tx.Exec(ctx, `UPDATE configuration_versions SET status='RETIRED'
		WHERE tenant_id=$1 AND profile_id=$2 AND status IN ('PUBLISHED','DRAFT')`, actor.TenantID, profileID); err != nil {
		return domain.Profile{}, err
	}
	if err = insertVersion(ctx, tx, version); err != nil {
		return domain.Profile{}, err
	}
	if err = claimActiveScope(ctx, tx, actor.TenantID, profileID, version.ID, now); err != nil {
		return domain.Profile{}, err
	}
	next := current + 1
	if _, err = tx.Exec(ctx, `UPDATE configuration_profiles SET aggregate_version=$1,current_published_version_id=$2,
		updated_at=$3,updated_by=$4 WHERE tenant_id=$5 AND id=$6`,
		next, version.ID, now, actor.ActorID, actor.TenantID, profileID); err != nil {
		return domain.Profile{}, err
	}
	if err = insertAudit(ctx, tx, actor, profileID, version.ID, auditAction, next, version.ConfigVersion, version.PolicyVersion, version.Reason, hash, now); err != nil {
		return domain.Profile{}, err
	}
	if err = insertOutbox(ctx, tx, actor.TenantID, profileID, version.ID, version.ConfigVersion, hash, eventType, now); err != nil {
		return domain.Profile{}, err
	}
	if err = refreshTenantReadiness(ctx, tx, actor.TenantID, now); err != nil {
		return domain.Profile{}, err
	}
	if err = completeIdempotency(ctx, tx, actor, profileID, version.ID); err != nil {
		return domain.Profile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Profile{}, err
	}
	return s.profileAfterWrite(ctx, actor.TenantID, profileID)
}

func (s *Store) Retire(
	ctx context.Context,
	actor domain.Actor,
	profileID string,
	expected int64,
	reason string,
	now time.Time,
) (domain.Profile, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Profile{}, err
	}
	defer tx.Rollback(ctx)
	duplicateID, duplicate, err := claimIdempotency(ctx, tx, actor, "configuration.retire")
	if err != nil {
		return domain.Profile{}, err
	}
	if duplicate {
		_ = tx.Rollback(ctx)
		return loadProfile(ctx, s.pool, actor.TenantID, duplicateID)
	}
	current, _, err := lockProfile(ctx, tx, actor.TenantID, profileID)
	if err != nil {
		return domain.Profile{}, err
	}
	if current != expected {
		return domain.Profile{}, domain.ErrConflict
	}
	var versionID, configVersion, policyVersion string
	var hash []byte
	err = tx.QueryRow(ctx, `SELECT id::text,config_version,policy_version,content_hash
		FROM configuration_versions
		WHERE tenant_id=$1 AND profile_id=$2 AND status='PUBLISHED' FOR UPDATE`,
		actor.TenantID, profileID).Scan(&versionID, &configVersion, &policyVersion, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Profile{}, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM configuration_active_scopes
		WHERE tenant_id=$1 AND profile_id=$2 AND version_id=$3`,
		actor.TenantID, profileID, versionID)
	if err != nil {
		return domain.Profile{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.Profile{}, domain.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE configuration_versions SET status='RETIRED'
		WHERE tenant_id=$1 AND profile_id=$2 AND id=$3 AND status='PUBLISHED'`,
		actor.TenantID, profileID, versionID); err != nil {
		return domain.Profile{}, err
	}
	next := current + 1
	if _, err = tx.Exec(ctx, `UPDATE configuration_profiles
		SET aggregate_version=$1,current_published_version_id=NULL,updated_at=$2,updated_by=$3
		WHERE tenant_id=$4 AND id=$5`,
		next, now, actor.ActorID, actor.TenantID, profileID); err != nil {
		return domain.Profile{}, err
	}
	if err = insertAudit(ctx, tx, actor, profileID, versionID, "CONFIGURATION_RETIRED", next, configVersion, policyVersion, reason, hash, now); err != nil {
		return domain.Profile{}, err
	}
	if err = insertOutbox(ctx, tx, actor.TenantID, profileID, versionID, configVersion, hash, "configuration.retired", now); err != nil {
		return domain.Profile{}, err
	}
	if err = refreshTenantReadiness(ctx, tx, actor.TenantID, now); err != nil {
		return domain.Profile{}, err
	}
	if err = completeIdempotency(ctx, tx, actor, profileID, versionID); err != nil {
		return domain.Profile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Profile{}, err
	}
	return s.profileAfterWrite(ctx, actor.TenantID, profileID)
}

func claimActiveScope(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	profileID string,
	versionID string,
	now time.Time,
) error {
	var owner, scopeType, scopeReference string
	err := tx.QueryRow(ctx, `SELECT owner,scope_type,scope_reference FROM configuration_profiles
		WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`,
		tenantID, profileID).Scan(&owner, &scopeType, &scopeReference)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO configuration_active_scopes
		(tenant_id,owner,scope_type,scope_reference,profile_id,version_id,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tenant_id,owner,scope_type,scope_reference) DO UPDATE
		SET version_id=EXCLUDED.version_id,updated_at=EXCLUDED.updated_at
		WHERE configuration_active_scopes.profile_id=EXCLUDED.profile_id`,
		tenantID, owner, scopeType, scopeReference, profileID, versionID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) profileAfterWrite(ctx context.Context, tenantID, profileID string) (domain.Profile, error) {
	profile, err := loadProfile(ctx, s.pool, tenantID, profileID)
	return profile, err
}

func lockProfile(ctx context.Context, tx pgx.Tx, tenantID, profileID string) (int64, int64, error) {
	var current, ordinal int64
	err := tx.QueryRow(ctx, `SELECT aggregate_version FROM configuration_profiles
		WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted FOR UPDATE`,
		tenantID, profileID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, domain.ErrNotFound
	}
	if err != nil {
		return 0, 0, err
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(ordinal),0) FROM configuration_versions
		WHERE tenant_id=$1 AND profile_id=$2`, tenantID, profileID).Scan(&ordinal)
	return current, ordinal, err
}

func insertVersion(ctx context.Context, tx pgx.Tx, version domain.Version) error {
	_, err := tx.Exec(ctx, `INSERT INTO configuration_versions
		(id,profile_id,tenant_id,ordinal,config_version,policy_version,schema_version,status,values_json,content_hash,
		 reason,created_at,created_by,published_at,published_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13,$14,$15)`,
		version.ID, version.ProfileID, version.TenantID, version.Ordinal, version.ConfigVersion,
		version.PolicyVersion, version.SchemaVersion, version.Status, string(version.ValuesJSON),
		version.ContentHash[:], version.Reason, version.CreatedAt, version.CreatedBy,
		version.PublishedAt, nullString(version.PublishedBy))
	return err
}

func insertAudit(ctx context.Context, tx pgx.Tx, actor domain.Actor, profileID, versionID, action string, aggregateVersion int64, configVersion, policyVersion, reason string, hash []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO configuration_audit
		(audit_id,tenant_id,profile_id,version_id,actor_id,action,aggregate_version,
		 config_version,policy_version,reason,content_hash,correlation_id,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		uuid.NewString(), actor.TenantID, profileID, versionID, actor.ActorID, action,
		aggregateVersion, configVersion, policyVersion, reason, hash, actor.CorrelationID, now)
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, tenantID, profileID, versionID, configVersion string, hash []byte, eventType string, now time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"tenant_id": tenantID, "profile_id": profileID, "version_id": versionID,
		"config_version": configVersion, "content_hash": fmt.Sprintf("%x", hash),
	})
	if err != nil {
		return err
	}
	var eventSequence int64
	if err = tx.QueryRow(ctx, `UPDATE configuration_outbox_publish_state
		SET next_sequence=next_sequence+1,updated_at=$1
		WHERE singleton_id=1 RETURNING next_sequence`, now).Scan(&eventSequence); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO configuration_outbox
		(event_id,event_sequence,tenant_id,profile_id,version_id,event_type,payload,occurred_at,next_attempt_at)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$8)`,
		uuid.NewString(), eventSequence, tenantID, profileID, versionID, eventType, payload, now)
	return err
}

func claimIdempotency(ctx context.Context, tx pgx.Tx, actor domain.Actor, operation string) (string, bool, error) {
	if actor.IdempotencyKey == "" || actor.CommandID == "" {
		return "", false, domain.ErrInvalid
	}
	tag, err := tx.Exec(ctx, `INSERT INTO configuration_idempotency
		(tenant_id,actor_id,idempotency_key,operation,request_digest,command_id,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
		actor.TenantID, actor.ActorID, actor.IdempotencyKey, operation,
		actor.RequestDigest[:], actor.CommandID, time.Now().UTC())
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() == 1 {
		return "", false, nil
	}
	var storedOperation string
	var storedDigest []byte
	var resultProfileID *string
	err = tx.QueryRow(ctx, `SELECT operation,request_digest,result_profile_id::text
		FROM configuration_idempotency
		WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`,
		actor.TenantID, actor.ActorID, actor.IdempotencyKey).
		Scan(&storedOperation, &storedDigest, &resultProfileID)
	if err != nil {
		return "", false, err
	}
	if storedOperation != operation || !bytes.Equal(storedDigest, actor.RequestDigest[:]) ||
		resultProfileID == nil || *resultProfileID == "" {
		return "", false, domain.ErrConflict
	}
	return *resultProfileID, true, nil
}

func completeIdempotency(ctx context.Context, tx pgx.Tx, actor domain.Actor, profileID, versionID string) error {
	tag, err := tx.Exec(ctx, `UPDATE configuration_idempotency
		SET result_profile_id=$1,result_version_id=$2
		WHERE tenant_id=$3 AND actor_id=$4 AND idempotency_key=$5 AND request_digest=$6`,
		profileID, versionID, actor.TenantID, actor.ActorID, actor.IdempotencyKey, actor.RequestDigest[:])
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadProfile(ctx context.Context, db queryer, tenantID, profileID string) (domain.Profile, error) {
	var profile domain.Profile
	var scopeType string
	err := db.QueryRow(ctx, `SELECT id::text,tenant_id,owner,name,scope_type,scope_reference,aggregate_version,
		COALESCE(current_published_version_id::text,''),is_deleted,created_at,updated_at
		FROM configuration_profiles WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted`,
		tenantID, profileID).Scan(
		&profile.ID, &profile.TenantID, &profile.Owner, &profile.Name, &scopeType, &profile.Scope.Reference,
		&profile.AggregateVersion, &profile.CurrentPublishedVersionID, &profile.IsDeleted,
		&profile.CreatedAt, &profile.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, domain.ErrNotFound
	}
	profile.Scope.Type = domain.ScopeType(scopeType)
	return profile, err
}

func scanProfileWithLatest(row rowScanner) (domain.Profile, error) {
	var profile domain.Profile
	var scopeType, versionID, configVersion, policyVersion, status, reason, createdBy string
	var ordinal int64
	var schemaVersion uint32
	var versionCreatedAt *time.Time
	err := row.Scan(
		&profile.ID, &profile.TenantID, &profile.Owner, &profile.Name, &scopeType, &profile.Scope.Reference,
		&profile.AggregateVersion, &profile.CurrentPublishedVersionID, &profile.IsDeleted,
		&profile.CreatedAt, &profile.UpdatedAt, &versionID, &ordinal, &configVersion,
		&policyVersion, &schemaVersion, &status, &reason, &versionCreatedAt, &createdBy)
	if err != nil {
		return domain.Profile{}, err
	}
	profile.Scope.Type = domain.ScopeType(scopeType)
	if versionID != "" && versionCreatedAt != nil {
		profile.Latest = &domain.Version{
			ID: versionID, ProfileID: profile.ID, TenantID: profile.TenantID, Ordinal: ordinal,
			ConfigVersion: configVersion, PolicyVersion: policyVersion, SchemaVersion: schemaVersion,
			Status: domain.VersionStatus(status), Reason: reason, CreatedAt: *versionCreatedAt, CreatedBy: createdBy,
		}
	}
	return profile, nil
}

func scanVersion(row rowScanner) (domain.Version, error) {
	var version domain.Version
	var status, values string
	var hash []byte
	err := row.Scan(
		&version.ID, &version.ProfileID, &version.TenantID, &version.Ordinal, &version.ConfigVersion,
		&version.PolicyVersion, &version.SchemaVersion, &status, &values, &hash, &version.Reason,
		&version.CreatedAt, &version.CreatedBy, &version.PublishedAt, &version.PublishedBy)
	version.Status = domain.VersionStatus(status)
	version.ValuesJSON = []byte(values)
	copy(version.ContentHash[:], hash)
	return version, err
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.ErrConflict
	}
	return err
}
