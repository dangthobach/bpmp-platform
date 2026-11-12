-- The branch binding is captured from the committed WorkflowStarted event.
-- It is process scope metadata, not a client supplied query filter.
ALTER TABLE workflow_instance_read_models
    ADD COLUMN IF NOT EXISTS resource_branch_id TEXT;

CREATE INDEX IF NOT EXISTS idx_workflow_instances_tenant_branch_updated
    ON workflow_instance_read_models (tenant_id, resource_branch_id, updated_at DESC, instance_id DESC)
    WHERE NOT is_deleted AND resource_branch_id IS NOT NULL;

CREATE OR REPLACE FUNCTION projection_read_model_immutable_identity()
RETURNS trigger AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id
       OR NEW.instance_id <> OLD.instance_id
       OR NEW.resource_branch_id IS DISTINCT FROM OLD.resource_branch_id THEN
        RAISE EXCEPTION 'projection read-model identity and resource branch are immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
