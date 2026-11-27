package application

import (
	"context"
	"testing"
	"time"
)

func TestAuditProjectionRedactionIndexLossRebuildAndRetention(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	svc := NewService(func() time.Time { return now })
	ctx := context.Background()
	actor := Actor{TenantID: "t1", ActorID: "auditor-1", Capabilities: []string{"audit.projection.read"}}

	authoritativeLog := []CommittedOwnerEvent{
		{
			EventID: "evt-old", TenantID: "t1", SourceService: "case-service", AggregateType: "Case", AggregateID: "case-1",
			Action: "CaseCreated", ActorID: "u1", CorrelationID: "c1", OccurredAt: now.AddDate(0, 0, -400),
			Metadata: map[string]string{"status": "ACTIVE", "ssn": "123-45-6789", "bearer_token": "secret"},
		},
		{
			EventID: "evt-hold", TenantID: "t1", SourceService: "document-adapter", AggregateType: "Document", AggregateID: "doc-1",
			Action: "DocumentAvailable", ActorID: "u1", CorrelationID: "c2", OccurredAt: now.AddDate(0, 0, -400),
			LegalHold: true, Metadata: map[string]string{"status": "CLEAN"},
		},
		{
			EventID: "evt-other-tenant", TenantID: "t2", SourceService: "case-service", AggregateType: "Case", AggregateID: "case-9",
			Action: "CaseCreated", ActorID: "u9", CorrelationID: "c9", OccurredAt: now,
		},
	}

	for _, e := range authoritativeLog {
		if _, err := svc.Ingest(ctx, e); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}

	// Verify PII/token redaction & cross-tenant isolation.
	before, err := svc.Search(ctx, actor, SearchQuery{})
	if err != nil || len(before.Entries) != 2 {
		t.Fatalf("expected 2 entries for t1, got err=%v len=%d", err, len(before.Entries))
	}
	for _, e := range before.Entries {
		if _, leaked := e.RedactedMetadata["ssn"]; leaked {
			t.Fatalf("PII field ssn leaked into audit projection")
		}
		if _, leaked := e.RedactedMetadata["bearer_token"]; leaked {
			t.Fatalf("secret field bearer_token leaked into audit projection")
		}
	}

	// Simulate total OpenSearch/index loss, then rebuild from authoritative owner log.
	svc.SimulateIndexLoss()
	empty, _ := svc.Search(ctx, actor, SearchQuery{})
	if len(empty.Entries) != 0 {
		t.Fatalf("expected empty projection after simulated index loss")
	}
	gen, count, err := svc.Rebuild(ctx, authoritativeLog)
	if err != nil || gen != 2 || count != 3 {
		t.Fatalf("Rebuild: gen=%d count=%d err=%v", gen, count, err)
	}
	after, err := svc.Search(ctx, actor, SearchQuery{})
	if err != nil || len(after.Entries) != 2 || after.ProjectionGeneration != 2 {
		t.Fatalf("expected rebuilt projection with generation 2, got %+v", after)
	}

	// Retention purges expired non-hold entry while preserving legal-hold entry.
	purged, err := svc.ApplyRetention(ctx, "t1", 365)
	if err != nil || purged != 1 {
		t.Fatalf("expected 1 purged entry, got purged=%d err=%v", purged, err)
	}
	remaining, _ := svc.Search(ctx, actor, SearchQuery{})
	if len(remaining.Entries) != 1 || remaining.Entries[0].EventID != "evt-hold" {
		t.Fatalf("expected legal-hold entry preserved, got %+v", remaining.Entries)
	}
}
