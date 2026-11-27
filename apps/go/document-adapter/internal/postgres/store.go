package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/document-adapter/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/odsruntime"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse document postgres dsn: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open document postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping document postgres: %w", err)
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

func (s *Store) AppendOutbox(ctx context.Context, evt application.OutboxEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO document_outbox (
			event_id, tenant_id, aggregate_type, aggregate_id, aggregate_version,
			event_type, correlation_id, payload_json, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9)
		ON CONFLICT (event_id) DO NOTHING
	`, evt.EventID, evt.TenantID, evt.AggregateType, evt.AggregateID, evt.AggregateVersion,
		evt.EventType, evt.CorrelationID, evt.PayloadJSON, evt.OccurredAt)
	return err
}

func (s *Store) FetchUnpublished(ctx context.Context, limit int) ([]odsruntime.OutboxEnvelope, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id, tenant_id, aggregate_type, aggregate_id, aggregate_version,
		       event_type, correlation_id, payload_json::text, created_at
		FROM document_outbox
		WHERE published_at IS NULL
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []odsruntime.OutboxEnvelope
	for rows.Next() {
		var env odsruntime.OutboxEnvelope
		var payload string
		if err := rows.Scan(
			&env.EventID, &env.TenantID, &env.AggregateType, &env.AggregateID,
			&env.AggregateVersion, &env.EventType, &env.CorrelationID, &payload, &env.OccurredAt,
		); err != nil {
			return nil, err
		}
		env.SchemaVersion = 1
		env.Producer = "document-adapter"
		env.Payload = json.RawMessage(payload)
		out = append(out, env)
	}
	return out, rows.Err()
}

func (s *Store) MarkPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE document_outbox SET published_at = $2 WHERE event_id = $1 AND published_at IS NULL`, eventID, publishedAt)
	return err
}
