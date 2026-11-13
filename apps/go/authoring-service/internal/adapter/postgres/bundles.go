package postgres

import (
	"bytes"
	"context"
	"errors"
	"math"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Load(ctx context.Context, install func([]byte) error) error {
	if install == nil {
		return errors.New("bundle installer is required")
	}
	rows, err := s.pool.Query(ctx, `SELECT bundle_bytes FROM authoring_authorization_bundle_cache ORDER BY tenant_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if err = install(raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) Persist(ctx context.Context, tenantID string, sequence, catalogVersion, revokeEpoch uint64, contentHash, raw []byte) ([]byte, error) {
	tenant, err := uuid.Parse(tenantID)
	if err != nil || sequence == 0 || sequence > math.MaxInt64 || catalogVersion == 0 || catalogVersion > math.MaxInt64 || revokeEpoch > math.MaxInt64 || len(contentHash) != 32 || len(raw) == 0 || len(raw) > 16<<20 {
		return nil, application.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentSequence, currentCatalog, currentRevoke int64
	var currentHash, currentRaw []byte
	err = tx.QueryRow(ctx, `SELECT bundle_sequence,catalog_version,revoke_epoch,content_hash,bundle_bytes
		FROM authoring_authorization_bundle_cache WHERE tenant_id=$1 FOR UPDATE`, tenant).Scan(&currentSequence, &currentCatalog, &currentRevoke, &currentHash, &currentRaw)
	if err == nil {
		if uint64(currentSequence) > sequence {
			return currentRaw, tx.Commit(ctx)
		}
		if uint64(currentSequence) == sequence {
			if !bytes.Equal(currentHash, contentHash) {
				return nil, application.ErrConflict
			}
			return currentRaw, tx.Commit(ctx)
		}
		if revokeEpoch < uint64(currentRevoke) {
			return nil, application.ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE authoring_authorization_bundle_cache SET bundle_sequence=$2,catalog_version=$3,revoke_epoch=$4,
			content_hash=$5,bundle_bytes=$6,installed_at=now() WHERE tenant_id=$1`, tenant, int64(sequence), int64(catalogVersion), int64(revokeEpoch), contentHash, raw)
	} else if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO authoring_authorization_bundle_cache(tenant_id,bundle_sequence,catalog_version,revoke_epoch,content_hash,bundle_bytes)
			VALUES($1,$2,$3,$4,$5,$6)`, tenant, int64(sequence), int64(catalogVersion), int64(revokeEpoch), contentHash, raw)
	} else {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

var _ interface {
	Load(context.Context, func([]byte) error) error
	Persist(context.Context, string, uint64, uint64, uint64, []byte, []byte) ([]byte, error)
} = (*Store)(nil)
