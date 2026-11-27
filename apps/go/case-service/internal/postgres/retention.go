package postgres

import (
	"context"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
)

func (s *Store) PurgeExpiredRetention(ctx context.Context, now time.Time, policy application.RetentionPolicy) (application.PurgeStats, error) {
	if now.IsZero() {
		return application.PurgeStats{}, application.ErrInvalid
	}
	if err := policy.Validate(); err != nil {
		return application.PurgeStats{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return application.PurgeStats{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stats application.PurgeStats
	outboxCutoff := now.UTC().Add(-policy.PublishedOutboxRetention)
	tag, err := tx.Exec(ctx, `DELETE FROM case_outbox WHERE ctid IN (
		SELECT ctid FROM case_outbox
		WHERE published_at IS NOT NULL AND published_at < $1
		ORDER BY published_at ASC
		LIMIT $2
	)`, outboxCutoff, policy.MaxBatchSize)
	if err != nil {
		return application.PurgeStats{}, err
	}
	stats.PublishedOutboxDeleted = tag.RowsAffected()

	inboxCutoff := now.UTC().Add(-policy.InboxReplayWindow)
	tag, err = tx.Exec(ctx, `DELETE FROM case_inbox WHERE ctid IN (
		SELECT ctid FROM case_inbox
		WHERE applied_at < $1
		ORDER BY applied_at ASC
		LIMIT $2
	)`, inboxCutoff, policy.MaxBatchSize)
	if err != nil {
		return application.PurgeStats{}, err
	}
	stats.InboxDeleted = tag.RowsAffected()

	receiptCutoff := now.UTC().Add(-policy.CreateReceiptRetention)
	tag, err = tx.Exec(ctx, `DELETE FROM case_create_receipts WHERE ctid IN (
		SELECT ctid FROM case_create_receipts
		WHERE created_at < $1
		ORDER BY created_at ASC
		LIMIT $2
	)`, receiptCutoff, policy.MaxBatchSize)
	if err != nil {
		return application.PurgeStats{}, err
	}
	stats.CreateReceiptsDeleted = tag.RowsAffected()

	if err = tx.Commit(ctx); err != nil {
		return application.PurgeStats{}, err
	}
	return stats, nil
}

func (s *Store) ReconciliationSummary(ctx context.Context, now time.Time, staleAfter time.Duration) (application.ReconciliationReport, error) {
	if now.IsZero() || staleAfter <= 0 {
		return application.ReconciliationReport{}, application.ErrInvalid
	}
	staleCutoff := now.UTC().Add(-staleAfter)
	var report application.ReconciliationReport
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customer_cases WHERE NOT is_deleted AND orchestration_status = 'ACTIVE'),
		(SELECT count(*) FROM customer_cases WHERE NOT is_deleted AND orchestration_status = 'START_PENDING'),
		(SELECT count(*) FROM customer_cases WHERE NOT is_deleted AND orchestration_status = 'RECONCILING'),
		(SELECT count(*) FROM customer_cases WHERE NOT is_deleted AND orchestration_status = 'START_FAILED'),
		(SELECT count(*) FROM customer_cases WHERE NOT is_deleted AND orchestration_status = 'CLOSED'),
		(SELECT count(*) FROM case_outbox WHERE published_at IS NULL),
		(SELECT count(*) FROM workflow_start_jobs WHERE status IN ('PENDING', 'RECONCILING') AND updated_at < $1),
		(SELECT count(*) FROM case_inbox)`, staleCutoff).Scan(
		&report.ActiveCases,
		&report.StartPendingCases,
		&report.ReconcilingCases,
		&report.StartFailedCases,
		&report.ClosedCases,
		&report.UnpublishedOutbox,
		&report.StalePendingStartJobs,
		&report.InboxCount,
	)
	if err != nil {
		return application.ReconciliationReport{}, err
	}
	return report, nil
}

var _ application.RetentionRepository = (*Store)(nil)
