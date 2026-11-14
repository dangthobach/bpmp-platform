BEGIN;

ALTER TABLE work_items
    DROP CONSTRAINT IF EXISTS work_items_tenant_id_completion_command_id_key;
ALTER TABLE work_items
    ADD CONSTRAINT work_items_tenant_id_completion_command_id_key
    UNIQUE (tenant_id, completion_command_id);

COMMIT;
