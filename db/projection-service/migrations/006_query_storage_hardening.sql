DO $$
BEGIN
    IF coalesce(pg_get_constraintdef(
        (SELECT oid
         FROM pg_constraint
         WHERE conrelid = 'projection_saved_queries'::regclass
           AND contype = 'p')
    ), '') <> 'PRIMARY KEY (tenant_id, owner_subject, id)' THEN
        ALTER TABLE projection_saved_queries
            DROP CONSTRAINT projection_saved_queries_pkey;
        ALTER TABLE projection_saved_queries
            ADD CONSTRAINT projection_saved_queries_pkey
            PRIMARY KEY (tenant_id, owner_subject, id);
    END IF;
END $$;

ALTER TABLE projection_query_exports
    DROP CONSTRAINT IF EXISTS projection_query_exports_max_rows_check;
ALTER TABLE projection_query_exports
    ADD CONSTRAINT projection_query_exports_max_rows_check
    CHECK (max_rows BETWEEN 1 AND 100000);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'projection_saved_queries'::regclass
          AND conname = 'projection_saved_queries_name_trimmed_check'
    ) THEN
        ALTER TABLE projection_saved_queries
            ADD CONSTRAINT projection_saved_queries_name_trimmed_check
            CHECK (length(btrim(name)) > 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'projection_saved_queries'::regclass
          AND conname = 'projection_saved_queries_query_object_check'
    ) THEN
        ALTER TABLE projection_saved_queries
            ADD CONSTRAINT projection_saved_queries_query_object_check
            CHECK (jsonb_typeof(query_json) = 'object');
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'projection_query_exports'::regclass
          AND conname = 'projection_query_exports_status_check'
    ) THEN
        ALTER TABLE projection_query_exports
            ADD CONSTRAINT projection_query_exports_status_check
            CHECK (status IN (1, 2, 3, 4, 5));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'projection_query_exports'::regclass
          AND conname = 'projection_query_exports_query_object_check'
    ) THEN
        ALTER TABLE projection_query_exports
            ADD CONSTRAINT projection_query_exports_query_object_check
            CHECK (jsonb_typeof(query_json) = 'object');
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'projection_query_exports'::regclass
          AND conname = 'projection_query_exports_csv_size_check'
    ) THEN
        ALTER TABLE projection_query_exports
            ADD CONSTRAINT projection_query_exports_csv_size_check
            CHECK (csv IS NULL OR octet_length(csv) <= 16777216);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'projection_query_exports'::regclass
          AND conname = 'projection_query_exports_expiry_check'
    ) THEN
        ALTER TABLE projection_query_exports
            ADD CONSTRAINT projection_query_exports_expiry_check
            CHECK (expires_at > created_at);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'workflow_instance_read_models'::regclass
          AND conname = 'workflow_instances_updated_after_started_check'
    ) THEN
        ALTER TABLE workflow_instance_read_models
            ADD CONSTRAINT workflow_instances_updated_after_started_check
            CHECK (updated_at >= started_at) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'workflow_instance_read_models'::regclass
          AND conname = 'workflow_instances_completed_after_started_check'
    ) THEN
        ALTER TABLE workflow_instance_read_models
            ADD CONSTRAINT workflow_instances_completed_after_started_check
            CHECK (completed_at IS NULL OR completed_at >= started_at) NOT VALID;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS projection_query_exports_pending_worker_idx
    ON projection_query_exports (created_at, tenant_id, id)
    WHERE status = 1;
CREATE INDEX IF NOT EXISTS projection_query_exports_reclaim_worker_idx
    ON projection_query_exports (updated_at, created_at, tenant_id, id)
    WHERE status = 5;
CREATE INDEX IF NOT EXISTS projection_query_exports_expiry_idx
    ON projection_query_exports (expires_at);
