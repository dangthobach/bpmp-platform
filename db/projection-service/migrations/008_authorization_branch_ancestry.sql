-- Canonical organization scope captured in the committed workflow-start event.
-- Empty ancestry / revision 0 denotes legacy data and is not eligible for
-- branch-scoped authorization.
ALTER TABLE workflow_instance_read_models
    ADD COLUMN IF NOT EXISTS resource_branch_ancestry TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS organization_version BIGINT NOT NULL DEFAULT 0;

ALTER TABLE workflow_instance_read_models
    ADD CONSTRAINT workflow_instance_branch_ancestry_revision_check
    CHECK (
        (cardinality(resource_branch_ancestry) = 0 AND organization_version = 0)
        OR
        (resource_branch_id IS NOT NULL
         AND cardinality(resource_branch_ancestry) > 0
         AND resource_branch_ancestry[cardinality(resource_branch_ancestry)] = resource_branch_id
         AND organization_version > 0)
    ) NOT VALID;

CREATE INDEX IF NOT EXISTS idx_workflow_instances_branch_ancestry_gin
    ON workflow_instance_read_models USING GIN (resource_branch_ancestry);

CREATE OR REPLACE FUNCTION projection_read_model_immutable_identity()
RETURNS trigger AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id
       OR NEW.instance_id <> OLD.instance_id
       OR NEW.resource_branch_id IS DISTINCT FROM OLD.resource_branch_id
       OR NEW.resource_branch_ancestry IS DISTINCT FROM OLD.resource_branch_ancestry
       OR NEW.organization_version IS DISTINCT FROM OLD.organization_version THEN
        RAISE EXCEPTION 'projection read-model identity and authorization scope are immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
