package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres pool is required")
	}
	return &Store{pool: pool}, nil
}

func parseUUID(raw string) uuid.UUID {
	if parsed, err := uuid.Parse(raw); err == nil {
		return parsed
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(raw))
}

func nullableUUID(raw string) *uuid.UUID {
	if raw == "" {
		return nil
	}
	u := parseUUID(raw)
	return &u
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func derefUUID(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func mapPGError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return application.ErrConflict
		}
		if pgErr.Code == "P0001" {
			return application.ErrImmutable
		}
	}
	return err
}

func (s *Store) GetCommandReceipt(ctx context.Context, tenantID, commandID string) (*application.CommandReceipt, bool, error) {
	var rec application.CommandReceipt
	var tID, rID, cID, actRef, aggID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, command_id, intent_digest, actor_ref, aggregate_id, status, result, created_at
		FROM command_receipts
		WHERE tenant_id = $1 AND command_id = $2 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(commandID)).Scan(
		&tID, &rID, &cID, &rec.IntentDigest, &actRef, &aggID, &rec.Status, &rec.ResultJSON, &rec.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rec.TenantID = tID.String()
	rec.ID = rID.String()
	rec.CommandID = cID.String()
	rec.ActorRef = actRef.String()
	rec.AggregateID = aggID.String()
	return &rec, true, nil
}

func (s *Store) SaveCommandReceipt(ctx context.Context, rec application.CommandReceipt) error {
	now := rec.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO command_receipts (
			tenant_id, id, command_id, intent_digest, actor_ref, aggregate_id, status, result,
			created_at, updated_at, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10,$10)
	`, parseUUID(rec.TenantID), parseUUID(rec.ID), parseUUID(rec.CommandID), rec.IntentDigest,
		parseUUID(rec.ActorRef), parseUUID(rec.AggregateID), rec.Status, rec.ResultJSON, now, rec.ActorRef)
	return mapPGError(err)
}

func (s *Store) AppendAudit(ctx context.Context, rec application.AuditRecord) error {
	now := rec.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	resType := rec.ResourceType
	if resType == "" {
		resType = "TEMPLATE"
	}
	sev := rec.Severity
	if sev == "" {
		sev = "INFO"
	}
	var detailsJSON []byte
	if len(rec.Details) > 0 {
		detailsJSON, _ = json.Marshal(rec.Details)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_evidence (
			tenant_id, id, actor_ref, actor_display, actor_role, client_ip,
			action, resource_type, resource_ref, severity, command_id,
			before_digest, after_digest, details,
			occurred_at, created_at, updated_at, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15,$15,$16,$16)
	`, parseUUID(rec.TenantID), parseUUID(rec.ID), parseUUID(rec.ActorRef),
		rec.ActorDisplay, rec.ActorRole, rec.ClientIP,
		rec.Action, resType, rec.ResourceRef, sev, nullableUUID(rec.CommandID),
		rec.BeforeDigest, rec.AfterDigest, detailsJSON, now, rec.ActorRef)
	return mapPGError(err)
}

func (s *Store) ListAudit(ctx context.Context, tenantID string, limit int) ([]application.AuditRecord, error) {
	return s.ListAuditFiltered(ctx, application.AuditFilter{TenantID: tenantID, Limit: limit})
}

func (s *Store) ListAuditFiltered(ctx context.Context, filter application.AuditFilter) ([]application.AuditRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT tenant_id, id, actor_ref, COALESCE(actor_display,''), COALESCE(actor_role,''), COALESCE(client_ip,''),
		       action, resource_type, resource_ref, severity, command_id,
		       COALESCE(before_digest,''), COALESCE(after_digest,''), details, occurred_at
		FROM audit_evidence
		WHERE tenant_id = $1`
	args := []any{parseUUID(filter.TenantID)}
	idx := 2
	if filter.ResourceType != "" {
		query += fmt.Sprintf(" AND resource_type = $%d", idx)
		args = append(args, filter.ResourceType)
		idx++
	}
	if filter.Severity != "" {
		query += fmt.Sprintf(" AND severity = $%d", idx)
		args = append(args, filter.Severity)
		idx++
	}
	if filter.Query != "" {
		query += fmt.Sprintf(" AND (action ILIKE $%d OR resource_ref ILIKE $%d)", idx, idx)
		args = append(args, "%"+filter.Query+"%")
		idx++
	}
	query += fmt.Sprintf(" ORDER BY occurred_at DESC, id DESC LIMIT $%d", idx)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.AuditRecord
	for rows.Next() {
		var r application.AuditRecord
		var tID, rID, aRef uuid.UUID
		var cID *uuid.UUID
		var detailsJSON []byte
		if err := rows.Scan(&tID, &rID, &aRef, &r.ActorDisplay, &r.ActorRole, &r.ClientIP,
			&r.Action, &r.ResourceType, &r.ResourceRef, &r.Severity, &cID,
			&r.BeforeDigest, &r.AfterDigest, &detailsJSON, &r.OccurredAt); err != nil {
			return nil, err
		}
		r.TenantID = tID.String()
		r.ID = rID.String()
		r.ActorRef = aRef.String()
		r.CommandID = derefUUID(cID)
		if len(detailsJSON) > 0 {
			_ = json.Unmarshal(detailsJSON, &r.Details)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AppendOutbox(ctx context.Context, msg application.OutboxMessage) error {
	now := msg.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outbox (
			tenant_id, id, event_id, aggregate_id, aggregate_version, event_type, payload, attempts,
			created_at, updated_at, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$8,'system','system')
	`, parseUUID(msg.TenantID), parseUUID(msg.ID), parseUUID(msg.EventID), parseUUID(msg.AggregateID),
		int64(msg.AggregateVersion), msg.EventType, msg.PayloadJSON, now)
	return mapPGError(err)
}

func (s *Store) ClaimPendingOutbox(ctx context.Context, limit int) ([]application.OutboxMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, id, event_id, aggregate_id, aggregate_version, event_type, payload, attempts, created_at
		FROM outbox
		WHERE published_at IS NULL AND NOT is_deleted
		ORDER BY created_at ASC, id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.OutboxMessage
	for rows.Next() {
		var m application.OutboxMessage
		var tID, id, evID, aggID uuid.UUID
		var aggVer int64
		if err := rows.Scan(&tID, &id, &evID, &aggID, &aggVer, &m.EventType, &m.PayloadJSON, &m.Attempts, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.TenantID, m.ID, m.EventID, m.AggregateID = tID.String(), id.String(), evID.String(), aggID.String()
		m.AggregateVersion = uint64(aggVer)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MarkOutboxPublished(ctx context.Context, tenantID, id string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE outbox SET published_at = $3, updated_at = $3
		WHERE tenant_id = $1 AND id = $2 AND published_at IS NULL
	`, parseUUID(tenantID), parseUUID(id), now.UTC())
	return mapPGError(err)
}

func (s *Store) RecordInboxEvent(ctx context.Context, tenantID, source, eventID, digest string, now time.Time) (bool, error) {
	var existingDigest string
	err := s.pool.QueryRow(ctx, `
		SELECT payload_digest FROM inbox WHERE tenant_id = $1 AND source = $2 AND event_id = $3 AND NOT is_deleted
	`, parseUUID(tenantID), source, eventID).Scan(&existingDigest)
	if err == nil {
		if existingDigest != digest {
			return false, application.ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO inbox (tenant_id, id, source, event_id, payload_digest, applied_at, created_at, updated_at, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$6,$6,'intake','intake')
	`, parseUUID(tenantID), uuid.New(), source, eventID, digest, now.UTC())
	return false, mapPGError(err)
}

func (s *Store) SaveCategory(ctx context.Context, cat application.Category) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO categories (
			tenant_id, id, category_key, name, default_lane, user_unsubscribable,
			ambiguous_outcome_policy, retention_class, retain_rendered_copy, status,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, parseUUID(cat.TenantID), parseUUID(cat.ID), cat.CategoryKey, cat.Name, cat.DefaultLane,
		cat.UserUnsubscribable, cat.AmbiguousOutcomePolicy, cat.RetentionClass, cat.RetainRenderedCopy,
		cat.Status, cat.CreatedAt, cat.UpdatedAt, int64(cat.RowVersion), cat.CreatedBy, cat.UpdatedBy)
	return mapPGError(err)
}

func (s *Store) GetCategory(ctx context.Context, tenantID, id string) (*application.Category, error) {
	return s.scanCategory(ctx, `WHERE tenant_id = $1 AND id = $2 AND NOT is_deleted`, parseUUID(tenantID), parseUUID(id))
}

func (s *Store) GetCategoryByKey(ctx context.Context, tenantID, key string) (*application.Category, error) {
	return s.scanCategory(ctx, `WHERE tenant_id = $1 AND category_key = $2 AND NOT is_deleted`, parseUUID(tenantID), key)
}

func (s *Store) scanCategory(ctx context.Context, clause string, args ...any) (*application.Category, error) {
	var c application.Category
	var tID, cID uuid.UUID
	var rv int64
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, category_key, name, default_lane, user_unsubscribable,
		       ambiguous_outcome_policy, retention_class, retain_rendered_copy, status,
		       created_at, updated_at, row_version, created_by, updated_by
		FROM categories `+clause, args...).Scan(
		&tID, &cID, &c.CategoryKey, &c.Name, &c.DefaultLane, &c.UserUnsubscribable,
		&c.AmbiguousOutcomePolicy, &c.RetentionClass, &c.RetainRenderedCopy, &c.Status,
		&c.CreatedAt, &c.UpdatedAt, &rv, &c.CreatedBy, &c.UpdatedBy,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	c.TenantID, c.ID, c.RowVersion = tID.String(), cID.String(), uint64(rv)
	return &c, nil
}

func (s *Store) ListCategories(ctx context.Context, tenantID string) ([]application.Category, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, id, category_key, name, default_lane, user_unsubscribable,
		       ambiguous_outcome_policy, retention_class, retain_rendered_copy, status,
		       created_at, updated_at, row_version, created_by, updated_by
		FROM categories WHERE tenant_id = $1 AND NOT is_deleted ORDER BY category_key ASC
	`, parseUUID(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.Category
	for rows.Next() {
		var c application.Category
		var tID, cID uuid.UUID
		var rv int64
		if err := rows.Scan(&tID, &cID, &c.CategoryKey, &c.Name, &c.DefaultLane, &c.UserUnsubscribable,
			&c.AmbiguousOutcomePolicy, &c.RetentionClass, &c.RetainRenderedCopy, &c.Status,
			&c.CreatedAt, &c.UpdatedAt, &rv, &c.CreatedBy, &c.UpdatedBy); err != nil {
			return nil, err
		}
		c.TenantID, c.ID, c.RowVersion = tID.String(), cID.String(), uint64(rv)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveApplication(ctx context.Context, app application.Application) error {
	nsJSON, _ := json.Marshal(app.AllowedNamespaces)
	chJSON, _ := json.Marshal(app.AllowedChannels)
	qJSON, _ := json.Marshal(app.QuotaConfig)
	var senderJSON []byte
	if len(app.DefaultSenderIdentity) > 0 {
		senderJSON, _ = json.Marshal(app.DefaultSenderIdentity)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO applications (
			tenant_id, id, app_key, name, auth_mode, principal_ref, allowed_namespaces, allowed_channels,
			default_lane, quota_config, callback_url, callback_secret_ref, owner_team, default_sender_identity, status,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`, parseUUID(app.TenantID), parseUUID(app.ID), app.AppKey, app.Name, app.AuthMode, app.PrincipalRef,
		nsJSON, chJSON, app.DefaultLane, qJSON, app.CallbackURL, app.CallbackSecretRef, app.OwnerTeam, senderJSON, app.Status,
		app.CreatedAt, app.UpdatedAt, int64(app.RowVersion), app.CreatedBy, app.UpdatedBy)
	return mapPGError(err)
}

func (s *Store) GetApplication(ctx context.Context, tenantID, id string) (*application.Application, error) {
	return s.scanApp(ctx, `WHERE tenant_id = $1 AND id = $2 AND NOT is_deleted`, parseUUID(tenantID), parseUUID(id))
}

func (s *Store) GetApplicationByKey(ctx context.Context, tenantID, appKey string) (*application.Application, error) {
	return s.scanApp(ctx, `WHERE tenant_id = $1 AND app_key = $2 AND NOT is_deleted`, parseUUID(tenantID), appKey)
}

func (s *Store) scanApp(ctx context.Context, clause string, args ...any) (*application.Application, error) {
	var a application.Application
	var tID, aID uuid.UUID
	var nsJSON, chJSON, qJSON, senderJSON []byte
	var rv int64
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, app_key, name, auth_mode, principal_ref, allowed_namespaces, allowed_channels,
		       default_lane, quota_config, COALESCE(callback_url,''), COALESCE(callback_secret_ref,''),
		       COALESCE(owner_team,''), default_sender_identity, status,
		       created_at, updated_at, row_version, created_by, updated_by
		FROM applications `+clause, args...).Scan(
		&tID, &aID, &a.AppKey, &a.Name, &a.AuthMode, &a.PrincipalRef, &nsJSON, &chJSON,
		&a.DefaultLane, &qJSON, &a.CallbackURL, &a.CallbackSecretRef, &a.OwnerTeam, &senderJSON, &a.Status,
		&a.CreatedAt, &a.UpdatedAt, &rv, &a.CreatedBy, &a.UpdatedBy,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	a.TenantID, a.ID, a.RowVersion = tID.String(), aID.String(), uint64(rv)
	_ = json.Unmarshal(nsJSON, &a.AllowedNamespaces)
	_ = json.Unmarshal(chJSON, &a.AllowedChannels)
	_ = json.Unmarshal(qJSON, &a.QuotaConfig)
	if len(senderJSON) > 0 {
		_ = json.Unmarshal(senderJSON, &a.DefaultSenderIdentity)
	}
	return &a, nil
}

func (s *Store) ListApplications(ctx context.Context, tenantID string) ([]application.Application, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, id, app_key, name, auth_mode, principal_ref, allowed_namespaces, allowed_channels,
		       default_lane, quota_config, COALESCE(callback_url,''), COALESCE(callback_secret_ref,''),
		       COALESCE(owner_team,''), default_sender_identity, status,
		       created_at, updated_at, row_version, created_by, updated_by
		FROM applications WHERE tenant_id = $1 AND NOT is_deleted ORDER BY app_key ASC
	`, parseUUID(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.Application
	for rows.Next() {
		var a application.Application
		var tID, aID uuid.UUID
		var nsJSON, chJSON, qJSON, senderJSON []byte
		var rv int64
		if err := rows.Scan(&tID, &aID, &a.AppKey, &a.Name, &a.AuthMode, &a.PrincipalRef, &nsJSON, &chJSON,
			&a.DefaultLane, &qJSON, &a.CallbackURL, &a.CallbackSecretRef, &a.OwnerTeam, &senderJSON, &a.Status,
			&a.CreatedAt, &a.UpdatedAt, &rv, &a.CreatedBy, &a.UpdatedBy); err != nil {
			return nil, err
		}
		a.TenantID, a.ID, a.RowVersion = tID.String(), aID.String(), uint64(rv)
		_ = json.Unmarshal(nsJSON, &a.AllowedNamespaces)
		_ = json.Unmarshal(chJSON, &a.AllowedChannels)
		_ = json.Unmarshal(qJSON, &a.QuotaConfig)
		if len(senderJSON) > 0 {
			_ = json.Unmarshal(senderJSON, &a.DefaultSenderIdentity)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CountApplicationRequestsSince(ctx context.Context, tenantID, appID string, since time.Time) (int, error) {
	var cnt int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM notification_requests
		WHERE tenant_id = $1 AND application_id = $2 AND NOT is_test AND created_at >= $3 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(appID), since.UTC()).Scan(&cnt)
	return cnt, err
}

func (s *Store) SaveLayoutVersion(ctx context.Context, lv application.LayoutVersion) error {
	contentJSON, _ := json.Marshal(map[string]string{"html": lv.HTMLTemplate})
	_, err := s.pool.Exec(ctx, `
		INSERT INTO layout_versions (
			tenant_id, id, layout_id, version, syntax, variable_schema, content, content_hash, status,
			published_at, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,'system','system')
		ON CONFLICT (tenant_id, id) DO UPDATE SET
			syntax = EXCLUDED.syntax, variable_schema = EXCLUDED.variable_schema,
			content = EXCLUDED.content, content_hash = EXCLUDED.content_hash,
			status = EXCLUDED.status, published_at = EXCLUDED.published_at,
			updated_at = EXCLUDED.updated_at, row_version = layout_versions.row_version + 1
	`, parseUUID(lv.TenantID), parseUUID(lv.ID), parseUUID(lv.LayoutID), int64(lv.Version),
		lv.Syntax, lv.VariableSchema, contentJSON, lv.ContentHash, lv.Status,
		nullableTime(lv.PublishedAt), lv.CreatedAt, lv.UpdatedAt)
	return mapPGError(err)
}

func (s *Store) GetLayoutVersion(ctx context.Context, tenantID, id string) (*application.LayoutVersion, error) {
	var lv application.LayoutVersion
	var tID, vID, lID uuid.UUID
	var ver, rv int64
	var contentJSON []byte
	var pubAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, layout_id, version, syntax, variable_schema, content, content_hash, status, published_at, created_at, updated_at, row_version
		FROM layout_versions WHERE tenant_id = $1 AND id = $2 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(id)).Scan(
		&tID, &vID, &lID, &ver, &lv.Syntax, &lv.VariableSchema, &contentJSON, &lv.ContentHash, &lv.Status, &pubAt, &lv.CreatedAt, &lv.UpdatedAt, &rv,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	lv.TenantID, lv.ID, lv.LayoutID = tID.String(), vID.String(), lID.String()
	lv.Version, lv.RowVersion, lv.PublishedAt = uint64(ver), uint64(rv), derefTime(pubAt)
	var m map[string]string
	_ = json.Unmarshal(contentJSON, &m)
	lv.HTMLTemplate = m["html"]
	return &lv, nil
}

func (s *Store) CreateTemplate(ctx context.Context, tmpl application.Template) error {
	tags := tmpl.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO templates (
			tenant_id, id, template_key, application_id, category_id, name, default_locale, tags, status,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, parseUUID(tmpl.TenantID), parseUUID(tmpl.ID), tmpl.TemplateKey, parseUUID(tmpl.ApplicationID),
		parseUUID(tmpl.CategoryID), tmpl.Name, tmpl.DefaultLocale, tagsJSON, tmpl.Status,
		tmpl.CreatedAt, tmpl.UpdatedAt, int64(tmpl.RowVersion), tmpl.CreatedBy, tmpl.UpdatedBy)
	return mapPGError(err)
}

func (s *Store) UpdateTemplate(ctx context.Context, tmpl application.Template) error {
	tags := tmpl.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	var delBy, delReason *string
	if tmpl.IsDeleted {
		delBy = &tmpl.DeletedBy
		delReason = &tmpl.DeleteReason
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE templates SET
			name = $3, default_locale = $4, tags = $5, status = $6,
			updated_at = $7, updated_by = $8, row_version = row_version + 1,
			is_deleted = $9, deleted_at = $10, deleted_by = $11, delete_reason = $12
		WHERE tenant_id = $1 AND id = $2
	`, parseUUID(tmpl.TenantID), parseUUID(tmpl.ID), tmpl.Name, tmpl.DefaultLocale, tagsJSON, tmpl.Status,
		tmpl.UpdatedAt, tmpl.UpdatedBy, tmpl.IsDeleted, nullableTime(tmpl.DeletedAt), delBy, delReason)
	return mapPGError(err)
}

func (s *Store) GetTemplate(ctx context.Context, tenantID, id string) (*application.Template, error) {
	return s.scanTemplate(ctx, `WHERE tenant_id = $1 AND id = $2 AND NOT is_deleted`, parseUUID(tenantID), parseUUID(id))
}

func (s *Store) GetTemplateIncludingDeleted(ctx context.Context, tenantID, id string) (*application.Template, error) {
	return s.scanTemplate(ctx, `WHERE tenant_id = $1 AND id = $2`, parseUUID(tenantID), parseUUID(id))
}

func (s *Store) GetTemplateByKey(ctx context.Context, tenantID, key string) (*application.Template, error) {
	return s.scanTemplate(ctx, `WHERE tenant_id = $1 AND template_key = $2 AND NOT is_deleted`, parseUUID(tenantID), key)
}

func (s *Store) scanTemplate(ctx context.Context, clause string, args ...any) (*application.Template, error) {
	var t application.Template
	var tID, id, appID, catID uuid.UUID
	var rv int64
	var tagsJSON []byte
	var delAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, template_key, application_id, category_id, name, default_locale, tags, status,
		       created_at, updated_at, row_version, created_by, updated_by,
		       is_deleted, deleted_at, COALESCE(deleted_by,''), COALESCE(delete_reason,'')
		FROM templates `+clause, args...).Scan(
		&tID, &id, &t.TemplateKey, &appID, &catID, &t.Name, &t.DefaultLocale, &tagsJSON, &t.Status,
		&t.CreatedAt, &t.UpdatedAt, &rv, &t.CreatedBy, &t.UpdatedBy,
		&t.IsDeleted, &delAt, &t.DeletedBy, &t.DeleteReason,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	t.TenantID, t.ID, t.ApplicationID, t.CategoryID = tID.String(), id.String(), appID.String(), catID.String()
	t.RowVersion = uint64(rv)
	t.DeletedAt = derefTime(delAt)
	_ = json.Unmarshal(tagsJSON, &t.Tags)
	if t.Tags == nil {
		t.Tags = []string{}
	}
	return &t, nil
}

func (s *Store) ListTemplates(ctx context.Context, tenantID, appID string, pageSize int, pageToken string) ([]application.Template, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	offset := 0
	if pageToken != "" {
		if n, err := strconv.Atoi(pageToken); err == nil && n >= 0 {
			offset = n
		}
	}
	query := `
		SELECT tenant_id, id, template_key, application_id, category_id, name, default_locale, tags, status,
		       created_at, updated_at, row_version, created_by, updated_by
		FROM templates
		WHERE tenant_id = $1 AND NOT is_deleted`
	args := []any{parseUUID(tenantID)}
	if appID != "" {
		query += ` AND application_id = $2 ORDER BY template_key ASC LIMIT $3 OFFSET $4`
		args = append(args, parseUUID(appID), pageSize+1, offset)
	} else {
		query += ` ORDER BY template_key ASC LIMIT $2 OFFSET $3`
		args = append(args, pageSize+1, offset)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []application.Template
	for rows.Next() {
		var t application.Template
		var tID, id, aID, cID uuid.UUID
		var rv int64
		var tagsJSON []byte
		if err := rows.Scan(&tID, &id, &t.TemplateKey, &aID, &cID, &t.Name, &t.DefaultLocale, &tagsJSON, &t.Status,
			&t.CreatedAt, &t.UpdatedAt, &rv, &t.CreatedBy, &t.UpdatedBy); err != nil {
			return nil, "", err
		}
		t.TenantID, t.ID, t.ApplicationID, t.CategoryID = tID.String(), id.String(), aID.String(), cID.String()
		t.RowVersion = uint64(rv)
		_ = json.Unmarshal(tagsJSON, &t.Tags)
		if t.Tags == nil {
			t.Tags = []string{}
		}
		out = append(out, t)
	}
	next := ""
	if len(out) > pageSize {
		out = out[:pageSize]
		next = strconv.Itoa(offset + pageSize)
	}
	return out, next, rows.Err()
}

func (s *Store) ListTemplateTags(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT tag
		FROM templates, jsonb_array_elements_text(tags) AS tag
		WHERE tenant_id = $1 AND NOT is_deleted
		ORDER BY tag ASC
	`, parseUUID(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

func (s *Store) CountTemplateDeliveriesSince(ctx context.Context, tenantID, templateID string, since time.Time) (uint64, error) {
	var cnt int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM deliveries d
		JOIN notification_requests nr ON nr.tenant_id = d.tenant_id AND nr.id = d.request_id
		WHERE d.tenant_id = $1 AND nr.template_id = $2 AND d.created_at >= $3 AND NOT d.is_deleted
	`, parseUUID(tenantID), parseUUID(templateID), since.UTC()).Scan(&cnt)
	return uint64(cnt), err
}

func (s *Store) SaveTemplateVersion(ctx context.Context, tv application.TemplateVersion, expectedRowVersion uint64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tUUID := parseUUID(tv.TenantID)
	vUUID := parseUUID(tv.ID)

	var currentRV int64
	var currentStatus string
	err = tx.QueryRow(ctx, `SELECT row_version, status FROM template_versions WHERE tenant_id = $1 AND id = $2`, tUUID, vUUID).Scan(&currentRV, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO template_versions (
				tenant_id, id, template_id, version, schema_major, syntax, layout_version_id,
				variable_schema, content_hash, source_commit, change_note, approval_ref, status, published_at,
				created_at, updated_at, row_version, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,1,$17,$18)
		`, tUUID, vUUID, parseUUID(tv.TemplateID), int64(tv.Version), int(tv.SchemaMajor),
			tv.Syntax, nullableUUID(tv.LayoutVersionID), tv.VariableSchemaJSON, tv.ContentHash,
			tv.SourceCommit, tv.ChangeNote, tv.ApprovalRef, tv.Status, nullableTime(tv.PublishedAt),
			tv.CreatedAt, tv.UpdatedAt, tv.CreatedBy, tv.UpdatedBy)
		if err != nil {
			return mapPGError(err)
		}
		for _, v := range tv.Variants {
			pm := v.ParamMappingJSON
			if len(pm) == 0 {
				pm = []byte(`null`)
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO template_variants (
					tenant_id, id, template_version_id, channel, locale, variant_kind,
					subject_tmpl, html_tmpl, text_tmpl, mjml_source, provider_template_id, param_mapping,
					variant_hash, created_at, updated_at, row_version, created_by, updated_by
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,1,$16,$17)
			`, tUUID, parseUUID(v.ID), vUUID, v.Channel, v.Locale, v.VariantKind,
				v.SubjectTmpl, v.HTMLTmpl, v.TextTmpl, v.MJMLSource, v.ProviderTemplateID, pm,
				v.VariantHash, tv.CreatedAt, tv.UpdatedAt, tv.CreatedBy, tv.UpdatedBy)
			if err != nil {
				return mapPGError(err)
			}
		}
	} else if err != nil {
		return mapPGError(err)
	} else {
		if expectedRowVersion > 0 && uint64(currentRV) != expectedRowVersion {
			return application.ErrConflict
		}
		if currentStatus == "DRAFT" || currentStatus == "IN_REVIEW" {
			if _, err = tx.Exec(ctx, `DELETE FROM template_variants WHERE tenant_id = $1 AND template_version_id = $2`, tUUID, vUUID); err != nil {
				return mapPGError(err)
			}
			for _, v := range tv.Variants {
				pm := v.ParamMappingJSON
				if len(pm) == 0 {
					pm = []byte(`null`)
				}
				_, err = tx.Exec(ctx, `
					INSERT INTO template_variants (
						tenant_id, id, template_version_id, channel, locale, variant_kind,
						subject_tmpl, html_tmpl, text_tmpl, mjml_source, provider_template_id, param_mapping,
						variant_hash, created_at, updated_at, row_version, created_by, updated_by
					) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,1,$16,$17)
				`, tUUID, parseUUID(v.ID), vUUID, v.Channel, v.Locale, v.VariantKind,
					v.SubjectTmpl, v.HTMLTmpl, v.TextTmpl, v.MJMLSource, v.ProviderTemplateID, pm,
					v.VariantHash, tv.CreatedAt, tv.UpdatedAt, tv.CreatedBy, tv.UpdatedBy)
				if err != nil {
					return mapPGError(err)
				}
			}
		}
		_, err = tx.Exec(ctx, `
			UPDATE template_versions SET
				schema_major = $3, syntax = $4, layout_version_id = $5,
				variable_schema = $6, content_hash = $7, source_commit = $8, change_note = $9,
				approval_ref = $10, status = $11, published_at = $12,
				updated_at = $13, updated_by = $14, row_version = row_version + 1
			WHERE tenant_id = $1 AND id = $2
		`, tUUID, vUUID, int(tv.SchemaMajor), tv.Syntax, nullableUUID(tv.LayoutVersionID),
			tv.VariableSchemaJSON, tv.ContentHash, tv.SourceCommit, tv.ChangeNote, tv.ApprovalRef,
			tv.Status, nullableTime(tv.PublishedAt), tv.UpdatedAt, tv.UpdatedBy)
		if err != nil {
			return mapPGError(err)
		}
	}
	return mapPGError(tx.Commit(ctx))
}

func (s *Store) GetTemplateVersion(ctx context.Context, tenantID, versionID string) (*application.TemplateVersion, error) {
	var tv application.TemplateVersion
	var tID, vID, tmplID uuid.UUID
	var lvID *uuid.UUID
	var ver, rv int64
	var major int
	var pubAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, template_id, version, schema_major, syntax, layout_version_id,
		       variable_schema, content_hash, COALESCE(source_commit,''), COALESCE(change_note,''), COALESCE(approval_ref,''),
		       status, published_at, created_at, updated_at, row_version, created_by, updated_by
		FROM template_versions WHERE tenant_id = $1 AND id = $2 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(versionID)).Scan(
		&tID, &vID, &tmplID, &ver, &major, &tv.Syntax, &lvID,
		&tv.VariableSchemaJSON, &tv.ContentHash, &tv.SourceCommit, &tv.ChangeNote, &tv.ApprovalRef,
		&tv.Status, &pubAt, &tv.CreatedAt, &tv.UpdatedAt, &rv, &tv.CreatedBy, &tv.UpdatedBy,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	tv.TenantID, tv.ID, tv.TemplateID = tID.String(), vID.String(), tmplID.String()
	tv.LayoutVersionID = derefUUID(lvID)
	tv.Version, tv.SchemaMajor, tv.RowVersion = uint64(ver), uint32(major), uint64(rv)
	tv.PublishedAt = derefTime(pubAt)

	vRows, err := s.pool.Query(ctx, `
		SELECT id, channel, locale, variant_kind, COALESCE(subject_tmpl,''), COALESCE(html_tmpl,''),
		       COALESCE(text_tmpl,''), COALESCE(mjml_source,''), COALESCE(provider_template_id,''),
		       param_mapping, variant_hash, created_at, updated_at, row_version
		FROM template_variants WHERE tenant_id = $1 AND template_version_id = $2 AND NOT is_deleted
		ORDER BY channel ASC, locale ASC
	`, tID, vID)
	if err != nil {
		return nil, err
	}
	defer vRows.Close()
	for vRows.Next() {
		var v application.TemplateVariant
		var vid uuid.UUID
		var vrv int64
		if err := vRows.Scan(&vid, &v.Channel, &v.Locale, &v.VariantKind, &v.SubjectTmpl, &v.HTMLTmpl,
			&v.TextTmpl, &v.MJMLSource, &v.ProviderTemplateID, &v.ParamMappingJSON, &v.VariantHash,
			&v.CreatedAt, &v.UpdatedAt, &vrv); err != nil {
			return nil, err
		}
		v.TenantID, v.ID, v.TemplateVersionID, v.RowVersion = tv.TenantID, vid.String(), tv.ID, uint64(vrv)
		tv.Variants = append(tv.Variants, v)
	}
	return &tv, vRows.Err()
}

func (s *Store) ListTemplateVersions(ctx context.Context, tenantID, templateID string) ([]application.TemplateVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM template_versions WHERE tenant_id = $1 AND template_id = $2 AND NOT is_deleted ORDER BY version ASC
	`, parseUUID(tenantID), parseUUID(templateID))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id.String())
	}
	rows.Close()
	var out []application.TemplateVersion
	for _, id := range ids {
		tv, err := s.GetTemplateVersion(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *tv)
	}
	return out, nil
}

func (s *Store) SaveTemplateRelease(ctx context.Context, rel application.TemplateRelease) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tUUID := parseUUID(rel.TenantID)
	tmplUUID := parseUUID(rel.TemplateID)
	if rel.Status == "ACTIVE" {
		if _, err := tx.Exec(ctx, `
			UPDATE template_releases SET status = 'SUPERSEDED', updated_at = $4, row_version = row_version + 1
			WHERE tenant_id = $1 AND template_id = $2 AND schema_major = $3 AND status = 'ACTIVE' AND NOT is_deleted
		`, tUUID, tmplUUID, int(rel.SchemaMajor), rel.UpdatedAt); err != nil {
			return mapPGError(err)
		}
	}
	var prevOrd *int64
	if rel.PreviousOrdinal > 0 {
		po := int64(rel.PreviousOrdinal)
		prevOrd = &po
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO template_releases (
			tenant_id, id, template_id, schema_major, template_version_id, ordinal, previous_ordinal,
			activate_at, activated_at, approval_ref, status, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,$14)
	`, tUUID, parseUUID(rel.ID), tmplUUID, int(rel.SchemaMajor), parseUUID(rel.TemplateVersionID),
		int64(rel.Ordinal), prevOrd, rel.ActivateAt, nullableTime(rel.ActivatedAt), rel.ApprovalRef,
		rel.Status, rel.CreatedAt, rel.UpdatedAt, rel.CreatedBy)
	if err != nil {
		return mapPGError(err)
	}
	return mapPGError(tx.Commit(ctx))
}

func (s *Store) GetActiveTemplateRelease(ctx context.Context, tenantID, templateID string, schemaMajor uint32, now time.Time) (*application.TemplateRelease, error) {
	var r application.TemplateRelease
	var tID, rID, tmplID, tvID uuid.UUID
	var major int
	var ord, rv int64
	var prevOrd *int64
	var actAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, id, template_id, schema_major, template_version_id, ordinal, previous_ordinal,
		       activate_at, activated_at, COALESCE(approval_ref,''), status, created_at, updated_at, row_version, created_by
		FROM template_releases
		WHERE tenant_id = $1 AND template_id = $2 AND schema_major = $3
		  AND status = 'ACTIVE' AND activate_at <= $4 AND NOT is_deleted
		ORDER BY ordinal DESC LIMIT 1
	`, parseUUID(tenantID), parseUUID(templateID), int(schemaMajor), now.UTC()).Scan(
		&tID, &rID, &tmplID, &major, &tvID, &ord, &prevOrd, &r.ActivateAt, &actAt, &r.ApprovalRef,
		&r.Status, &r.CreatedAt, &r.UpdatedAt, &rv, &r.CreatedBy,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	r.TenantID, r.ID, r.TemplateID, r.TemplateVersionID = tID.String(), rID.String(), tmplID.String(), tvID.String()
	r.SchemaMajor, r.Ordinal, r.RowVersion = uint32(major), uint64(ord), uint64(rv)
	if prevOrd != nil {
		r.PreviousOrdinal = uint64(*prevOrd)
	}
	r.ActivatedAt = derefTime(actAt)
	return &r, nil
}

func (s *Store) ListTemplateReleases(ctx context.Context, tenantID, templateID string) ([]application.TemplateRelease, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, id, template_id, schema_major, template_version_id, ordinal, previous_ordinal,
		       activate_at, activated_at, COALESCE(approval_ref,''), status, created_at, updated_at, row_version, created_by
		FROM template_releases WHERE tenant_id = $1 AND template_id = $2 AND NOT is_deleted ORDER BY ordinal DESC
	`, parseUUID(tenantID), parseUUID(templateID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.TemplateRelease
	for rows.Next() {
		var r application.TemplateRelease
		var tID, rID, tmplID, tvID uuid.UUID
		var major int
		var ord, rv int64
		var prevOrd *int64
		var actAt *time.Time
		if err := rows.Scan(&tID, &rID, &tmplID, &major, &tvID, &ord, &prevOrd, &r.ActivateAt, &actAt,
			&r.ApprovalRef, &r.Status, &r.CreatedAt, &r.UpdatedAt, &rv, &r.CreatedBy); err != nil {
			return nil, err
		}
		r.TenantID, r.ID, r.TemplateID, r.TemplateVersionID = tID.String(), rID.String(), tmplID.String(), tvID.String()
		r.SchemaMajor, r.Ordinal, r.RowVersion = uint32(major), uint64(ord), uint64(rv)
		if prevOrd != nil {
			r.PreviousOrdinal = uint64(*prevOrd)
		}
		r.ActivatedAt = derefTime(actAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ActivateDueScheduledReleases(ctx context.Context, now time.Time) ([]application.TemplateRelease, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE template_releases
		SET status = 'ACTIVE', activated_at = $1, updated_at = $1, row_version = row_version + 1
		WHERE status = 'SCHEDULED' AND activate_at <= $1 AND NOT is_deleted
		RETURNING tenant_id, id, template_id, schema_major, template_version_id, ordinal
	`, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.TemplateRelease
	for rows.Next() {
		var r application.TemplateRelease
		var tID, id, tmplID, tvID uuid.UUID
		var major int
		var ord int64
		if err := rows.Scan(&tID, &id, &tmplID, &major, &tvID, &ord); err != nil {
			return nil, err
		}
		r.TenantID, r.ID, r.TemplateID, r.TemplateVersionID = tID.String(), id.String(), tmplID.String(), tvID.String()
		r.SchemaMajor, r.Ordinal, r.Status = uint32(major), uint64(ord), "ACTIVE"
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SaveProviderProfile(ctx context.Context, p application.ProviderProfile) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tUUID := parseUUID(p.TenantID)
	pUUID := parseUUID(p.ID)
	_, err = tx.Exec(ctx, `
		INSERT INTO provider_profiles (
			tenant_id, id, provider_key, name, channel, driver_kind, preset_key,
			active_ordinal, operational_state, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,'system','system')
		ON CONFLICT (tenant_id, id) DO UPDATE SET
			active_ordinal = EXCLUDED.active_ordinal, operational_state = EXCLUDED.operational_state,
			updated_at = EXCLUDED.updated_at, row_version = provider_profiles.row_version + 1
	`, tUUID, pUUID, p.ProviderKey, p.Name, p.Channel, p.DriverKind, p.PresetKey,
		int64(p.ActiveOrdinal), p.OperationalState, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return mapPGError(err)
	}
	if p.ActiveRevision != nil {
		rev := p.ActiveRevision
		idJSON, _ := json.Marshal(rev.SenderIdentity)
		_, err = tx.Exec(ctx, `
			INSERT INTO provider_revisions (
				tenant_id, id, provider_id, revision, config, credentials_ref, sender_identity,
				capabilities, quota_config, config_hash, status, published_at,
				created_at, updated_at, row_version, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,'{}','{}',$8,$9,$10,$11,$11,1,'system','system')
			ON CONFLICT (tenant_id, id) DO NOTHING
		`, tUUID, parseUUID(rev.ID), pUUID, int64(rev.Revision), rev.ConfigJSON, rev.CredentialsRef,
			idJSON, rev.ConfigHash, rev.Status, nullableTime(rev.PublishedAt), p.CreatedAt)
		if err != nil {
			return mapPGError(err)
		}
	}
	return mapPGError(tx.Commit(ctx))
}

func (s *Store) GetProviderProfile(ctx context.Context, tenantID, id string) (*application.ProviderProfile, error) {
	list, err := s.ListProviderProfiles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.ID == id {
			cp := p
			return &cp, nil
		}
	}
	return nil, application.ErrNotFound
}

func (s *Store) ListProviderProfiles(ctx context.Context, tenantID string) ([]application.ProviderProfile, error) {
	tUUID := parseUUID(tenantID)
	rows, err := s.pool.Query(ctx, `
		SELECT id, provider_key, name, channel, driver_kind, COALESCE(preset_key,''), active_ordinal, operational_state, created_at, updated_at, row_version
		FROM provider_profiles WHERE tenant_id = $1 AND NOT is_deleted ORDER BY provider_key ASC
	`, tUUID)
	if err != nil {
		return nil, err
	}
	var out []application.ProviderProfile
	for rows.Next() {
		var p application.ProviderProfile
		var pID uuid.UUID
		var ord, rv int64
		if err := rows.Scan(&pID, &p.ProviderKey, &p.Name, &p.Channel, &p.DriverKind, &p.PresetKey, &ord, &p.OperationalState, &p.CreatedAt, &p.UpdatedAt, &rv); err != nil {
			rows.Close()
			return nil, err
		}
		p.TenantID, p.ID, p.ActiveOrdinal, p.RowVersion = tenantID, pID.String(), uint64(ord), uint64(rv)
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		var rev application.ProviderRevision
		var revID uuid.UUID
		var revNo int64
		var senderJSON []byte
		var pubAt *time.Time
		err := s.pool.QueryRow(ctx, `
			SELECT id, revision, config, COALESCE(credentials_ref,''), sender_identity, config_hash, status, published_at
			FROM provider_revisions WHERE tenant_id = $1 AND provider_id = $2 AND status = 'PUBLISHED' AND NOT is_deleted
			ORDER BY revision DESC LIMIT 1
		`, tUUID, parseUUID(out[i].ID)).Scan(&revID, &revNo, &rev.ConfigJSON, &rev.CredentialsRef, &senderJSON, &rev.ConfigHash, &rev.Status, &pubAt)
		if err == nil {
			rev.TenantID, rev.ID, rev.ProviderID, rev.Revision = tenantID, revID.String(), out[i].ID, uint64(revNo)
			rev.PublishedAt = derefTime(pubAt)
			_ = json.Unmarshal(senderJSON, &rev.SenderIdentity)
			out[i].ActiveRevision = &rev
		}
	}
	return out, nil
}

func (s *Store) SaveRoutingPolicy(ctx context.Context, rp application.RoutingPolicy) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tUUID := parseUUID(rp.TenantID)
	rpUUID := parseUUID(rp.ID)
	now := rp.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO routing_policies (
			tenant_id, id, policy_key, application_id, category_id, channel, active_ordinal, status,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,1,$7,$8,$8,1,'system','system')
	`, tUUID, rpUUID, rp.PolicyKey, nullableUUID(rp.ApplicationID), nullableUUID(rp.CategoryID), rp.Channel, rp.Status, now)
	if err != nil {
		return mapPGError(err)
	}
	routesJSON, _ := json.Marshal(rp.Tiers)
	_, err = tx.Exec(ctx, `
		INSERT INTO routing_policy_versions (
			tenant_id, id, routing_policy_id, version, routes, failover_policy, digest, status, published_at,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,1,$4,'{}','v1','PUBLISHED',$5,$5,$5,1,'system','system')
	`, tUUID, uuid.New(), rpUUID, routesJSON, now)
	if err != nil {
		return mapPGError(err)
	}
	return mapPGError(tx.Commit(ctx))
}

func (s *Store) ListRoutingPolicies(ctx context.Context, tenantID, channel string) ([]application.RoutingPolicy, error) {
	tUUID := parseUUID(tenantID)
	rows, err := s.pool.Query(ctx, `
		SELECT rp.id, rp.policy_key, rp.application_id, rp.category_id, rp.channel, rp.status, rpv.routes
		FROM routing_policies rp
		JOIN routing_policy_versions rpv ON rpv.tenant_id = rp.tenant_id AND rpv.routing_policy_id = rp.id
		WHERE rp.tenant_id = $1 AND ($2 = '' OR rp.channel = $2) AND NOT rp.is_deleted AND rpv.status = 'PUBLISHED'
	`, tUUID, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.RoutingPolicy
	for rows.Next() {
		var rp application.RoutingPolicy
		var id uuid.UUID
		var appID, catID *uuid.UUID
		var routesJSON []byte
		if err := rows.Scan(&id, &rp.PolicyKey, &appID, &catID, &rp.Channel, &rp.Status, &routesJSON); err != nil {
			return nil, err
		}
		rp.TenantID, rp.ID = tenantID, id.String()
		rp.ApplicationID, rp.CategoryID = derefUUID(appID), derefUUID(catID)
		_ = json.Unmarshal(routesJSON, &rp.Tiers)
		out = append(out, rp)
	}
	return out, rows.Err()
}

func (s *Store) SaveBreakerOverride(ctx context.Context, ov application.BreakerOverrideRecord) error {
	tUUID := parseUUID(ov.TenantID)
	pUUID := parseUUID(ov.ProviderID)
	_, _ = s.pool.Exec(ctx, `
		UPDATE breaker_overrides SET revoked_at = $3, status = 'SUPERSEDED', updated_at = $3
		WHERE tenant_id = $1 AND provider_id = $2 AND revoked_at IS NULL
	`, tUUID, pUUID, ov.CreatedAt)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO breaker_overrides (
			tenant_id, id, provider_id, override_mode, reason, ordinal, expires_at, status,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,1,$10,$10)
	`, tUUID, parseUUID(ov.ID), pUUID, ov.OverrideMode, ov.Reason, int64(ov.Ordinal), ov.ExpiresAt, ov.Status, ov.CreatedAt, ov.CreatedBy)
	return mapPGError(err)
}

func (s *Store) GetActiveBreakerOverride(ctx context.Context, tenantID, providerID string, now time.Time) (*application.BreakerOverrideRecord, error) {
	var ov application.BreakerOverrideRecord
	var id uuid.UUID
	var ord int64
	err := s.pool.QueryRow(ctx, `
		SELECT id, override_mode, reason, ordinal, expires_at, status, created_at, created_by
		FROM breaker_overrides
		WHERE tenant_id = $1 AND provider_id = $2 AND revoked_at IS NULL AND expires_at > $3 AND NOT is_deleted
		ORDER BY ordinal DESC LIMIT 1
	`, parseUUID(tenantID), parseUUID(providerID), now.UTC()).Scan(
		&id, &ov.OverrideMode, &ov.Reason, &ord, &ov.ExpiresAt, &ov.Status, &ov.CreatedAt, &ov.CreatedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ov.TenantID, ov.ID, ov.ProviderID, ov.Ordinal = tenantID, id.String(), providerID, uint64(ord)
	return &ov, nil
}

func (s *Store) SaveSubjectContact(ctx context.Context, c application.SubjectContact) error {
	now := c.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO subject_contacts (
			tenant_id, id, subject_ref, channel, encrypted_address, address_hash, masked_address,
			locale, timezone, verified, source_version, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$11,1,'system','system')
	`, parseUUID(c.TenantID), parseUUID(c.ID), parseUUID(c.SubjectRef), c.Channel, c.EncryptedAddress,
		c.AddressHash, c.MaskedAddress, c.Locale, c.Timezone, c.Verified, now)
	return mapPGError(err)
}

func (s *Store) GetSubjectContact(ctx context.Context, tenantID, subjectRef, channel string) (*application.SubjectContact, error) {
	var c application.SubjectContact
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, encrypted_address, address_hash, masked_address, COALESCE(locale,''), COALESCE(timezone,''), verified, created_at
		FROM subject_contacts
		WHERE tenant_id = $1 AND subject_ref = $2 AND channel = $3 AND NOT is_deleted
		ORDER BY source_version DESC LIMIT 1
	`, parseUUID(tenantID), parseUUID(subjectRef), channel).Scan(
		&id, &c.EncryptedAddress, &c.AddressHash, &c.MaskedAddress, &c.Locale, &c.Timezone, &c.Verified, &c.CreatedAt,
	)
	if err != nil {
		return nil, mapPGError(err)
	}
	c.TenantID, c.ID, c.SubjectRef, c.Channel = tenantID, id.String(), subjectRef, channel
	return &c, nil
}

func (s *Store) ListPreferences(ctx context.Context, tenantID, subjectRef string) ([]application.Preference, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, category_id, channel, enabled
		FROM recipient_preferences
		WHERE tenant_id = $1 AND subject_ref = $2 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(subjectRef))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.Preference
	for rows.Next() {
		var p application.Preference
		var id, catID uuid.UUID
		if err := rows.Scan(&id, &catID, &p.Channel, &p.Enabled); err != nil {
			return nil, err
		}
		p.TenantID, p.ID, p.SubjectRef, p.CategoryID = tenantID, id.String(), subjectRef, catID.String()
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpsertPreference(ctx context.Context, p application.Preference) error {
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO recipient_preferences (
			tenant_id, id, subject_ref, category_id, channel, enabled,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$7,1,$8,$8)
		ON CONFLICT (tenant_id, subject_ref, category_id, channel) WHERE NOT is_deleted
		DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at, row_version = recipient_preferences.row_version + 1
	`, parseUUID(p.TenantID), parseUUID(p.ID), parseUUID(p.SubjectRef), parseUUID(p.CategoryID), p.Channel, p.Enabled, now, p.SubjectRef)
	return mapPGError(err)
}

func (s *Store) IsSuppressed(ctx context.Context, tenantID, channel, addressHash string, now time.Time) (bool, error) {
	var cnt int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM suppressions
		WHERE tenant_id = $1 AND channel = $2 AND address_hash = $3 AND status = 'ACTIVE'
		  AND (expires_at IS NULL OR expires_at > $4) AND NOT is_deleted
	`, parseUUID(tenantID), channel, addressHash, now.UTC()).Scan(&cnt)
	return cnt > 0, err
}

func (s *Store) SaveSuppression(ctx context.Context, sup application.Suppression) error {
	now := sup.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO suppressions (
			tenant_id, id, channel, address_hash, masked_address, reason, expires_at, status,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,1,'system','system')
	`, parseUUID(sup.TenantID), parseUUID(sup.ID), sup.Channel, sup.AddressHash, sup.MaskedAddress,
		sup.Reason, nullableTime(sup.ExpiresAt), sup.Status, now)
	return mapPGError(err)
}

func (s *Store) SaveInAppMessage(ctx context.Context, m application.InAppMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inapp_messages (
			tenant_id, id, delivery_id, subject_ref, category_key, title, body_html, action_url,
			expires_at, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,1,'inapp','inapp')
	`, parseUUID(m.TenantID), parseUUID(m.ID), parseUUID(m.DeliveryID), parseUUID(m.SubjectRef),
		m.CategoryKey, m.Title, m.BodyHTML, m.ActionURL, m.ExpiresAt, m.CreatedAt)
	return mapPGError(err)
}

func (s *Store) ListInAppMessages(ctx context.Context, tenantID, subjectRef string, unreadOnly bool, pageSize int, pageToken string) ([]application.InAppMessage, string, uint32, error) {
	tUUID := parseUUID(tenantID)
	sUUID := parseUUID(subjectRef)
	var unread uint32
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM inapp_messages
		WHERE tenant_id = $1 AND subject_ref = $2 AND read_at IS NULL AND archived_at IS NULL AND NOT is_deleted
	`, tUUID, sUUID).Scan(&unread)

	if pageSize <= 0 {
		pageSize = 25
	}
	offset := 0
	if pageToken != "" {
		if n, err := strconv.Atoi(pageToken); err == nil && n >= 0 {
			offset = n
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, delivery_id, category_key, title, body_html, COALESCE(action_url,''), read_at, expires_at, created_at
		FROM inapp_messages
		WHERE tenant_id = $1 AND subject_ref = $2 AND archived_at IS NULL AND NOT is_deleted
		  AND (NOT $3 OR read_at IS NULL)
		ORDER BY created_at DESC, id DESC
		LIMIT $4 OFFSET $5
	`, tUUID, sUUID, unreadOnly, pageSize+1, offset)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()
	var out []application.InAppMessage
	for rows.Next() {
		var m application.InAppMessage
		var id, delID uuid.UUID
		var readAt *time.Time
		if err := rows.Scan(&id, &delID, &m.CategoryKey, &m.Title, &m.BodyHTML, &m.ActionURL, &readAt, &m.ExpiresAt, &m.CreatedAt); err != nil {
			return nil, "", 0, err
		}
		m.TenantID, m.ID, m.DeliveryID, m.SubjectRef = tenantID, id.String(), delID.String(), subjectRef
		m.ReadAt = derefTime(readAt)
		out = append(out, m)
	}
	next := ""
	if len(out) > pageSize {
		out = out[:pageSize]
		next = strconv.Itoa(offset + pageSize)
	}
	return out, next, unread, rows.Err()
}

func (s *Store) MarkInAppMessagesRead(ctx context.Context, tenantID, subjectRef string, ids []string, now time.Time) (uint32, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	uuids := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		uuids = append(uuids, parseUUID(id))
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE inapp_messages
		SET read_at = $4, updated_at = $4, row_version = row_version + 1
		WHERE tenant_id = $1 AND subject_ref = $2 AND id = ANY($3) AND read_at IS NULL AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(subjectRef), uuids, now.UTC())
	if err != nil {
		return 0, err
	}
	return uint32(tag.RowsAffected()), nil
}

func (s *Store) FindRequestByIdempotency(ctx context.Context, tenantID, appID, idempotencyKey string) (*application.NotificationRequestRecord, []application.Delivery, bool, error) {
	var reqID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM notification_requests
		WHERE tenant_id = $1 AND application_id = $2 AND idempotency_key = $3 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(appID), idempotencyKey).Scan(&reqID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	req, dels, err := s.GetNotificationRequest(ctx, tenantID, reqID.String())
	if err != nil {
		return nil, nil, false, err
	}
	return req, dels, true, nil
}

func (s *Store) CreateRequestWithDeliveries(ctx context.Context, req application.NotificationRequestRecord, deliveries []application.Delivery, payloads []application.DeliveryPayload, outbox []application.OutboxMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if req.ID != "" {
		var pin *int64
		if req.PinnedVersion > 0 {
			p := int64(req.PinnedVersion)
			pin = &p
		}
		prio := req.Priority
		if prio < 1 || prio > 3 {
			prio = 2
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO notification_requests (
				tenant_id, id, application_id, idempotency_key, request_digest, template_id, schema_major,
				pinned_version, category_id, lane, priority, correlation_id, is_test, send_at, expires_at, status,
				created_at, updated_at, row_version, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17,1,$18,$18)
		`, parseUUID(req.TenantID), parseUUID(req.ID), parseUUID(req.ApplicationID), req.IdempotencyKey,
			req.RequestDigest, parseUUID(req.TemplateID), int(req.SchemaMajor), pin, parseUUID(req.CategoryID),
			req.Lane, prio, req.CorrelationID, req.IsTest, nullableTime(req.SendAt), nullableTime(req.ExpiresAt),
			req.Status, req.CreatedAt, req.CreatedBy)
		if err != nil {
			return mapPGError(err)
		}
	}
	for _, d := range deliveries {
		_, err = tx.Exec(ctx, `
			INSERT INTO deliveries (
				tenant_id, id, request_id, template_version_id, variant_id, release_ordinal,
				recipient_subject_ref, address_hash, masked_address, channel, lane, status,
				selected_provider_id, provider_message_id, content_hash, resend_of_delivery_id,
				attempt_count, next_attempt_at, expires_at, completed_at,
				created_at, updated_at, row_version, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$21,1,'system','system')
		`, parseUUID(d.TenantID), parseUUID(d.ID), parseUUID(d.RequestID), parseUUID(d.TemplateVersionID),
			parseUUID(d.VariantID), int64(d.ReleaseOrdinal), nullableUUID(d.RecipientSubjectRef),
			d.AddressHash, d.MaskedAddress, d.Channel, d.Lane, d.Status,
			nullableUUID(d.SelectedProviderID), d.ProviderMessageID, d.ContentHash, nullableUUID(d.ResendOfDeliveryID),
			d.AttemptCount, nullableTime(d.NextAttemptAt), nullableTime(d.ExpiresAt), nullableTime(d.CompletedAt), d.CreatedAt)
		if err != nil {
			return mapPGError(err)
		}
	}
	for _, p := range payloads {
		_, err = tx.Exec(ctx, `
			INSERT INTO delivery_payloads (
				tenant_id, id, delivery_id, encrypted_address, encrypted_data, encrypted_secrets,
				rendered_copy_encrypted, key_ref, secrets_erased_at, expires_at,
				created_at, updated_at, row_version, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,1,'system','system')
		`, parseUUID(p.TenantID), parseUUID(p.ID), parseUUID(p.DeliveryID), p.EncryptedAddress,
			p.EncryptedData, p.EncryptedSecrets, p.RenderedCopyEncrypted, p.KeyRef,
			nullableTime(p.SecretsErasedAt), p.ExpiresAt, p.CreatedAt)
		if err != nil {
			return mapPGError(err)
		}
	}
	for _, o := range outbox {
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox (
				tenant_id, id, event_id, aggregate_id, aggregate_version, event_type, payload, attempts,
				created_at, updated_at, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$8,'system','system')
		`, parseUUID(o.TenantID), parseUUID(o.ID), parseUUID(o.EventID), parseUUID(o.AggregateID),
			int64(o.AggregateVersion), o.EventType, o.PayloadJSON, o.CreatedAt)
		if err != nil {
			return mapPGError(err)
		}
	}
	return mapPGError(tx.Commit(ctx))
}

func (s *Store) GetNotificationRequest(ctx context.Context, tenantID, requestID string) (*application.NotificationRequestRecord, []application.Delivery, error) {
	var r application.NotificationRequestRecord
	var tID, rID, appID, tmplID, catID uuid.UUID
	var major int
	var sendAt, expAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT nr.tenant_id, nr.id, nr.application_id, nr.idempotency_key, nr.request_digest,
		       nr.template_id, t.template_key, nr.schema_major, nr.category_id, c.category_key,
		       nr.lane, COALESCE(nr.correlation_id,''), nr.is_test, nr.send_at, nr.expires_at,
		       nr.status, nr.created_at, nr.updated_at, nr.created_by
		FROM notification_requests nr
		JOIN templates t ON t.tenant_id = nr.tenant_id AND t.id = nr.template_id
		JOIN categories c ON c.tenant_id = nr.tenant_id AND c.id = nr.category_id
		WHERE nr.tenant_id = $1 AND nr.id = $2 AND NOT nr.is_deleted
	`, parseUUID(tenantID), parseUUID(requestID)).Scan(
		&tID, &rID, &appID, &r.IdempotencyKey, &r.RequestDigest, &tmplID, &r.TemplateKey,
		&major, &catID, &r.CategoryKey, &r.Lane, &r.CorrelationID, &r.IsTest, &sendAt, &expAt,
		&r.Status, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy,
	)
	if err != nil {
		return nil, nil, mapPGError(err)
	}
	r.TenantID, r.ID, r.ApplicationID, r.TemplateID, r.CategoryID = tID.String(), rID.String(), appID.String(), tmplID.String(), catID.String()
	r.SchemaMajor, r.SendAt, r.ExpiresAt = uint32(major), derefTime(sendAt), derefTime(expAt)

	rows, err := s.pool.Query(ctx, `
		SELECT id, template_version_id, variant_id, release_ordinal, recipient_subject_ref,
		       address_hash, masked_address, channel, lane, status, selected_provider_id,
		       COALESCE(provider_message_id,''), content_hash, resend_of_delivery_id,
		       attempt_count, next_attempt_at, expires_at, completed_at, created_at, updated_at
		FROM deliveries WHERE tenant_id = $1 AND request_id = $2 AND NOT is_deleted ORDER BY created_at ASC
	`, tID, rID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var dels []application.Delivery
	for rows.Next() {
		var d application.Delivery
		var dID, tvID, varID uuid.UUID
		var subjRef, provID, resendID *uuid.UUID
		var ord int64
		var nextAt, dExpAt, compAt *time.Time
		if err := rows.Scan(&dID, &tvID, &varID, &ord, &subjRef, &d.AddressHash, &d.MaskedAddress,
			&d.Channel, &d.Lane, &d.Status, &provID, &d.ProviderMessageID, &d.ContentHash, &resendID,
			&d.AttemptCount, &nextAt, &dExpAt, &compAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, nil, err
		}
		d.TenantID, d.ID, d.RequestID = tenantID, dID.String(), requestID
		d.ApplicationID, d.TemplateID, d.CorrelationID = r.ApplicationID, r.TemplateID, r.CorrelationID
		d.TemplateVersionID, d.VariantID, d.ReleaseOrdinal = tvID.String(), varID.String(), uint64(ord)
		d.RecipientSubjectRef, d.SelectedProviderID, d.ResendOfDeliveryID = derefUUID(subjRef), derefUUID(provID), derefUUID(resendID)
		d.NextAttemptAt, d.ExpiresAt, d.CompletedAt = derefTime(nextAt), derefTime(dExpAt), derefTime(compAt)
		dels = append(dels, d)
	}
	return &r, dels, rows.Err()
}

func (s *Store) GetDelivery(ctx context.Context, tenantID, deliveryID string) (*application.Delivery, *application.DeliveryPayload, error) {
	tUUID := parseUUID(tenantID)
	dUUID := parseUUID(deliveryID)
	var d application.Delivery
	var reqID, appID, tmplID, tvID, varID uuid.UUID
	var subjRef, provID, resendID *uuid.UUID
	var ord int64
	var nextAt, expAt, compAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT d.request_id, nr.application_id, nr.template_id, COALESCE(nr.correlation_id,''),
		       d.template_version_id, d.variant_id, d.release_ordinal, d.recipient_subject_ref,
		       d.address_hash, d.masked_address, d.channel, d.lane, d.status, d.selected_provider_id,
		       COALESCE(d.provider_message_id,''), d.content_hash, d.resend_of_delivery_id,
		       d.attempt_count, d.next_attempt_at, d.expires_at, d.completed_at, d.created_at, d.updated_at
		FROM deliveries d
		JOIN notification_requests nr ON nr.tenant_id = d.tenant_id AND nr.id = d.request_id
		WHERE d.tenant_id = $1 AND d.id = $2 AND NOT d.is_deleted
	`, tUUID, dUUID).Scan(
		&reqID, &appID, &tmplID, &d.CorrelationID, &tvID, &varID, &ord, &subjRef,
		&d.AddressHash, &d.MaskedAddress, &d.Channel, &d.Lane, &d.Status, &provID,
		&d.ProviderMessageID, &d.ContentHash, &resendID, &d.AttemptCount, &nextAt, &expAt, &compAt,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, nil, mapPGError(err)
	}
	d.TenantID, d.ID, d.RequestID = tenantID, deliveryID, reqID.String()
	d.ApplicationID, d.TemplateID = appID.String(), tmplID.String()
	d.TemplateVersionID, d.VariantID, d.ReleaseOrdinal = tvID.String(), varID.String(), uint64(ord)
	d.RecipientSubjectRef, d.SelectedProviderID, d.ResendOfDeliveryID = derefUUID(subjRef), derefUUID(provID), derefUUID(resendID)
	d.NextAttemptAt, d.ExpiresAt, d.CompletedAt = derefTime(nextAt), derefTime(expAt), derefTime(compAt)

	var p application.DeliveryPayload
	var pID uuid.UUID
	var erasedAt *time.Time
	perr := s.pool.QueryRow(ctx, `
		SELECT id, encrypted_address, encrypted_data, encrypted_secrets, rendered_copy_encrypted,
		       key_ref, secrets_erased_at, expires_at, created_at
		FROM delivery_payloads WHERE tenant_id = $1 AND delivery_id = $2 AND NOT is_deleted
	`, tUUID, dUUID).Scan(
		&pID, &p.EncryptedAddress, &p.EncryptedData, &p.EncryptedSecrets, &p.RenderedCopyEncrypted,
		&p.KeyRef, &erasedAt, &p.ExpiresAt, &p.CreatedAt,
	)
	if errors.Is(perr, pgx.ErrNoRows) {
		return &d, nil, nil
	}
	if perr != nil {
		return nil, nil, perr
	}
	p.TenantID, p.ID, p.DeliveryID, p.SecretsErasedAt = tenantID, pID.String(), deliveryID, derefTime(erasedAt)
	return &d, &p, nil
}

func (s *Store) UpdateDeliveryOutcome(ctx context.Context, d application.Delivery, eraseSecrets bool, erasedAt time.Time, attempts []application.DeliveryAttempt, outbox []application.OutboxMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tUUID := parseUUID(d.TenantID)
	dUUID := parseUUID(d.ID)

	_, err = tx.Exec(ctx, `
		UPDATE deliveries SET
			status = $3, selected_provider_id = $4, provider_message_id = $5,
			attempt_count = $6, next_attempt_at = $7, completed_at = $8,
			updated_at = $9, row_version = row_version + 1
		WHERE tenant_id = $1 AND id = $2
	`, tUUID, dUUID, d.Status, nullableUUID(d.SelectedProviderID), d.ProviderMessageID,
		d.AttemptCount, nullableTime(d.NextAttemptAt), nullableTime(d.CompletedAt), d.UpdatedAt)
	if err != nil {
		return mapPGError(err)
	}
	if eraseSecrets {
		_, err = tx.Exec(ctx, `
			UPDATE delivery_payloads SET
				encrypted_secrets = NULL, secrets_erased_at = $3, updated_at = $3, row_version = row_version + 1
			WHERE tenant_id = $1 AND delivery_id = $2
		`, tUUID, dUUID, erasedAt.UTC())
		if err != nil {
			return mapPGError(err)
		}
	}
	for _, a := range attempts {
		_, err = tx.Exec(ctx, `
			INSERT INTO delivery_attempts (
				tenant_id, id, delivery_id, provider_id, provider_revision_id, attempt_no,
				outcome_class, provider_status_code, provider_message_id, error_code, latency_ms,
				started_at, finished_at, created_at, updated_at, row_version, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,$13,1,'dispatcher','dispatcher')
		`, tUUID, parseUUID(a.ID), dUUID, parseUUID(a.ProviderID), parseUUID(a.ProviderRevisionID),
			a.AttemptNo, a.OutcomeClass, a.ProviderStatusCode, a.ProviderMessageID, a.ErrorCode,
			a.LatencyMS, a.StartedAt, a.FinishedAt)
		if err != nil {
			return mapPGError(err)
		}
	}
	for _, o := range outbox {
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox (
				tenant_id, id, event_id, aggregate_id, aggregate_version, event_type, payload, attempts,
				created_at, updated_at, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$8,'dispatcher','dispatcher')
		`, tUUID, parseUUID(o.ID), parseUUID(o.EventID), parseUUID(o.AggregateID),
			int64(o.AggregateVersion), o.EventType, o.PayloadJSON, o.CreatedAt)
		if err != nil {
			return mapPGError(err)
		}
	}
	return mapPGError(tx.Commit(ctx))
}

func (s *Store) QueryDeliveries(ctx context.Context, q application.DeliveryQuery) ([]application.Delivery, time.Time, string, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sql := `
		SELECT d.id, d.request_id, nr.application_id, nr.template_id, COALESCE(nr.correlation_id,''),
		       d.template_version_id, d.variant_id, d.release_ordinal, d.recipient_subject_ref,
		       d.address_hash, d.masked_address, d.channel, d.lane, d.status, d.selected_provider_id,
		       COALESCE(d.provider_message_id,''), d.content_hash, d.resend_of_delivery_id,
		       d.attempt_count, d.completed_at, d.created_at, d.updated_at
		FROM deliveries d
		JOIN notification_requests nr ON nr.tenant_id = d.tenant_id AND nr.id = d.request_id
		WHERE d.tenant_id = $1 AND NOT d.is_deleted`
	args := []any{parseUUID(q.TenantID)}
	idx := 2
	addFilter := func(cond string, val any) {
		sql += fmt.Sprintf(" AND "+cond, idx)
		args = append(args, val)
		idx++
	}
	if q.ApplicationID != "" {
		addFilter("nr.application_id = $%d", parseUUID(q.ApplicationID))
	}
	if q.TemplateID != "" {
		addFilter("nr.template_id = $%d", parseUUID(q.TemplateID))
	}
	if q.Channel != "" {
		addFilter("d.channel = $%d", q.Channel)
	}
	if q.Status != "" {
		addFilter("d.status = $%d", q.Status)
	}
	if q.CorrelationID != "" {
		addFilter("nr.correlation_id = $%d", q.CorrelationID)
	}
	if q.RecipientSubjectRef != "" {
		addFilter("d.recipient_subject_ref = $%d", parseUUID(q.RecipientSubjectRef))
	}
	if !q.BeforeCreatedAt.IsZero() && q.BeforeID != "" {
		sql += fmt.Sprintf(" AND (d.created_at, d.id) < ($%d, $%d)", idx, idx+1)
		args = append(args, q.BeforeCreatedAt.UTC(), parseUUID(q.BeforeID))
		idx += 2
	}
	sql += fmt.Sprintf(" ORDER BY d.created_at DESC, d.id DESC LIMIT $%d", idx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	defer rows.Close()
	var out []application.Delivery
	for rows.Next() {
		var d application.Delivery
		var id, reqID, appID, tmplID, tvID, varID uuid.UUID
		var subjRef, provID, resendID *uuid.UUID
		var ord int64
		var compAt *time.Time
		if err := rows.Scan(&id, &reqID, &appID, &tmplID, &d.CorrelationID, &tvID, &varID, &ord,
			&subjRef, &d.AddressHash, &d.MaskedAddress, &d.Channel, &d.Lane, &d.Status, &provID,
			&d.ProviderMessageID, &d.ContentHash, &resendID, &d.AttemptCount, &compAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, time.Time{}, "", err
		}
		d.TenantID, d.ID, d.RequestID = q.TenantID, id.String(), reqID.String()
		d.ApplicationID, d.TemplateID = appID.String(), tmplID.String()
		d.TemplateVersionID, d.VariantID, d.ReleaseOrdinal = tvID.String(), varID.String(), uint64(ord)
		d.RecipientSubjectRef, d.SelectedProviderID, d.ResendOfDeliveryID = derefUUID(subjRef), derefUUID(provID), derefUUID(resendID)
		d.CompletedAt = derefTime(compAt)
		out = append(out, d)
	}
	if len(out) <= limit {
		return out, time.Time{}, "", rows.Err()
	}
	page := out[:limit]
	last := page[len(page)-1]
	return page, last.CreatedAt, last.ID, rows.Err()
}

func (s *Store) AppendBreakerTransition(ctx context.Context, tr application.BreakerTransition) error {
	now := tr.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO breaker_transitions (
			tenant_id, id, provider_id, from_state, to_state, trigger_kind, reason, actor_ref,
			error_rate_bps, slow_call_rate_bps, occurred_at, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$11,1,'system','system')
	`, parseUUID(tr.TenantID), parseUUID(tr.ID), parseUUID(tr.ProviderID), tr.FromState, tr.ToState,
		tr.TriggerKind, tr.Reason, nullableUUID(tr.ActorRef), int(tr.ErrorRateBps), int(tr.SlowCallRateBps), now)
	return mapPGError(err)
}

func (s *Store) ListBreakerTransitions(ctx context.Context, tenantID, providerID string, limit int) ([]application.BreakerTransition, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, from_state, to_state, trigger_kind, COALESCE(reason,''), actor_ref,
		       error_rate_bps, slow_call_rate_bps, occurred_at
		FROM breaker_transitions
		WHERE tenant_id = $1 AND provider_id = $2
		ORDER BY occurred_at DESC, id DESC
		LIMIT $3
	`, parseUUID(tenantID), parseUUID(providerID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.BreakerTransition
	for rows.Next() {
		var tr application.BreakerTransition
		var id uuid.UUID
		var actRef *uuid.UUID
		var errBps, slowBps int
		if err := rows.Scan(&id, &tr.FromState, &tr.ToState, &tr.TriggerKind, &tr.Reason, &actRef, &errBps, &slowBps, &tr.OccurredAt); err != nil {
			return nil, err
		}
		tr.TenantID, tr.ID, tr.ProviderID = tenantID, id.String(), providerID
		tr.ActorRef = derefUUID(actRef)
		tr.ErrorRateBps, tr.SlowCallRateBps = uint32(errBps), uint32(slowBps)
		out = append(out, tr)
	}
	return out, rows.Err()
}

func (s *Store) CountProviderDeliveriesSince(ctx context.Context, tenantID, providerID string, since time.Time) (uint64, error) {
	var cnt int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM deliveries
		WHERE tenant_id = $1 AND selected_provider_id = $2 AND created_at >= $3 AND NOT is_deleted
	`, parseUUID(tenantID), parseUUID(providerID), since.UTC()).Scan(&cnt)
	return uint64(cnt), err
}

func (s *Store) ListDeliveryAttempts(ctx context.Context, tenantID, deliveryID string) ([]application.DeliveryAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, provider_id, provider_revision_id, attempt_no, outcome_class,
		       COALESCE(provider_status_code,''), COALESCE(provider_message_id,''), COALESCE(error_code,''),
		       latency_ms, started_at, finished_at
		FROM delivery_attempts
		WHERE tenant_id = $1 AND delivery_id = $2 AND NOT is_deleted
		ORDER BY attempt_no ASC
	`, parseUUID(tenantID), parseUUID(deliveryID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.DeliveryAttempt
	for rows.Next() {
		var a application.DeliveryAttempt
		var id, pID, revID uuid.UUID
		if err := rows.Scan(&id, &pID, &revID, &a.AttemptNo, &a.OutcomeClass,
			&a.ProviderStatusCode, &a.ProviderMessageID, &a.ErrorCode,
			&a.LatencyMS, &a.StartedAt, &a.FinishedAt); err != nil {
			return nil, err
		}
		a.TenantID, a.ID, a.DeliveryID, a.ProviderID, a.ProviderRevisionID = tenantID, id.String(), deliveryID, pID.String(), revID.String()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AppendDeliveryEvent(ctx context.Context, ev application.DeliveryEvent) error {
	now := ev.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var detailJSON []byte
	if len(ev.Detail) > 0 {
		detailJSON, _ = json.Marshal(ev.Detail)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO delivery_events (
			tenant_id, id, delivery_id, event_type, from_status, to_status, detail, occurred_at,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$8,1,'system','system')
	`, parseUUID(ev.TenantID), parseUUID(ev.ID), parseUUID(ev.DeliveryID), ev.EventType, ev.FromStatus, ev.ToStatus, detailJSON, now)
	return mapPGError(err)
}

func (s *Store) ListDeliveryEvents(ctx context.Context, tenantID, deliveryID string) ([]application.DeliveryEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, event_type, COALESCE(from_status,''), to_status, detail, occurred_at
		FROM delivery_events
		WHERE tenant_id = $1 AND delivery_id = $2 AND NOT is_deleted
		ORDER BY occurred_at ASC, id ASC
	`, parseUUID(tenantID), parseUUID(deliveryID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.DeliveryEvent
	for rows.Next() {
		var ev application.DeliveryEvent
		var id uuid.UUID
		var detailJSON []byte
		if err := rows.Scan(&id, &ev.EventType, &ev.FromStatus, &ev.ToStatus, &detailJSON, &ev.OccurredAt); err != nil {
			return nil, err
		}
		ev.TenantID, ev.ID, ev.DeliveryID = tenantID, id.String(), deliveryID
		if len(detailJSON) > 0 {
			_ = json.Unmarshal(detailJSON, &ev.Detail)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) SaveTriggerRule(ctx context.Context, rule application.TriggerRule) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trigger_rules (
			tenant_id, id, rule_key, name, workflow_type, node_id, trigger_kind, template_id, schema_major,
			active_ordinal, status, created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,$14)
		ON CONFLICT (tenant_id, id) DO UPDATE SET
			active_ordinal = EXCLUDED.active_ordinal, status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at, row_version = trigger_rules.row_version + 1
	`, parseUUID(rule.TenantID), parseUUID(rule.ID), rule.RuleKey, rule.Name, rule.WorkflowType,
		rule.NodeID, rule.TriggerKind, parseUUID(rule.TemplateID), int(rule.SchemaMajor),
		int64(rule.ActiveOrdinal), rule.Status, rule.CreatedAt, rule.UpdatedAt, rule.CreatedBy)
	return mapPGError(err)
}

func (s *Store) GetTriggerRule(ctx context.Context, tenantID, id string) (*application.TriggerRule, error) {
	list, err := s.ListTriggerRules(ctx, tenantID, "")
	if err != nil {
		return nil, err
	}
	for _, r := range list {
		if r.ID == id {
			cp := r
			return &cp, nil
		}
	}
	return nil, application.ErrNotFound
}

func (s *Store) ListTriggerRules(ctx context.Context, tenantID, workflowType string) ([]application.TriggerRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, rule_key, name, workflow_type, COALESCE(node_id,''), trigger_kind, template_id, schema_major,
		       active_ordinal, status, created_at, updated_at, created_by
		FROM trigger_rules
		WHERE tenant_id = $1 AND ($2 = '' OR workflow_type = $2) AND NOT is_deleted
		ORDER BY rule_key ASC
	`, parseUUID(tenantID), workflowType)
	if err != nil {
		return nil, err
	}
	var out []application.TriggerRule
	for rows.Next() {
		var r application.TriggerRule
		var id, tmplID uuid.UUID
		var major int
		var ord int64
		if err := rows.Scan(&id, &r.RuleKey, &r.Name, &r.WorkflowType, &r.NodeID, &r.TriggerKind,
			&tmplID, &major, &ord, &r.Status, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy); err != nil {
			rows.Close()
			return nil, err
		}
		r.TenantID, r.ID, r.TemplateID = tenantID, id.String(), tmplID.String()
		r.SchemaMajor, r.ActiveOrdinal = uint32(major), uint64(ord)
		out = append(out, r)
	}
	rows.Close()
	for i := range out {
		revs, err := s.ListTriggerRuleRevisions(ctx, tenantID, out[i].ID)
		if err == nil {
			for j := len(revs) - 1; j >= 0; j-- {
				if revs[j].Status == "PUBLISHED" {
					cp := revs[j]
					out[i].ActiveRevision = &cp
					break
				}
			}
		}
	}
	return out, nil
}

func (s *Store) SaveTriggerRuleRevision(ctx context.Context, rev application.TriggerRuleRevision) error {
	mapJSON, _ := json.Marshal(rev.DataMappingCEL)
	refJSON, _ := json.Marshal(rev.ReferencedVars)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trigger_rule_revisions (
			tenant_id, id, rule_id, revision, version_selector, condition_cel, recipient_spec,
			data_mapping_cel, referenced_vars, delay_seconds, dedup_window_seconds, digest, status, published_at,
			created_at, updated_at, row_version, created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,1,$17,$17)
		ON CONFLICT (tenant_id, id) DO UPDATE SET
			status = EXCLUDED.status, updated_at = EXCLUDED.updated_at, row_version = trigger_rule_revisions.row_version + 1
	`, parseUUID(rev.TenantID), parseUUID(rev.ID), parseUUID(rev.RuleID), int64(rev.Revision),
		rev.VersionSelector, rev.ConditionCEL, rev.RecipientSpecJSON, mapJSON, refJSON,
		rev.DelaySeconds, rev.DedupWindowSeconds, rev.Digest, rev.Status, nullableTime(rev.PublishedAt),
		rev.CreatedAt, rev.UpdatedAt, rev.CreatedBy)
	return mapPGError(err)
}

func (s *Store) ListTriggerRuleRevisions(ctx context.Context, tenantID, ruleID string) ([]application.TriggerRuleRevision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, revision, version_selector, COALESCE(condition_cel,''), recipient_spec,
		       data_mapping_cel, referenced_vars, delay_seconds, dedup_window_seconds, digest, status,
		       published_at, created_at, updated_at, created_by
		FROM trigger_rule_revisions
		WHERE tenant_id = $1 AND rule_id = $2 AND NOT is_deleted
		ORDER BY revision ASC
	`, parseUUID(tenantID), parseUUID(ruleID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.TriggerRuleRevision
	for rows.Next() {
		var r application.TriggerRuleRevision
		var id uuid.UUID
		var revNo int64
		var mapJSON, refJSON []byte
		var pubAt *time.Time
		if err := rows.Scan(&id, &revNo, &r.VersionSelector, &r.ConditionCEL, &r.RecipientSpecJSON,
			&mapJSON, &refJSON, &r.DelaySeconds, &r.DedupWindowSeconds, &r.Digest, &r.Status,
			&pubAt, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy); err != nil {
			return nil, err
		}
		r.TenantID, r.ID, r.RuleID, r.Revision = tenantID, id.String(), ruleID, uint64(revNo)
		r.PublishedAt = derefTime(pubAt)
		_ = json.Unmarshal(mapJSON, &r.DataMappingCEL)
		_ = json.Unmarshal(refJSON, &r.ReferencedVars)
		out = append(out, r)
	}
	return out, rows.Err()
}
