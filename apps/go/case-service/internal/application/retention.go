package application

import (
	"context"
	"time"
)

const (
	MinPublishedOutboxRetention = 24 * time.Hour
	MinInboxReplayWindow        = 7 * 24 * time.Hour
	MinCreateReceiptRetention   = 7 * 24 * time.Hour
	MaxRetentionBatchSize       = 5000
)

// RetentionPolicy defines fail-closed retention windows for Case Service
// operational tables. Unpublished outbox rows, active/reconciling start jobs,
// customer cases, and engine process timelines are never deleted by retention.
type RetentionPolicy struct {
	PublishedOutboxRetention time.Duration
	InboxReplayWindow        time.Duration
	CreateReceiptRetention   time.Duration
	MaxBatchSize             int
}

func (p RetentionPolicy) Validate() error {
	if p.PublishedOutboxRetention < MinPublishedOutboxRetention ||
		p.InboxReplayWindow < MinInboxReplayWindow ||
		p.CreateReceiptRetention < MinCreateReceiptRetention ||
		p.MaxBatchSize < 1 || p.MaxBatchSize > MaxRetentionBatchSize {
		return ErrInvalid
	}
	return nil
}

type PurgeStats struct {
	PublishedOutboxDeleted int64
	InboxDeleted           int64
	CreateReceiptsDeleted  int64
}

type ReconciliationReport struct {
	ActiveCases           int64
	StartPendingCases     int64
	ReconcilingCases      int64
	StartFailedCases      int64
	ClosedCases           int64
	UnpublishedOutbox     int64
	StalePendingStartJobs int64
	InboxCount            int64
}

type RetentionRepository interface {
	PurgeExpiredRetention(ctx context.Context, now time.Time, policy RetentionPolicy) (PurgeStats, error)
	ReconciliationSummary(ctx context.Context, now time.Time, staleAfter time.Duration) (ReconciliationReport, error)
}

type RetentionRunner struct {
	repo   RetentionRepository
	policy RetentionPolicy
	now    func() time.Time
}

func NewRetentionRunner(repo RetentionRepository, policy RetentionPolicy, now func() time.Time) (*RetentionRunner, error) {
	if repo == nil || now == nil {
		return nil, ErrInvalid
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &RetentionRunner{repo: repo, policy: policy, now: now}, nil
}

func (r *RetentionRunner) RunOnce(ctx context.Context) (PurgeStats, error) {
	now := r.now().UTC()
	if now.IsZero() {
		return PurgeStats{}, ErrInvalid
	}
	return r.repo.PurgeExpiredRetention(ctx, now, r.policy)
}

func (r *RetentionRunner) InspectReconciliation(ctx context.Context, staleAfter time.Duration) (ReconciliationReport, error) {
	if staleAfter <= 0 {
		return ReconciliationReport{}, ErrInvalid
	}
	now := r.now().UTC()
	if now.IsZero() {
		return ReconciliationReport{}, ErrInvalid
	}
	return r.repo.ReconciliationSummary(ctx, now, staleAfter)
}
