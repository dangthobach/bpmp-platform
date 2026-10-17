-- Expression indexes for the most common case-insensitive business lookup.
-- Other transform/field combinations remain valid but must earn an index from
-- representative plan evidence because every additional index increases
-- projection write amplification.
CREATE INDEX IF NOT EXISTS idx_workflow_instances_completed_type_trim_lower
    ON workflow_instance_read_models
        (tenant_id, (LOWER(BTRIM(workflow_type))), completed_at DESC, instance_id ASC)
    WHERE status = 'COMPLETED' AND NOT is_deleted;

CREATE INDEX IF NOT EXISTS idx_workflow_events_type_trim_lower_instance
    ON workflow_event_read_models
        (tenant_id, (LOWER(BTRIM(event_type))), instance_id)
    WHERE NOT is_deleted;
