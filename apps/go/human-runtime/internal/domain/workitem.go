package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type WorkItemStatus string

const (
	WorkItemActive              WorkItemStatus = "ACTIVE"
	WorkItemCompletionRequested WorkItemStatus = "COMPLETION_REQUESTED"
	WorkItemCompleted           WorkItemStatus = "COMPLETED"
	WorkItemCancelled           WorkItemStatus = "CANCELLED"
)

type Assignment struct {
	AssigneeID     string
	CandidateGroup string
}

func (a Assignment) Validate() error {
	if (a.AssigneeID == "") == (a.CandidateGroup == "") {
		return errors.New("assignment must contain exactly one assignee or candidate group")
	}
	return nil
}

type WorkItem struct {
	TenantID            string
	ID                  string
	ActivationEventID   string
	InstanceID          string
	WorkflowType        string
	WorkflowVersion     string
	NodeID              string
	TaskType            string
	AssignmentPolicyRef string
	Assignment          Assignment
	FormKey             string
	Status              WorkItemStatus
	Decision            string
	CompletionCommandID string
	SLADeadline         *time.Time
	EscalationPolicyRef string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Activation struct {
	TenantID            string
	EventID             string
	Sequence            uint64
	InstanceID          string
	WorkflowType        string
	WorkflowVersion     string
	NodeID              string
	TaskType            string
	AssignmentPolicyRef string
	FormKey             string
	OccurredAt          time.Time
}

type AssignmentPolicy struct {
	TenantID            string
	Reference           string
	WorkflowType        string
	WorkflowVersion     string
	NodeID              string
	Assignment          Assignment
	SLADuration         time.Duration
	EscalationPolicyRef string
	ConfigVersion       string
	Version             int64
}

func Activate(activation Activation, policy AssignmentPolicy) (WorkItem, error) {
	for field, value := range map[string]string{
		"tenant_id": activation.TenantID, "event_id": activation.EventID,
		"instance_id": activation.InstanceID, "node_id": activation.NodeID,
		"assignment_policy_ref": activation.AssignmentPolicyRef,
	} {
		if strings.TrimSpace(value) == "" {
			return WorkItem{}, fmt.Errorf("%s must not be empty", field)
		}
	}
	if activation.Sequence == 0 {
		return WorkItem{}, errors.New("event sequence must be greater than zero")
	}
	if activation.TenantID != policy.TenantID || activation.AssignmentPolicyRef != policy.Reference {
		return WorkItem{}, errors.New("activation and assignment policy scope do not match")
	}
	if err := policy.Assignment.Validate(); err != nil {
		return WorkItem{}, err
	}
	deadline := activation.OccurredAt.Add(policy.SLADuration)
	var slaDeadline *time.Time
	if policy.SLADuration > 0 {
		slaDeadline = &deadline
	}
	return WorkItem{
		TenantID: activation.TenantID, ID: activation.EventID,
		ActivationEventID: activation.EventID, InstanceID: activation.InstanceID,
		WorkflowType: activation.WorkflowType, WorkflowVersion: activation.WorkflowVersion,
		NodeID: activation.NodeID, TaskType: activation.TaskType,
		AssignmentPolicyRef: activation.AssignmentPolicyRef, Assignment: policy.Assignment,
		FormKey: activation.FormKey, Status: WorkItemActive, SLADeadline: slaDeadline,
		EscalationPolicyRef: policy.EscalationPolicyRef, Version: 1,
		CreatedAt: activation.OccurredAt, UpdatedAt: activation.OccurredAt,
	}, nil
}

func (w WorkItem) CanAct(actorID string, actorGroups map[string]struct{}) bool {
	if w.Assignment.AssigneeID != "" {
		return actorID == w.Assignment.AssigneeID
	}
	_, ok := actorGroups[w.Assignment.CandidateGroup]
	return ok
}

func RequestCompletion(w WorkItem, actorID, decision string, now time.Time) (WorkItem, error) {
	if w.Status != WorkItemActive {
		return WorkItem{}, fmt.Errorf("work item status %s cannot be completed", w.Status)
	}
	if actorID == "" || strings.TrimSpace(decision) == "" {
		return WorkItem{}, errors.New("actor and decision must not be empty")
	}
	w.Status = WorkItemCompletionRequested
	w.Decision = decision
	w.Version++
	w.UpdatedAt = now
	return w, nil
}

func CommitCompletion(w WorkItem, decision string, now time.Time) (WorkItem, error) {
	if w.Status != WorkItemCompletionRequested && w.Status != WorkItemActive {
		return WorkItem{}, fmt.Errorf("work item status %s cannot accept committed completion", w.Status)
	}
	w.Status = WorkItemCompleted
	if decision != "" {
		w.Decision = decision
	}
	w.Version++
	w.UpdatedAt = now
	return w, nil
}

func Delegate(w WorkItem, actorID string, assignment Assignment, now time.Time) (WorkItem, error) {
	if w.Status != WorkItemActive {
		return WorkItem{}, fmt.Errorf("work item status %s cannot be delegated", w.Status)
	}
	if actorID == "" {
		return WorkItem{}, errors.New("delegating actor must not be empty")
	}
	if err := assignment.Validate(); err != nil {
		return WorkItem{}, err
	}
	w.Assignment = assignment
	w.Version++
	w.UpdatedAt = now
	return w, nil
}
