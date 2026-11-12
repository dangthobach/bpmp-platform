package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
)

const maxRestoredAuthorizationBundles = 100_000

// Load restores only the latest immutable signed bundle for each tenant.
func (s *Store) Load(ctx context.Context, install func([]byte) error) error {
	if install == nil {
		return errors.New("authorization bundle restore callback is required")
	}
	rows, err := s.pool.Query(ctx, `SELECT bundle_bytes
		FROM projection_authorization_policy_bundle_state
		ORDER BY tenant_id`)
	if err != nil {
		return fmt.Errorf("query persisted authorization bundles: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > maxRestoredAuthorizationBundles {
			return errors.New("persisted authorization bundle count exceeds startup limit")
		}
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return fmt.Errorf("scan persisted authorization bundle: %w", err)
		}
		if err = install(raw); err != nil {
			return fmt.Errorf("verify persisted authorization bundle: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate persisted authorization bundles: %w", err)
	}
	return nil
}

// Persist commits the highest verified sequence before the consumer acknowledges it.
// A stale delivery returns the already persisted newer bundle for local installation.
func (s *Store) Persist(
	ctx context.Context,
	tenantID string,
	sequence uint64,
	catalogVersion uint64,
	revokeEpoch uint64,
	contentHash []byte,
	raw []byte,
) ([]byte, error) {
	if tenantID == "" || sequence == 0 || catalogVersion == 0 ||
		sequence > math.MaxInt64 || catalogVersion > math.MaxInt64 || revokeEpoch > math.MaxInt64 ||
		len(contentHash) != 32 || len(raw) == 0 {
		return nil, errors.New("authorization bundle persistence metadata is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin authorization bundle persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var latest []byte
	err = tx.QueryRow(ctx, `INSERT INTO projection_authorization_policy_bundle_state
		(tenant_id,bundle_sequence,catalog_version,revoke_epoch,content_hash,bundle_bytes)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id) DO UPDATE SET
			bundle_sequence=EXCLUDED.bundle_sequence,
			catalog_version=EXCLUDED.catalog_version,
			revoke_epoch=EXCLUDED.revoke_epoch,
			content_hash=EXCLUDED.content_hash,
			bundle_bytes=EXCLUDED.bundle_bytes,
			installed_at=now()
		WHERE projection_authorization_policy_bundle_state.bundle_sequence < EXCLUDED.bundle_sequence
		  AND projection_authorization_policy_bundle_state.catalog_version <= EXCLUDED.catalog_version
		  AND projection_authorization_policy_bundle_state.revoke_epoch <= EXCLUDED.revoke_epoch
		RETURNING bundle_bytes`, tenantID, int64(sequence), int64(catalogVersion), int64(revokeEpoch), contentHash, raw).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) {
		var currentSequence int64
		var currentHash []byte
		err = tx.QueryRow(ctx, `SELECT bundle_sequence,content_hash,bundle_bytes
			FROM projection_authorization_policy_bundle_state WHERE tenant_id=$1`, tenantID).
			Scan(&currentSequence, &currentHash, &latest)
		if err == nil && uint64(currentSequence) == sequence && !bytes.Equal(currentHash, contentHash) {
			err = errors.New("authorization bundle sequence was reused with different content")
		}
		if err == nil && uint64(currentSequence) < sequence {
			err = errors.New("authorization bundle metadata regressed against persisted state")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("persist authorization bundle: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit authorization bundle: %w", err)
	}
	return latest, nil
}
