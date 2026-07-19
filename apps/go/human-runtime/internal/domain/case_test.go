package domain

import (
	"testing"
	"time"
)

func TestCaseStageAndMilestoneLifecycle(t *testing.T) {
	now := time.Unix(400, 0).UTC()
	c, err := NewCase("tenant-a", "case-1", "claim", []string{"assessment"}, []string{"approved"}, now)
	if err != nil {
		t.Fatal(err)
	}
	c, err = TransitionStage(c, "assessment", PlanActive, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	c, err = TransitionStage(c, "assessment", PlanCompleted, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	c, err = AchieveMilestone(c, "approved", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stages["assessment"] != PlanCompleted || c.Milestones["approved"] != PlanCompleted {
		t.Fatalf("unexpected case state: %#v", c)
	}
}
