package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubRetentionRepo struct {
	calledNow    time.Time
	calledPolicy RetentionPolicy
	calledStale  time.Duration
	stats        PurgeStats
	report       ReconciliationReport
	err          error
}

func (s *stubRetentionRepo) PurgeExpiredRetention(_ context.Context, now time.Time, policy RetentionPolicy) (PurgeStats, error) {
	s.calledNow = now
	s.calledPolicy = policy
	return s.stats, s.err
}

func (s *stubRetentionRepo) ReconciliationSummary(_ context.Context, now time.Time, staleAfter time.Duration) (ReconciliationReport, error) {
	s.calledNow = now
	s.calledStale = staleAfter
	return s.report, s.err
}

func TestRetentionPolicyValidation(t *testing.T) {
	valid := RetentionPolicy{
		PublishedOutboxRetention: 24 * time.Hour,
		InboxReplayWindow:        7 * 24 * time.Hour,
		CreateReceiptRetention:   7 * 24 * time.Hour,
		MaxBatchSize:             500,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid policy, got %v", err)
	}
	cases := []RetentionPolicy{
		{PublishedOutboxRetention: 23 * time.Hour, InboxReplayWindow: 7 * 24 * time.Hour, CreateReceiptRetention: 7 * 24 * time.Hour, MaxBatchSize: 100},
		{PublishedOutboxRetention: 24 * time.Hour, InboxReplayWindow: 6 * 24 * time.Hour, CreateReceiptRetention: 7 * 24 * time.Hour, MaxBatchSize: 100},
		{PublishedOutboxRetention: 24 * time.Hour, InboxReplayWindow: 7 * 24 * time.Hour, CreateReceiptRetention: 6 * 24 * time.Hour, MaxBatchSize: 100},
		{PublishedOutboxRetention: 24 * time.Hour, InboxReplayWindow: 7 * 24 * time.Hour, CreateReceiptRetention: 7 * 24 * time.Hour, MaxBatchSize: 0},
		{PublishedOutboxRetention: 24 * time.Hour, InboxReplayWindow: 7 * 24 * time.Hour, CreateReceiptRetention: 7 * 24 * time.Hour, MaxBatchSize: MaxRetentionBatchSize + 1},
	}
	for i, policy := range cases {
		if err := policy.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: expected ErrInvalid, got %v", i, err)
		}
	}
}

func TestRetentionRunnerExecutesAndInspects(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	repo := &stubRetentionRepo{
		stats:  PurgeStats{PublishedOutboxDeleted: 12, InboxDeleted: 4, CreateReceiptsDeleted: 3},
		report: ReconciliationReport{ActiveCases: 10, StartPendingCases: 1, UnpublishedOutbox: 2, StalePendingStartJobs: 1},
	}
	policy := RetentionPolicy{
		PublishedOutboxRetention: 48 * time.Hour,
		InboxReplayWindow:        14 * 24 * time.Hour,
		CreateReceiptRetention:   30 * 24 * time.Hour,
		MaxBatchSize:             250,
	}
	runner, err := NewRetentionRunner(repo, policy, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	stats, err := runner.RunOnce(context.Background())
	if err != nil || stats != repo.stats || !repo.calledNow.Equal(now) || repo.calledPolicy != policy {
		t.Fatalf("unexpected retention run: stats=%+v now=%v policy=%+v err=%v", stats, repo.calledNow, repo.calledPolicy, err)
	}
	report, err := runner.InspectReconciliation(context.Background(), 5*time.Minute)
	if err != nil || report != repo.report || repo.calledStale != 5*time.Minute {
		t.Fatalf("unexpected reconciliation report: report=%+v stale=%v err=%v", report, repo.calledStale, err)
	}
	if _, err := runner.InspectReconciliation(context.Background(), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for non-positive staleAfter, got %v", err)
	}
}
