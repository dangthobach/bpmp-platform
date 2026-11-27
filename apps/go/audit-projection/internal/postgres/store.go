package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/audit-projection/internal/application"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse audit postgres dsn: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open audit postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping audit postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.pool.Ping(pingCtx)
}

// UpsertEntry persists a redacted projected audit entry idempotently (safe for Kafka replay).
func (s *Store) UpsertEntry(ctx context.Context, entry application.ProjectedEntry) error {
	metaJSON, _ := json.Marshal(entry.RedactedMetadata)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_projection_entries (
			tenant_id, event_id, source_service, aggregate_type, aggregate_id,
			action, actor_id, correlation_id, occurred_at, redacted_metadata,
			legal_hold, projection_generation
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12)
		ON CONFLICT (tenant_id, event_id) DO NOTHING
	`, entry.TenantID, entry.EventID, entry.SourceService, entry.AggregateType, entry.AggregateID,
		entry.Action, entry.ActorID, entry.CorrelationID, entry.OccurredAt, string(metaJSON),
		entry.LegalHold, entry.ProjectionGeneration)
	return err
}
