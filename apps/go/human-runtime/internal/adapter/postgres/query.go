package postgres

import (
	"context"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

func (s *Store) ListWorkItems(ctx context.Context, tenantID, actorID string, groups []string, limit int, cursor *application.PageCursor) ([]domain.WorkItem, *application.PageCursor, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursorTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	cursorID := "~"
	if cursor != nil {
		cursorTime = cursor.UpdatedAt
		cursorID = cursor.WorkItemID
	}
	rows, err := s.pool.Query(ctx, workItemSelect+` WHERE tenant_id=$1 AND NOT is_deleted AND status IN ('ACTIVE','COMPLETION_REQUESTED')
        AND (assignee_id=$2 OR candidate_group=ANY($3)) AND (updated_at,work_item_id)<($4,$5)
        ORDER BY updated_at DESC,work_item_id DESC LIMIT $6`, tenantID, actorID, groups, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]domain.WorkItem, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanWorkItem(rows)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *application.PageCursor
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = &application.PageCursor{UpdatedAt: last.UpdatedAt, WorkItemID: last.ID}
	}
	return items, next, nil
}

func (s *Store) GetCase(ctx context.Context, tenantID, caseID string) (application.CaseView, error) {
	var c domain.Case
	err := s.pool.QueryRow(ctx, `SELECT tenant_id,case_id,case_type,status,version,updated_at FROM human_cases WHERE tenant_id=$1 AND case_id=$2 AND NOT is_deleted`, tenantID, caseID).Scan(&c.TenantID, &c.ID, &c.CaseType, &c.Status, &c.Version, &c.UpdatedAt)
	if err != nil {
		return application.CaseView{}, err
	}
	c.Stages = map[string]domain.PlanItemStatus{}
	c.Milestones = map[string]domain.PlanItemStatus{}
	rows, err := s.pool.Query(ctx, `SELECT plan_item_id,plan_item_kind,status FROM human_case_plan_items WHERE tenant_id=$1 AND case_id=$2 AND NOT is_deleted ORDER BY plan_item_id`, tenantID, caseID)
	if err != nil {
		return application.CaseView{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind string
		var status domain.PlanItemStatus
		if err = rows.Scan(&id, &kind, &status); err != nil {
			return application.CaseView{}, err
		}
		if kind == "STAGE" {
			c.Stages[id] = status
		} else {
			c.Milestones[id] = status
		}
	}
	if err = rows.Err(); err != nil {
		return application.CaseView{}, err
	}
	return application.CaseView{Case: c}, nil
}

func (s *Store) ListAuditRecords(ctx context.Context, tenantID, workItemID, caseID string, limit int, cursor *application.AuditCursor) ([]application.AuditRecord, *application.AuditCursor, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursorTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	cursorID := "~"
	if cursor != nil {
		cursorTime = cursor.OccurredAt
		cursorID = cursor.AuditID
	}
	rows, err := s.pool.Query(ctx, `SELECT audit_id,COALESCE(work_item_id,''),COALESCE(case_id,''),actor_id,action,occurred_at,
		COALESCE(command_id,''),COALESCE(correlation_id,''),COALESCE(from_version,0),COALESCE(to_version,0),details
		FROM human_audit_log WHERE tenant_id=$1 AND NOT is_deleted AND ($2='' OR work_item_id=$2) AND ($3='' OR case_id=$3)
		AND (occurred_at,audit_id)<($4,$5) ORDER BY occurred_at DESC,audit_id DESC LIMIT $6`, tenantID, workItemID, caseID, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	records := make([]application.AuditRecord, 0, limit+1)
	for rows.Next() {
		var record application.AuditRecord
		if err = rows.Scan(&record.AuditID, &record.WorkItemID, &record.CaseID, &record.ActorID, &record.Action, &record.OccurredAt, &record.CommandID, &record.CorrelationID, &record.FromVersion, &record.ToVersion, &record.DetailsJSON); err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *application.AuditCursor
	if len(records) > limit {
		records = records[:limit]
		last := records[len(records)-1]
		next = &application.AuditCursor{OccurredAt: last.OccurredAt, AuditID: last.AuditID}
	}
	return records, next, nil
}

var _ application.QueryPort = (*Store)(nil)
