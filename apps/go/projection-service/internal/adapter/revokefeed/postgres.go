package revokefeed

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"time"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("revoke store pool required")
	}
	return &PostgresStore{pool: pool}, nil
}
func checkpoint(ctx context.Context, tx pgx.Tx, r *kgo.Record) error {
	_, err := tx.Exec(ctx, `INSERT INTO query_revoke_checkpoints(topic,partition,last_offset) VALUES($1,$2,$3) ON CONFLICT(topic,partition) DO UPDATE SET last_offset=GREATEST(query_revoke_checkpoints.last_offset,EXCLUDED.last_offset)`, r.Topic, r.Partition, r.Offset)
	return err
}
func (s *PostgresStore) Apply(ctx context.Context, r *kgo.Record, event *authorizationv1.AuthorizationQueryRevokeEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize updates even when a publisher replay or partition change races a newer epoch.
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, event.TenantId+"/"+event.ActorId)
	if err != nil {
		return err
	}
	var epoch, sequence int64
	var revoked bool
	var eventID string
	err = tx.QueryRow(ctx, `SELECT revoke_epoch,event_sequence,revoked,event_id FROM query_actor_revoke_epochs WHERE tenant_id=$1 AND actor_id=$2`, event.TenantId, event.ActorId).Scan(&epoch, &sequence, &revoked, &eventID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, `INSERT INTO query_actor_revoke_epochs(tenant_id,actor_id,revoke_epoch,event_sequence,revoked,event_id) VALUES($1,$2,$3,$4,$5,$6)`, event.TenantId, event.ActorId, int64(event.RevokeEpoch), int64(event.EventSequence), event.Revoked, event.EventId)
	case err != nil:
		return err
	case int64(event.EventSequence) == sequence:
		if int64(event.RevokeEpoch) != epoch || event.Revoked != revoked || event.EventId != eventID {
			return ErrInvalidEvent
		}
	case int64(event.EventSequence) > sequence:
		if int64(event.RevokeEpoch) <= epoch {
			return ErrInvalidEvent
		}
		_, err = tx.Exec(ctx, `UPDATE query_actor_revoke_epochs SET revoke_epoch=$3,event_sequence=$4,revoked=$5,event_id=$6 WHERE tenant_id=$1 AND actor_id=$2`, event.TenantId, event.ActorId, int64(event.RevokeEpoch), int64(event.EventSequence), event.Revoked, event.EventId)
	default:
		if int64(event.RevokeEpoch) >= epoch {
			return ErrInvalidEvent
		}
	}
	if err != nil {
		return err
	}
	if err = checkpoint(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) Quarantine(ctx context.Context, r *kgo.Record, reason string) (*authorizationv1.AuthorizationQueryRevokeDeadLetter, error) {
	hash := sha256.Sum256(r.Value)
	id := IncidentID(r)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO query_revoke_incidents(incident_id,source_topic,source_partition,source_offset,source_sha256,source_size,reason_code) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, id, r.Topic, r.Partition, r.Offset, hash[:], len(r.Value), reason)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO query_revoke_dlq_state(incident_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return nil, err
	}
	var created time.Time
	var digest []byte
	var size int64
	err = tx.QueryRow(ctx, `SELECT created_at,source_sha256,source_size,reason_code FROM query_revoke_incidents WHERE incident_id=$1`, id).Scan(&created, &digest, &size, &reason)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &authorizationv1.AuthorizationQueryRevokeDeadLetter{SchemaVersion: 1, IncidentId: id, SourceTopic: r.Topic, SourcePartition: r.Partition, SourceOffset: r.Offset, SourceSha256: digest, SourceSizeBytes: uint64(size), ReasonCode: reason, QuarantinedAtEpochMs: uint64(created.UnixMilli())}, nil
}
func (s *PostgresStore) AcknowledgeDLQ(ctx context.Context, r *kgo.Record, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE query_revoke_dlq_state SET acknowledged_at=COALESCE(acknowledged_at,now()) WHERE incident_id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("DLQ incident absent")
	}
	if err = checkpoint(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) Check(ctx context.Context, tenant, actor string, tokenEpoch uint64) error {
	var fenced bool
	var epoch int64
	var revoked bool
	err := s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT unresolved_incidents>0 FROM query_revoke_security_state WHERE singleton),true),COALESCE((SELECT revoke_epoch FROM query_actor_revoke_epochs WHERE tenant_id=$1 AND actor_id=$2),0),COALESCE((SELECT revoked FROM query_actor_revoke_epochs WHERE tenant_id=$1 AND actor_id=$2),false)`, tenant, actor).Scan(&fenced, &epoch, &revoked)
	if err != nil {
		return err
	}
	if fenced || revoked || tokenEpoch < uint64(epoch) {
		return application.ErrQueryForbidden
	}
	return nil
}
