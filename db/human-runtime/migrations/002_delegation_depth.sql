BEGIN;

ALTER TABLE work_items
    ADD COLUMN IF NOT EXISTS delegation_depth integer NOT NULL DEFAULT 0;

ALTER TABLE work_items
    DROP CONSTRAINT IF EXISTS work_items_delegation_depth_check;

ALTER TABLE work_items
    ADD CONSTRAINT work_items_delegation_depth_check
    CHECK (delegation_depth >= 0);

COMMIT;
