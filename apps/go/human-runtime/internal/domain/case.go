package domain

import (
	"errors"
	"fmt"
	"time"
)

type CaseStatus string
type PlanItemStatus string

const (
	CaseActive    CaseStatus     = "ACTIVE"
	CaseCompleted CaseStatus     = "COMPLETED"
	PlanAvailable PlanItemStatus = "AVAILABLE"
	PlanActive    PlanItemStatus = "ACTIVE"
	PlanCompleted PlanItemStatus = "COMPLETED"
)

type Case struct {
	TenantID   string
	ID         string
	CaseType   string
	Status     CaseStatus
	Stages     map[string]PlanItemStatus
	Milestones map[string]PlanItemStatus
	Version    int64
	UpdatedAt  time.Time
}

func NewCase(tenantID, id, caseType string, stages, milestones []string, now time.Time) (Case, error) {
	if tenantID == "" || id == "" || caseType == "" {
		return Case{}, errors.New("tenant, case id, and case type must not be empty")
	}
	c := Case{TenantID: tenantID, ID: id, CaseType: caseType, Status: CaseActive,
		Stages: map[string]PlanItemStatus{}, Milestones: map[string]PlanItemStatus{}, Version: 1, UpdatedAt: now}
	for _, stage := range stages {
		if stage == "" {
			return Case{}, errors.New("stage id must not be empty")
		}
		c.Stages[stage] = PlanAvailable
	}
	for _, milestone := range milestones {
		if milestone == "" {
			return Case{}, errors.New("milestone id must not be empty")
		}
		c.Milestones[milestone] = PlanAvailable
	}
	return c, nil
}

func TransitionStage(c Case, stageID string, target PlanItemStatus, now time.Time) (Case, error) {
	current, ok := c.Stages[stageID]
	if !ok {
		return Case{}, fmt.Errorf("unknown stage %s", stageID)
	}
	valid := (current == PlanAvailable && target == PlanActive) || (current == PlanActive && target == PlanCompleted)
	if !valid {
		return Case{}, fmt.Errorf("invalid stage transition %s -> %s", current, target)
	}
	c.Stages[stageID] = target
	c.Version++
	c.UpdatedAt = now
	return c, nil
}

func AchieveMilestone(c Case, milestoneID string, now time.Time) (Case, error) {
	current, ok := c.Milestones[milestoneID]
	if !ok {
		return Case{}, fmt.Errorf("unknown milestone %s", milestoneID)
	}
	if current == PlanCompleted {
		return c, nil
	}
	c.Milestones[milestoneID] = PlanCompleted
	c.Version++
	c.UpdatedAt = now
	return c, nil
}
