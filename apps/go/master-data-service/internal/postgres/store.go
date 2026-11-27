package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/master-data-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/odsruntime"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mdm postgres dsn: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open mdm postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping mdm postgres: %w", err)
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
		INSERT INTO mdm_outbox (
			event_id, tenant_id, dataset_id, publication_version,
			event_type, content_sha256, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id) DO NOTHING
	`, evt.EventID, evt.TenantID, evt.DatasetID, evt.PublicationVersion,
		evt.EventType, evt.ContentSHA256, evt.OccurredAt)
	return err
}

func (s *Store) FetchUnpublished(ctx context.Context, limit int) ([]odsruntime.OutboxEnvelope, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id, tenant_id, dataset_id, publication_version,
		       event_type, content_sha256, occurred_at
		FROM mdm_outbox
		WHERE published_at IS NULL
		ORDER BY occurred_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []odsruntime.OutboxEnvelope
	for rows.Next() {
		var env odsruntime.OutboxEnvelope
		var sha string
		if err := rows.Scan(
			&env.EventID, &env.TenantID, &env.AggregateID, &env.AggregateVersion,
			&env.EventType, &sha, &env.OccurredAt,
		); err != nil {
			return nil, err
		}
		env.SchemaVersion = 1
		env.AggregateType = "MasterDataSet"
		env.Producer = "master-data-service"
		env.CorrelationID = env.EventID
		payload, _ := json.Marshal(map[string]any{"dataset_id": env.AggregateID, "content_sha256": sha})
		env.Payload = payload
		out = append(out, env)
	}
	return out, rows.Err()
}

func (s *Store) MarkPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE mdm_outbox SET published_at = $2 WHERE event_id = $1 AND published_at IS NULL`, eventID, publishedAt)
	return err
}
