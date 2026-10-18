package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateQueryExport(ctx context.Context, item application.QueryExport) (application.QueryExport, error) {
	payload, err := json.Marshal(item.Query)
	if err != nil {
		return item, err
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO projection_query_exports(tenant_id,id,owner_subject,status,query_json,max_rows,expires_at) VALUES($1,$2,$3,1,$4,$5,$6) RETURNING created_at`, item.TenantID, item.ID, item.OwnerSubject, payload, item.MaxRows, item.ExpiresAt).Scan(&item.CreatedAt)
	return item, err
}
func (s *Store) GetQueryExport(ctx context.Context, tenant, id, owner string) (application.QueryExport, error) {
	item := application.QueryExport{TenantID: tenant, ID: id, OwnerSubject: owner}
	err := s.pool.QueryRow(ctx, `SELECT status,max_rows,row_count,csv,error_code,created_at,expires_at FROM projection_query_exports WHERE tenant_id=$1 AND id=$2 AND owner_subject=$3`, tenant, id, owner).Scan(&item.Status, &item.MaxRows, &item.RowCount, &item.CSV, &item.ErrorCode, &item.CreatedAt, &item.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = application.ErrNotFound
	}
	return item, err
}
func (s *Store) CancelQueryExport(ctx context.Context, tenant, id, owner string) (application.QueryExport, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE projection_query_exports SET status=4,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND owner_subject=$3 AND status IN(1,5)`, tenant, id, owner)
	if err != nil {
		return application.QueryExport{}, err
	}
	if tag.RowsAffected() == 0 {
		return s.GetQueryExport(ctx, tenant, id, owner)
	}
	return s.GetQueryExport(ctx, tenant, id, owner)
}
func (s *Store) ClaimQueryExport(ctx context.Context) (application.QueryExport, error) {
	item := application.QueryExport{}
	var payload []byte
	err := s.pool.QueryRow(ctx, `WITH candidate AS(SELECT tenant_id,id FROM projection_query_exports WHERE (status=1 OR(status=5 AND updated_at<now()-interval '5 minutes')) AND expires_at>now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE projection_query_exports e SET status=5,updated_at=now() FROM candidate c WHERE e.tenant_id=c.tenant_id AND e.id=c.id RETURNING e.tenant_id,e.id,e.owner_subject,e.query_json,e.max_rows,e.created_at,e.expires_at`).Scan(&item.TenantID, &item.ID, &item.OwnerSubject, &payload, &item.MaxRows, &item.CreatedAt, &item.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, application.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(payload, &item.Query)
	}
	return item, err
}
func (s *Store) CompleteQueryExport(ctx context.Context, tenant, id string, data []byte, count uint64, code string) error {
	status := application.QueryExportCompleted
	if code != "" {
		status = application.QueryExportFailed
		data = nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE projection_query_exports SET status=$3,csv=$4,row_count=$5,error_code=$6,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status=5`, tenant, id, status, data, count, code)
	return err
}

var _ application.QueryExportStore = (*Store)(nil)
