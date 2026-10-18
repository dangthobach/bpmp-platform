package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SaveQuery(ctx context.Context, item application.SavedQuery, expected uint64) (application.SavedQuery, error) {
	payload, err := json.Marshal(item.Query)
	if err != nil {
		return application.SavedQuery{}, err
	}
	if expected == 0 {
		err = s.pool.QueryRow(ctx, `INSERT INTO projection_saved_queries(tenant_id,id,owner_subject,name,version,query_json) VALUES($1,$2,$3,$4,1,$5) ON CONFLICT DO NOTHING RETURNING version,created_at,updated_at`, item.TenantID, item.ID, item.OwnerSubject, item.Name, payload).Scan(&item.Version, &item.CreatedAt, &item.UpdatedAt)
	} else {
		err = s.pool.QueryRow(ctx, `UPDATE projection_saved_queries SET name=$4,query_json=$5,version=version+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND owner_subject=$3 AND version=$6 RETURNING version,created_at,updated_at`, item.TenantID, item.ID, item.OwnerSubject, item.Name, payload, expected).Scan(&item.Version, &item.CreatedAt, &item.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return application.SavedQuery{}, application.ErrConflict
	}
	return item, err
}

func (s *Store) ListSavedQueries(ctx context.Context, tenant, owner string, afterTime time.Time, afterID string, limit int) ([]application.SavedQuery, error) {
	args := []any{tenant, owner, limit}
	where := "tenant_id=$1 AND owner_subject=$2"
	if !afterTime.IsZero() {
		where += " AND (updated_at,id)<($4,$5)"
		args = append(args, afterTime, afterID)
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name,version,query_json,created_at,updated_at FROM projection_saved_queries WHERE `+where+` ORDER BY updated_at DESC,id DESC LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]application.SavedQuery, 0, limit)
	for rows.Next() {
		item := application.SavedQuery{TenantID: tenant, OwnerSubject: owner}
		var payload []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.Version, &payload, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(payload, &item.Query); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteSavedQuery(ctx context.Context, tenant, id, owner string, expected uint64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projection_saved_queries WHERE tenant_id=$1 AND id=$2 AND owner_subject=$3 AND version=$4`, tenant, id, owner, expected)
	if err == nil && tag.RowsAffected() == 0 {
		return application.ErrConflict
	}
	return err
}

var _ application.SavedQueryStore = (*Store)(nil)
