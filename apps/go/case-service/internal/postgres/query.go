package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) GetView(ctx context.Context, tenantID, caseID uuid.UUID) (application.CaseView, error) {
	var view application.CaseView
	err := s.pool.QueryRow(ctx, `SELECT c.tenant_id,c.id,c.customer_ref,c.workflow_type,c.workflow_version,
		c.business_status,c.orchestration_status,COALESCE(c.workflow_instance_id,''),c.row_version,
		c.created_at,c.updated_at,j.status,j.attempts,
		COALESCE((SELECT max(o.aggregate_version) FROM case_outbox o WHERE o.tenant_id=c.tenant_id
			AND o.aggregate_id=c.id AND o.published_at IS NOT NULL),0)
		FROM customer_cases c JOIN workflow_start_jobs j ON j.tenant_id=c.tenant_id AND j.case_id=c.id
		WHERE c.tenant_id=$1 AND c.id=$2 AND NOT c.is_deleted`, tenantID, caseID).Scan(
		&view.Case.TenantID, &view.Case.ID, &view.Case.CustomerRef, &view.Case.WorkflowType,
		&view.Case.WorkflowVersion, &view.Case.BusinessStatus, &view.Case.OrchestrationStatus,
		&view.Case.WorkflowInstanceID, &view.Case.Version, &view.Case.CreatedAt, &view.Case.UpdatedAt,
		&view.StartStatus, &view.StartAttempts, &view.PublishedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, application.ErrNotFound
	}
	view.AsOf = view.Case.UpdatedAt
	return view, err
}

func (s *Store) Timeline(ctx context.Context, tenantID, caseID uuid.UUID, afterVersion int64, limit int) ([]application.TimelineEntry, error) {
	if _, err := s.Get(ctx, tenantID, caseID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT event_id,aggregate_version,event_type,occurred_at,published_at
		FROM case_outbox WHERE tenant_id=$1 AND aggregate_id=$2 AND aggregate_version>$3
		ORDER BY aggregate_version LIMIT $4`, tenantID, caseID, afterVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]application.TimelineEntry, 0, limit)
	for rows.Next() {
		var entry application.TimelineEntry
		if err = rows.Scan(&entry.EventID, &entry.AggregateVersion, &entry.EventType, &entry.OccurredAt, &entry.PublishedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) Search(ctx context.Context, tenantID uuid.UUID, customerRef string, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) (application.SearchPage, error) {
	var before any
	if !beforeCreatedAt.IsZero() {
		before = beforeCreatedAt
	}
	rows, err := s.pool.Query(ctx, `SELECT tenant_id,id,customer_ref,workflow_type,workflow_version,
		business_status,orchestration_status,COALESCE(workflow_instance_id,''),row_version,created_at,updated_at
		FROM customer_cases WHERE tenant_id=$1 AND NOT is_deleted AND ($2='' OR customer_ref=$2)
		AND ($3::timestamptz IS NULL OR (created_at,id)<($3,$4))
		ORDER BY created_at DESC,id DESC LIMIT $5`, tenantID, customerRef, before, beforeID, limit+1)
	if err != nil {
		return application.SearchPage{}, err
	}
	defer rows.Close()
	page := application.SearchPage{Cases: make([]application.Case, 0, limit)}
	for rows.Next() {
		var item application.Case
		if err = rows.Scan(&item.TenantID, &item.ID, &item.CustomerRef, &item.WorkflowType,
			&item.WorkflowVersion, &item.BusinessStatus, &item.OrchestrationStatus,
			&item.WorkflowInstanceID, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return application.SearchPage{}, err
		}
		page.Cases = append(page.Cases, item)
	}
	if err = rows.Err(); err != nil {
		return application.SearchPage{}, err
	}
	if len(page.Cases) > limit {
		page.Cases = page.Cases[:limit]
		last := page.Cases[len(page.Cases)-1]
		page.NextCreatedAt, page.NextID = &last.CreatedAt, last.ID
	}
	return page, nil
}
