package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("projection PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Apply(
	ctx context.Context,
	position application.KafkaPosition,
	event application.InstanceEvent,
) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, `INSERT INTO projection_event_inbox
		(tenant_id,consumer_name,event_id,topic,partition_id,offset_value,instance_id,event_sequence,processed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (tenant_id,consumer_name,event_id) DO NOTHING`,
		event.TenantID, position.ConsumerName, event.EventID, position.Topic, position.Partition,
		position.Offset, event.InstanceID, event.Sequence, event.OccurredAt)
	if err != nil {
		return false, err
	}
	duplicate := result.RowsAffected() == 0
	if !duplicate {
		if err = s.applyInstance(ctx, tx, event); err != nil {
			return false, err
		}
	}
	if err = updateCheckpoint(ctx, tx, position, event.OccurredAt); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return duplicate, nil
}

func (s *Store) applyInstance(
	ctx context.Context,
	tx pgx.Tx,
	event application.InstanceEvent,
) error {
	var current uint64
	err := tx.QueryRow(ctx, `SELECT last_event_sequence
		FROM workflow_instance_read_models
		WHERE tenant_id=$1 AND instance_id=$2
		FOR UPDATE`, event.TenantID, event.InstanceID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		if event.Kind != application.EventStarted {
			return fmt.Errorf("%w: instance %s starts at sequence %d", application.ErrSequenceGap, event.InstanceID, event.Sequence)
		}
		_, err = tx.Exec(ctx, `INSERT INTO workflow_instance_read_models
			(tenant_id,instance_id,workflow_type,workflow_version,status,active_node_id,last_event_sequence,
			 started_at,updated_at,config_version,policy_version)
			VALUES($1,$2,$3,$4,'ACTIVE',$5,$6,$7,$7,$8,$9)`,
			event.TenantID, event.InstanceID, event.WorkflowType, event.WorkflowVersion,
			event.NodeID, event.Sequence, event.OccurredAt, event.ConfigVersion, event.PolicyVersion)
		return err
	}
	if err != nil {
		return err
	}
	if event.Sequence <= current {
		return nil
	}
	if event.Sequence != current+1 {
		return fmt.Errorf("%w: instance %s expected %d, got %d",
			application.ErrSequenceGap, event.InstanceID, current+1, event.Sequence)
	}

	status := ""
	activeNodeID := ""
	updateActiveNode := false
	var completedAt *time.Time
	switch event.Kind {
	case application.EventCompleted:
		status = "COMPLETED"
		updateActiveNode = true
		completedAt = &event.OccurredAt
	case application.EventTerminatedForCompliance:
		status = "TERMINATED_FOR_COMPLIANCE"
		updateActiveNode = true
		completedAt = &event.OccurredAt
	case application.EventNodeActivated:
		activeNodeID = event.NodeID
		updateActiveNode = true
	case application.EventProgress, application.EventStarted:
	}
	_, err = tx.Exec(ctx, `UPDATE workflow_instance_read_models
		SET status=CASE WHEN $3='' THEN status ELSE $3 END,
		    active_node_id=CASE WHEN $4 THEN $5 ELSE active_node_id END,
		    last_event_sequence=$6,updated_at=$7,
		    completed_at=COALESCE($8,completed_at),
		    config_version=$9,policy_version=$10,version=version+1
		WHERE tenant_id=$1 AND instance_id=$2`,
		event.TenantID, event.InstanceID, status, updateActiveNode, activeNodeID, event.Sequence,
		event.OccurredAt, completedAt, event.ConfigVersion, event.PolicyVersion)
	return err
}

func updateCheckpoint(
	ctx context.Context,
	tx pgx.Tx,
	position application.KafkaPosition,
	eventTimestamp time.Time,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO projection_checkpoints
		(consumer_name,topic,partition_id,offset_value,event_timestamp,updated_at)
		VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (consumer_name,topic,partition_id) DO UPDATE
		SET offset_value=EXCLUDED.offset_value,event_timestamp=EXCLUDED.event_timestamp,updated_at=now()
		WHERE projection_checkpoints.offset_value < EXCLUDED.offset_value`,
		position.ConsumerName, position.Topic, position.Partition, position.Offset, eventTimestamp)
	return err
}

func (s *Store) Get(ctx context.Context, tenantID, instanceID string) (application.Instance, error) {
	instance, err := scanInstance(s.pool.QueryRow(ctx, instanceSelect+`
		WHERE tenant_id=$1 AND instance_id=$2`, tenantID, instanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Instance{}, application.ErrNotFound
	}
	return instance, err
}

func (s *Store) List(
	ctx context.Context,
	filter application.ListFilter,
) ([]application.Instance, error) {
	var cursorTime any
	cursorID := ""
	if filter.After != nil {
		cursorTime = filter.After.UpdatedAt
		cursorID = filter.After.InstanceID
	}
	rows, err := s.pool.Query(ctx, instanceSelect+`
		WHERE tenant_id=$1
		  AND ($2='' OR workflow_type=$2)
		  AND (cardinality($3::text[])=0 OR status=ANY($3))
		  AND ($4 OR NOT is_deleted)
		  AND ($5::timestamptz IS NULL OR (updated_at,instance_id) < ($5,CAST($6 AS text)))
		ORDER BY updated_at DESC,instance_id DESC
		LIMIT $7`,
		filter.TenantID, filter.WorkflowType, filter.Statuses, filter.IncludeDeleted,
		cursorTime, cursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	instances := make([]application.Instance, 0, filter.Limit)
	for rows.Next() {
		instance, scanErr := scanInstance(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		instances = append(instances, instance)
	}
	return instances, rows.Err()
}

const instanceSelect = `SELECT tenant_id,instance_id,workflow_type,workflow_version,status,
	active_node_id,last_event_sequence,started_at,updated_at,completed_at,
	config_version,policy_version,is_deleted,version
	FROM workflow_instance_read_models`

type rowScanner interface{ Scan(...any) error }

func scanInstance(row rowScanner) (application.Instance, error) {
	var instance application.Instance
	err := row.Scan(
		&instance.TenantID, &instance.InstanceID, &instance.WorkflowType,
		&instance.WorkflowVersion, &instance.Status, &instance.ActiveNodeID,
		&instance.LastEventSequence, &instance.StartedAt, &instance.UpdatedAt,
		&instance.CompletedAt, &instance.ConfigVersion, &instance.PolicyVersion,
		&instance.IsDeleted, &instance.Version,
	)
	return instance, err
}

var _ application.Store = (*Store)(nil)
