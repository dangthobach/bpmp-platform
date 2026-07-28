CREATE TABLE IF NOT EXISTS projection_event_inbox (
    tenant_id TEXT NOT NULL,
    consumer_name TEXT NOT NULL,
    event_id TEXT NOT NULL,
    topic TEXT NOT NULL,
    partition_id INTEGER NOT NULL,
    offset_value BIGINT NOT NULL,
    instance_id TEXT NOT NULL,
    event_sequence BIGINT NOT NULL CHECK (event_sequence > 0),
    processed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, consumer_name, event_id)
);

CREATE TABLE IF NOT EXISTS projection_checkpoints (
    consumer_name TEXT NOT NULL,
    topic TEXT NOT NULL,
    partition_id INTEGER NOT NULL,
    offset_value BIGINT NOT NULL CHECK (offset_value >= 0),
    event_timestamp TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (consumer_name, topic, partition_id)
);

CREATE TABLE IF NOT EXISTS workflow_instance_read_models (
    tenant_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    workflow_type TEXT NOT NULL,
    workflow_version TEXT NOT NULL,
    status TEXT NOT NULL,
    active_node_id TEXT NOT NULL DEFAULT '',
    last_event_sequence BIGINT NOT NULL CHECK (last_event_sequence > 0),
    started_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    config_version TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (tenant_id, instance_id)
);

CREATE INDEX IF NOT EXISTS idx_workflow_instances_tenant_updated
    ON workflow_instance_read_models (tenant_id, updated_at DESC, instance_id DESC)
    WHERE NOT is_deleted;

CREATE INDEX IF NOT EXISTS idx_workflow_instances_tenant_status_updated
    ON workflow_instance_read_models (tenant_id, status, updated_at DESC, instance_id DESC)
    WHERE NOT is_deleted;

CREATE INDEX IF NOT EXISTS idx_workflow_instances_tenant_type_updated
    ON workflow_instance_read_models (tenant_id, workflow_type, updated_at DESC, instance_id DESC)
    WHERE NOT is_deleted;

CREATE OR REPLACE FUNCTION projection_read_model_immutable_identity()
RETURNS trigger AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.instance_id <> OLD.instance_id THEN
        RAISE EXCEPTION 'projection read-model identity is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS projection_read_model_identity_guard ON workflow_instance_read_models;
CREATE TRIGGER projection_read_model_identity_guard
BEFORE UPDATE ON workflow_instance_read_models
FOR EACH ROW EXECUTE FUNCTION projection_read_model_immutable_identity();
