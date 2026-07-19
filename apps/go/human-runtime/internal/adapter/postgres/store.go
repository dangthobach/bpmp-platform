package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) GetWorkItem(ctx context.Context, tenantID, workItemID string) (domain.WorkItem, error) {
	return scanWorkItem(s.pool.QueryRow(ctx, workItemSelect+` WHERE tenant_id=$1 AND work_item_id=$2 AND NOT is_deleted`, tenantID, workItemID))
}

func (s *Store) ProjectActivation(ctx context.Context, activation domain.Activation) (domain.WorkItem, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO human_event_inbox
        (tenant_id,consumer_name,event_id,stream_id,sequence,processed_at) VALUES($1,'human-runtime',$2,$3,$4,$5)
        ON CONFLICT (tenant_id,consumer_name,event_id) DO NOTHING`, activation.TenantID, activation.EventID, activation.InstanceID, activation.Sequence, activation.OccurredAt)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	if result.RowsAffected() == 0 {
		item, getErr := scanWorkItem(tx.QueryRow(ctx, workItemSelect+` WHERE tenant_id=$1 AND activation_event_id=$2`, activation.TenantID, activation.EventID))
		return item, true, getErr
	}
	policy, err := loadPolicy(ctx, tx, activation)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	item, err := domain.Activate(activation, policy)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO work_items
        (tenant_id,work_item_id,activation_event_id,instance_id,workflow_type,workflow_version,node_id,task_type,
         assignment_policy_ref,assignee_id,candidate_group,form_key,status,sla_deadline,escalation_policy_ref,version,created_at,updated_at)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13,$14,NULLIF($15,''),$16,$17,$17)`,
		item.TenantID, item.ID, item.ActivationEventID, item.InstanceID, item.WorkflowType, item.WorkflowVersion,
		item.NodeID, item.TaskType, item.AssignmentPolicyRef, item.Assignment.AssigneeID, item.Assignment.CandidateGroup,
		item.FormKey, item.Status, item.SLADeadline, item.EscalationPolicyRef, item.Version, item.CreatedAt)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	if err = appendAudit(ctx, tx, item.TenantID, "activation:"+item.ActivationEventID, item.ID, "", "system", "ACTIVATED", item.CreatedAt, "", "", 0, item.Version, nil); err != nil {
		return domain.WorkItem{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WorkItem{}, false, err
	}
	return item, false, nil
}

func (s *Store) RequestCompletion(ctx context.Context, item domain.WorkItem, commandID, correlationID, actorID string) error {
	return s.updateWorkItem(ctx, item, commandID, correlationID, actorID, "COMPLETION_REQUESTED",
		`status=$1,decision=$2,completion_command_id=$3`, item.Status, item.Decision, commandID)
}

func (s *Store) Delegate(ctx context.Context, item domain.WorkItem, commandID, correlationID, actorID string) error {
	return s.updateWorkItem(ctx, item, commandID, correlationID, actorID, "DELEGATED",
		`assignee_id=NULLIF($1,''),candidate_group=NULLIF($2,'')`, item.Assignment.AssigneeID, item.Assignment.CandidateGroup)
}

func (s *Store) updateWorkItem(ctx context.Context, item domain.WorkItem, commandID, correlationID, actorID, action, setSQL string, values ...any) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	args := append(values, item.UpdatedAt, item.Version, item.TenantID, item.ID, item.Version-1)
	query := fmt.Sprintf(`UPDATE work_items SET %s,updated_at=$%d,version=$%d
        WHERE tenant_id=$%d AND work_item_id=$%d AND version=$%d AND NOT is_deleted`, setSQL, len(values)+1, len(values)+2, len(values)+3, len(values)+4, len(values)+5)
	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrVersionConflict
	}
	details := map[string]any{"decision": item.Decision, "assignee_id": item.Assignment.AssigneeID, "candidate_group": item.Assignment.CandidateGroup}
	if err = appendAudit(ctx, tx, item.TenantID, action+":"+commandID, item.ID, "", actorID, action, item.UpdatedAt, commandID, correlationID, item.Version-1, item.Version, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CommitCompletion(ctx context.Context, event application.CommittedCompletion) error {
	if event.TenantID == "" || event.EventID == "" || event.InstanceID == "" || event.NodeID == "" || event.Sequence == 0 {
		return errors.New("committed completion metadata is incomplete")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO human_event_inbox
        (tenant_id,consumer_name,event_id,stream_id,sequence,processed_at) VALUES($1,'human-runtime',$2,$3,$4,$5)
        ON CONFLICT (tenant_id,consumer_name,event_id) DO NOTHING`, event.TenantID, event.EventID, event.InstanceID, event.Sequence, event.OccurredAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	var id string
	var version int64
	err = tx.QueryRow(ctx, `UPDATE work_items SET status='COMPLETED',decision=COALESCE(NULLIF($4,''),decision),
        updated_at=$5,version=version+1 WHERE tenant_id=$1 AND instance_id=$2 AND node_id=$3
        AND status IN ('ACTIVE','COMPLETION_REQUESTED') AND NOT is_deleted RETURNING work_item_id,version`,
		event.TenantID, event.InstanceID, event.NodeID, event.Decision, event.OccurredAt).Scan(&id, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		var status domain.WorkItemStatus
		lookupErr := tx.QueryRow(ctx, `SELECT status FROM work_items WHERE tenant_id=$1 AND instance_id=$2 AND node_id=$3 AND NOT is_deleted`, event.TenantID, event.InstanceID, event.NodeID).Scan(&status)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return application.ErrProjectionDependency
		}
		if lookupErr != nil {
			return lookupErr
		}
		if status == domain.WorkItemCompleted {
			return tx.Commit(ctx)
		}
		return fmt.Errorf("committed completion cannot transition work item in status %s", status)
	}
	if err != nil {
		return err
	}
	auditID := "completed:" + event.EventID
	if err = appendAudit(ctx, tx, event.TenantID, auditID, id, "", "engine", "COMPLETED", event.OccurredAt, "", "", version-1, version, map[string]any{"decision": event.Decision, "event_id": event.EventID, "sequence": event.Sequence}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CommitCancellation(ctx context.Context, event application.CommittedCancellation) error {
	if event.TenantID == "" || event.EventID == "" || event.InstanceID == "" || event.NodeID == "" || event.Sequence == 0 {
		return errors.New("committed cancellation metadata is incomplete")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	duplicate, err := insertInbox(ctx, tx, event.TenantID, event.EventID, event.InstanceID, event.Sequence, event.OccurredAt)
	if err != nil || duplicate {
		if duplicate {
			return tx.Commit(ctx)
		}
		return err
	}
	var id string
	var version int64
	err = tx.QueryRow(ctx, `UPDATE work_items SET status='CANCELLED',updated_at=$4,version=version+1
		WHERE tenant_id=$1 AND instance_id=$2 AND node_id=$3 AND status IN ('ACTIVE','COMPLETION_REQUESTED')
		AND NOT is_deleted RETURNING work_item_id,version`, event.TenantID, event.InstanceID, event.NodeID, event.OccurredAt).Scan(&id, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrProjectionDependency
	}
	if err != nil {
		return err
	}
	if err = appendAudit(ctx, tx, event.TenantID, "cancelled:"+event.EventID, id, "", "engine", "CANCELLED", event.OccurredAt, "", "", version-1, version, map[string]any{"event_id": event.EventID, "sequence": event.Sequence, "reason": event.Reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ProjectCase(ctx context.Context, event application.CommittedCase) (bool, error) {
	c := event.Case
	if event.EventID == "" || event.Sequence == 0 || c.TenantID == "" || c.ID == "" {
		return false, errors.New("committed case metadata is incomplete")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO human_event_inbox(tenant_id,consumer_name,event_id,stream_id,sequence,processed_at)
		VALUES($1,'human-runtime',$2,$3,$4,$5) ON CONFLICT (tenant_id,consumer_name,event_id) DO NOTHING`, c.TenantID, event.EventID, c.ID, event.Sequence, c.UpdatedAt)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() == 0 {
		return true, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO human_cases(tenant_id,case_id,case_type,status,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)`, c.TenantID, c.ID, c.CaseType, c.Status, c.Version, c.UpdatedAt)
	if err != nil {
		return false, err
	}
	for id, status := range c.Stages {
		if _, err = tx.Exec(ctx, `INSERT INTO human_case_plan_items(tenant_id,case_id,plan_item_id,plan_item_kind,status,version,created_at,updated_at) VALUES($1,$2,$3,'STAGE',$4,1,$5,$5)`, c.TenantID, c.ID, id, status, c.UpdatedAt); err != nil {
			return false, err
		}
	}
	for id, status := range c.Milestones {
		if _, err = tx.Exec(ctx, `INSERT INTO human_case_plan_items(tenant_id,case_id,plan_item_id,plan_item_kind,status,version,created_at,updated_at) VALUES($1,$2,$3,'MILESTONE',$4,1,$5,$5)`, c.TenantID, c.ID, id, status, c.UpdatedAt); err != nil {
			return false, err
		}
	}
	if err = appendAudit(ctx, tx, c.TenantID, "case-activated:"+event.EventID, "", c.ID, "engine", "CASE_ACTIVATED", c.UpdatedAt, "", "", 0, c.Version, map[string]any{"event_id": event.EventID, "sequence": event.Sequence}); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (s *Store) CommitCaseTransition(ctx context.Context, event application.CommittedCaseTransition) error {
	if event.TenantID == "" || event.EventID == "" || event.CaseID == "" || event.PlanItemID == "" || event.Sequence == 0 ||
		(event.PlanItemKind != "STAGE" && event.PlanItemKind != "MILESTONE") {
		return errors.New("committed case transition metadata is incomplete")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	duplicate, err := insertInbox(ctx, tx, event.TenantID, event.EventID, event.CaseID, event.Sequence, event.OccurredAt)
	if err != nil || duplicate {
		if duplicate {
			return tx.Commit(ctx)
		}
		return err
	}
	var itemVersion int64
	err = tx.QueryRow(ctx, `UPDATE human_case_plan_items SET status=$5,version=version+1,updated_at=$6
		WHERE tenant_id=$1 AND case_id=$2 AND plan_item_id=$3 AND plan_item_kind=$4 AND NOT is_deleted
		AND (($4='STAGE' AND ((status='AVAILABLE' AND $5='ACTIVE') OR (status='ACTIVE' AND $5='COMPLETED')))
		 OR ($4='MILESTONE' AND status IN ('AVAILABLE','ACTIVE') AND $5='COMPLETED')) RETURNING version`,
		event.TenantID, event.CaseID, event.PlanItemID, event.PlanItemKind, event.Status, event.OccurredAt).Scan(&itemVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrProjectionDependency
	}
	if err != nil {
		return err
	}
	var caseVersion int64
	if err = tx.QueryRow(ctx, `UPDATE human_cases SET version=version+1,updated_at=$3 WHERE tenant_id=$1 AND case_id=$2 AND NOT is_deleted RETURNING version`, event.TenantID, event.CaseID, event.OccurredAt).Scan(&caseVersion); err != nil {
		return err
	}
	details := map[string]any{"event_id": event.EventID, "sequence": event.Sequence, "satisfied_sentry_ids": event.SatisfiedSentryIDs, "plan_item_version": itemVersion}
	if err = appendAudit(ctx, tx, event.TenantID, "case-transition:"+event.EventID, "", event.CaseID, "engine", "CASE_"+event.PlanItemKind+"_"+string(event.Status), event.OccurredAt, "", "", caseVersion-1, caseVersion, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertInbox(ctx context.Context, tx pgx.Tx, tenantID, eventID, streamID string, sequence uint64, occurredAt time.Time) (bool, error) {
	result, err := tx.Exec(ctx, `INSERT INTO human_event_inbox(tenant_id,consumer_name,event_id,stream_id,sequence,processed_at)
		VALUES($1,'human-runtime',$2,$3,$4,$5) ON CONFLICT (tenant_id,consumer_name,event_id) DO NOTHING`, tenantID, eventID, streamID, sequence, occurredAt)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() == 0, nil
}

func (s *Store) TransitionCaseStage(ctx context.Context, tenantID, caseID, stageID string, target domain.PlanItemStatus, actorID string, at time.Time) error {
	return s.transitionPlanItem(ctx, tenantID, caseID, stageID, "STAGE", target, actorID, at)
}
func (s *Store) AchieveCaseMilestone(ctx context.Context, tenantID, caseID, milestoneID, actorID string, at time.Time) error {
	return s.transitionPlanItem(ctx, tenantID, caseID, milestoneID, "MILESTONE", domain.PlanCompleted, actorID, at)
}
func (s *Store) transitionPlanItem(ctx context.Context, tenantID, caseID, itemID, kind string, target domain.PlanItemStatus, actorID string, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var version int64
	err = tx.QueryRow(ctx, `UPDATE human_case_plan_items SET status=$5,version=version+1,updated_at=$6 WHERE tenant_id=$1 AND case_id=$2 AND plan_item_id=$3 AND plan_item_kind=$4 AND NOT is_deleted RETURNING version`, tenantID, caseID, itemID, kind, target, at).Scan(&version)
	if err != nil {
		return err
	}
	auditID := fmt.Sprintf("case:%s:%s:%d", caseID, itemID, version)
	if err = appendAudit(ctx, tx, tenantID, auditID, "", caseID, actorID, "CASE_"+kind+"_"+string(target), at, "", "", version-1, version, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const workItemSelect = `SELECT tenant_id,work_item_id,activation_event_id,instance_id,workflow_type,workflow_version,node_id,task_type,
 assignment_policy_ref,COALESCE(assignee_id,''),COALESCE(candidate_group,''),COALESCE(form_key,''),status,COALESCE(decision,''),
 COALESCE(completion_command_id,''),sla_deadline,COALESCE(escalation_policy_ref,''),version,created_at,updated_at FROM work_items`

type rowScanner interface{ Scan(...any) error }

func scanWorkItem(row rowScanner) (domain.WorkItem, error) {
	var w domain.WorkItem
	err := row.Scan(&w.TenantID, &w.ID, &w.ActivationEventID, &w.InstanceID, &w.WorkflowType, &w.WorkflowVersion, &w.NodeID, &w.TaskType, &w.AssignmentPolicyRef, &w.Assignment.AssigneeID, &w.Assignment.CandidateGroup, &w.FormKey, &w.Status, &w.Decision, &w.CompletionCommandID, &w.SLADeadline, &w.EscalationPolicyRef, &w.Version, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func loadPolicy(ctx context.Context, tx pgx.Tx, a domain.Activation) (domain.AssignmentPolicy, error) {
	var p domain.AssignmentPolicy
	var ms int64
	err := tx.QueryRow(ctx, `SELECT tenant_id,policy_ref,workflow_type,workflow_version,node_id,COALESCE(assignee_id,''),COALESCE(candidate_group,''),sla_duration_ms,COALESCE(escalation_policy_ref,''),config_version,version FROM assignment_policies WHERE tenant_id=$1 AND policy_ref=$2 AND workflow_type=$3 AND workflow_version=$4 AND node_id=$5 AND NOT is_deleted`, a.TenantID, a.AssignmentPolicyRef, a.WorkflowType, a.WorkflowVersion, a.NodeID).Scan(&p.TenantID, &p.Reference, &p.WorkflowType, &p.WorkflowVersion, &p.NodeID, &p.Assignment.AssigneeID, &p.Assignment.CandidateGroup, &ms, &p.EscalationPolicyRef, &p.ConfigVersion, &p.Version)
	p.SLADuration = time.Duration(ms) * time.Millisecond
	return p, err
}

func appendAudit(ctx context.Context, tx pgx.Tx, tenantID, auditID, workItemID, caseID, actorID, action string, at time.Time, commandID, correlationID string, fromVersion, toVersion int64, details any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO human_audit_log(tenant_id,audit_id,work_item_id,case_id,actor_id,action,occurred_at,command_id,correlation_id,from_version,to_version,details) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,0),$11,$12) ON CONFLICT DO NOTHING`, tenantID, auditID, workItemID, caseID, actorID, action, at, commandID, correlationID, fromVersion, toVersion, payload)
	return err
}

var _ application.Store = (*Store)(nil)
