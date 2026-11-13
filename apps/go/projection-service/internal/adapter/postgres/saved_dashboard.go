package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SaveDashboard(ctx context.Context, item application.SavedDashboard, expected uint64) (application.SavedDashboard, error) {
	var err error
	if expected == 0 {
		err = s.pool.QueryRow(ctx, `INSERT INTO projection_saved_dashboards(tenant_id,id,owner_subject,name,version,config_json) VALUES($1,$2,$3,$4,1,$5::jsonb) ON CONFLICT DO NOTHING RETURNING version,config_json::text,created_at,updated_at`, item.TenantID, item.ID, item.OwnerSubject, item.Name, item.ConfigJSON).Scan(&item.Version, &item.ConfigJSON, &item.CreatedAt, &item.UpdatedAt)
	} else {
		err = s.pool.QueryRow(ctx, `UPDATE projection_saved_dashboards SET name=$4,config_json=$5::jsonb,version=version+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND owner_subject=$3 AND version=$6 RETURNING version,config_json::text,created_at,updated_at`, item.TenantID, item.ID, item.OwnerSubject, item.Name, item.ConfigJSON, expected).Scan(&item.Version, &item.ConfigJSON, &item.CreatedAt, &item.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return application.SavedDashboard{}, application.ErrConflict
	}
	return item, err
}

func (s *Store) ListDashboards(ctx context.Context, tenant, owner string, afterTime time.Time, afterID string, limit int) ([]application.SavedDashboard, error) {
	args := []any{tenant, owner, limit}
	where := "tenant_id=$1 AND owner_subject=$2"
	if !afterTime.IsZero() {
		where += " AND (updated_at,id)<($4,$5)"
		args = append(args, afterTime, afterID)
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name,version,config_json::text,created_at,updated_at FROM projection_saved_dashboards WHERE `+where+` ORDER BY updated_at DESC,id DESC LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]application.SavedDashboard, 0, limit)
	for rows.Next() {
		item := application.SavedDashboard{TenantID: tenant, OwnerSubject: owner}
		if err = rows.Scan(&item.ID, &item.Name, &item.Version, &item.ConfigJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteDashboard(ctx context.Context, tenant, id, owner string, expected uint64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projection_saved_dashboards WHERE tenant_id=$1 AND id=$2 AND owner_subject=$3 AND version=$4`, tenant, id, owner, expected)
	if err == nil && tag.RowsAffected() == 0 {
		return application.ErrConflict
	}
	return err
}

var _ application.SavedDashboardStore = (*Store)(nil)
