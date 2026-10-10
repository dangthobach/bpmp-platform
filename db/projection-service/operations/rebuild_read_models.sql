-- Run only while every projection-service consumer using this database is stopped.
-- The caller must reset the Kafka consumer group offsets before restarting.
BEGIN;

SELECT pg_advisory_xact_lock(hashtext('bpmp.projection.rebuild.v1'));

TRUNCATE TABLE
    workflow_event_read_models,
    workflow_instance_read_models,
    projection_event_inbox,
    projection_checkpoints;

COMMIT;
