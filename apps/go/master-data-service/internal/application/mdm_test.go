package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMasterDataPublishRollbackPinningCacheLossAndConsumerLag(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	svc := NewService(func() time.Time { return now })
	ctx := context.Background()
	actor := Actor{TenantID: "t1", ActorID: "mdm-admin", Capabilities: []string{"mdm.read", "mdm.publish", "mdm.ack"}}

	// Publish v1
	p1, dup, err := svc.PublishDataset(ctx, actor, PublishRequest{
		DatasetCode: "BRANCH_CODES", DatasetName: "Branch Codes", ExpectedVersion: 0,
		Records: map[string]string{"HN01": "Hanoi Main", "HCM01": "Saigon Main"}, IdempotencyKey: "pub-1",
	})
	if err != nil || dup || p1.PublicationVersion != 1 {
		t.Fatalf("Publish v1: err=%v dup=%v ver=%d", err, dup, p1.PublicationVersion)
	}

	// Publish v2 with CAS
	p2, _, err := svc.PublishDataset(ctx, actor, PublishRequest{
		DatasetCode: "BRANCH_CODES", ExpectedVersion: 1,
		Records: map[string]string{"HN01": "Hanoi HQ", "HCM01": "Saigon Main", "DN01": "Da Nang"}, IdempotencyKey: "pub-2",
	})
	if err != nil || p2.PublicationVersion != 2 {
		t.Fatalf("Publish v2: err=%v ver=%d", err, p2.PublicationVersion)
	}

	// Stale CAS rejected
	if _, _, err := svc.PublishDataset(ctx, actor, PublishRequest{
		DatasetCode: "BRANCH_CODES", ExpectedVersion: 1,
		Records: map[string]string{"HN01": "Bad"}, IdempotencyKey: "pub-stale",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on stale CAS, got %v", err)
	}

	// Simulate Redis/local cache loss: authoritative snapshots remain intact.
	svc.ClearCache()

	// Snapshot pinning: requesting v1 after current pointer moved to v2 still resolves v1 deterministically.
	resV1, err := svc.ResolveBatch(ctx, actor, ResolveBatchRequest{DatasetCode: "BRANCH_CODES", Version: 1, Keys: []string{"HN01", "DN01"}})
	if err != nil || resV1.Resolved["HN01"] != "Hanoi Main" || len(resV1.Missing) != 1 || resV1.Missing[0] != "DN01" {
		t.Fatalf("expected pinned v1 resolution, got err=%v res=%+v", err, resV1)
	}

	// Rollback to v1 creates NEW generation v3 (immutable history preserved).
	p3, err := svc.RollbackDataset(ctx, actor, RollbackRequest{DatasetCode: "BRANCH_CODES", TargetVersion: 1, ExpectedVersion: 2})
	if err != nil || p3.PublicationVersion != 3 || p3.RollbackOfVersion == nil || *p3.RollbackOfVersion != 1 || p3.ContentSHA256 != p1.ContentSHA256 {
		t.Fatalf("expected rollback as v3 matching v1 hash, got err=%v p3=%+v", err, p3)
	}

	// Consumer ACK tracks lag accurately.
	ack, err := svc.AcknowledgeConsumer(ctx, actor, "BRANCH_CODES", "case-service", 2)
	if err != nil || ack.Lag != 1 {
		t.Fatalf("expected lag=1, got err=%v ack=%+v", err, ack)
	}

	// Cross-tenant isolation.
	other := Actor{TenantID: "t2", ActorID: "u2", Capabilities: []string{"mdm.read"}}
	if _, err := svc.GetSnapshot(ctx, other, "BRANCH_CODES", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-tenant ErrNotFound, got %v", err)
	}
}
