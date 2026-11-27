package queue

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryQueueLanesSnoozeAndExpiredLeaseRecovery(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	tenantID := uuid.New()
	delTx := uuid.New()
	delBulk := uuid.New()

	if err := q.Enqueue(ctx, tenantID, delTx, LaneTransactional, now); err != nil {
		t.Fatal(err)
	}
	// Idempotent duplicate enqueue for same (tenant, delivery).
	if err := q.Enqueue(ctx, tenantID, delTx, LaneTransactional, now); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ctx, tenantID, delBulk, LaneBulk, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Bulk job is scheduled in the future -> 0 claimed.
	bulkJobs, err := q.ClaimBatch(ctx, LaneBulk, 10, 30*time.Second, now)
	if err != nil || len(bulkJobs) != 0 {
		t.Fatalf("expected 0 bulk jobs before scheduled_at, got %d (err=%v)", len(bulkJobs), err)
	}

	// Transactional lane claims 1 job.
	txJobs, err := q.ClaimBatch(ctx, LaneTransactional, 10, 30*time.Second, now)
	if err != nil || len(txJobs) != 1 || txJobs[0].DeliveryID != delTx || txJobs[0].Attempts != 1 {
		t.Fatalf("unexpected claimed tx jobs: %+v err=%v", txJobs, err)
	}

	// Simulate worker crash: while lease is active, second claim returns 0; once lease expires, job is reclaimed!
	activeClaim, _ := q.ClaimBatch(ctx, LaneTransactional, 10, 30*time.Second, now.Add(10*time.Second))
	if len(activeClaim) != 0 {
		t.Fatal("leased job must not be double-claimed before lease expiry")
	}
	reclaimed, err := q.ClaimBatch(ctx, LaneTransactional, 10, 30*time.Second, now.Add(31*time.Second))
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Attempts != 2 {
		t.Fatalf("expected expired lease recovery with attempts=2, got %+v err=%v", reclaimed, err)
	}

	// Old worker token must be rejected!
	if err := q.Complete(ctx, txJobs[0].ID, txJobs[0].LeaseToken, now.Add(32*time.Second)); err == nil {
		t.Fatal("stale lease token must be rejected")
	}

	// Snooze and then complete with current valid token.
	snoozeUntil := now.Add(2 * time.Minute)
	if err := q.Snooze(ctx, reclaimed[0].ID, reclaimed[0].LeaseToken, snoozeUntil, "breaker open", now.Add(32*time.Second)); err != nil {
		t.Fatal(err)
	}
	afterSnooze, err := q.ClaimBatch(ctx, LaneTransactional, 10, 30*time.Second, snoozeUntil)
	if err != nil || len(afterSnooze) != 1 {
		t.Fatalf("expected snoozed job to become ready at snoozeUntil, got %d", len(afterSnooze))
	}
	if err := q.Complete(ctx, afterSnooze[0].ID, afterSnooze[0].LeaseToken, snoozeUntil.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
