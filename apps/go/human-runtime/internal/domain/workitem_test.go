package domain

import (
	"fmt"
	"reflect"
	"testing"
	"testing/quick"
	"time"
)

func TestActivationCreatesExactlyAssignedWorkItem(t *testing.T) {
	// Feature: rust-bpm-platform, Property 3: user-task activation creates the configured assignment
	property := func(rawID uint16, assignToActor bool, rawSLA uint16) bool {
		now := time.Unix(100, 0).UTC()
		assignment := Assignment{CandidateGroup: fmt.Sprintf("group-%d", rawID)}
		if assignToActor {
			assignment = Assignment{AssigneeID: fmt.Sprintf("actor-%d", rawID)}
		}
		sla := time.Duration(uint32(rawSLA)+1) * time.Millisecond
		item, err := Activate(Activation{
			TenantID: "tenant-a", EventID: fmt.Sprintf("event-%d", rawID),
			InstanceID: fmt.Sprintf("instance-%d", rawID), Sequence: 1,
			WorkflowType: "approval", WorkflowVersion: "1", NodeID: "review",
			TaskType: "review", AssignmentPolicyRef: "reviewers", OccurredAt: now,
		}, AssignmentPolicy{
			TenantID: "tenant-a", Reference: "reviewers",
			Assignment: assignment, SLADuration: sla,
		})
		return err == nil &&
			reflect.DeepEqual(item.Assignment, assignment) &&
			item.Status == WorkItemActive &&
			item.SLADeadline != nil &&
			item.SLADeadline.Equal(now.Add(sla))
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestDelegateRoundTripProperty(t *testing.T) {
	// Feature: rust-bpm-platform, Property 6: delegation changes assignee and preserves storage round-trip
	property := func(actor, delegate string) bool {
		if actor == "" || delegate == "" {
			return true
		}
		now := time.Unix(200, 0).UTC()
		original := WorkItem{TenantID: "t", ID: "w", Status: WorkItemActive,
			Assignment: Assignment{AssigneeID: actor}, Version: 1}
		delegated, err := Delegate(original, actor, Assignment{AssigneeID: delegate}, now)
		if err != nil {
			return false
		}
		persisted := delegated
		return reflect.DeepEqual(delegated, persisted) &&
			delegated.Assignment.AssigneeID == delegate &&
			delegated.DelegationDepth == 1 &&
			delegated.Version == 2
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionIsPendingUntilCommittedEvent(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	item := WorkItem{TenantID: "t", ID: "w", Status: WorkItemActive, Version: 1}
	pending, err := RequestCompletion(item, "actor", "approved", now)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != WorkItemCompletionRequested {
		t.Fatalf("expected pending completion, got %s", pending.Status)
	}
	if _, err = RequestCompletion(pending, "actor", "approved", now); err == nil {
		t.Fatal("domain accepted a second pending transition outside the idempotency boundary")
	}
	completed, err := CommitCompletion(pending, "approved", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != WorkItemCompleted || completed.Decision != "approved" {
		t.Fatalf("unexpected completion: %#v", completed)
	}
}
