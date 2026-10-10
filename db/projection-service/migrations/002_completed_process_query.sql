CREATE TABLE IF NOT EXISTS workflow_event_read_models (
    tenant_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    event_sequence BIGINT NOT NULL CHECK (event_sequence > 0),
    event_type TEXT NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    config_version TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (tenant_id, event_id),
    UNIQUE (tenant_id, instance_id, event_sequence),
    FOREIGN KEY (tenant_id, instance_id)
        REFERENCES workflow_instance_read_models (tenant_id, instance_id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_workflow_events_instance_sequence
    ON workflow_event_read_models (tenant_id, instance_id, event_sequence);

CREATE INDEX IF NOT EXISTS idx_workflow_events_type_instance
    ON workflow_event_read_models (tenant_id, event_type, instance_id)
    WHERE NOT is_deleted;

CREATE INDEX IF NOT EXISTS idx_workflow_events_node_instance
    ON workflow_event_read_models (tenant_id, node_id, instance_id)
    WHERE NOT is_deleted AND node_id <> '';

CREATE INDEX IF NOT EXISTS idx_workflow_events_actor_instance
    ON workflow_event_read_models (tenant_id, actor_id, instance_id)
    WHERE NOT is_deleted AND actor_id <> '';

CREATE INDEX IF NOT EXISTS idx_workflow_instances_completed_at
    ON workflow_instance_read_models
        (tenant_id, completed_at DESC, instance_id ASC)
    WHERE status = 'COMPLETED' AND NOT is_deleted;

CREATE INDEX IF NOT EXISTS idx_workflow_instances_completed_type
    ON workflow_instance_read_models
        (tenant_id, workflow_type, workflow_version, completed_at DESC, instance_id ASC)
    WHERE status = 'COMPLETED' AND NOT is_deleted;

