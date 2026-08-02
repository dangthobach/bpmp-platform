BEGIN;

ALTER TABLE configuration_outbox
    ADD COLUMN IF NOT EXISTS request_id text,
    ADD COLUMN IF NOT EXISTS correlation_id text,
    ADD COLUMN IF NOT EXISTS command_id text,
    ADD COLUMN IF NOT EXISTS trace_parent text,
    ADD COLUMN IF NOT EXISTS trace_state text;

UPDATE configuration_outbox
SET request_id = COALESCE(request_id, event_id::text),
    correlation_id = COALESCE(correlation_id, event_id::text),
    command_id = COALESCE(command_id, event_id::text)
WHERE request_id IS NULL OR correlation_id IS NULL OR command_id IS NULL;

ALTER TABLE configuration_outbox ALTER COLUMN request_id SET NOT NULL;
ALTER TABLE configuration_outbox ALTER COLUMN correlation_id SET NOT NULL;
ALTER TABLE configuration_outbox ALTER COLUMN command_id SET NOT NULL;

ALTER TABLE configuration_outbox
    DROP CONSTRAINT IF EXISTS configuration_outbox_request_id_check,
    DROP CONSTRAINT IF EXISTS configuration_outbox_correlation_id_check,
    DROP CONSTRAINT IF EXISTS configuration_outbox_command_id_check,
    DROP CONSTRAINT IF EXISTS configuration_outbox_trace_parent_check,
    DROP CONSTRAINT IF EXISTS configuration_outbox_trace_state_check;
ALTER TABLE configuration_outbox
    ADD CONSTRAINT configuration_outbox_request_id_check
        CHECK (request_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT configuration_outbox_correlation_id_check
        CHECK (correlation_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT configuration_outbox_command_id_check
        CHECK (command_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT configuration_outbox_trace_parent_check
        CHECK (trace_parent IS NULL OR trace_parent ~ '^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$'),
    ADD CONSTRAINT configuration_outbox_trace_state_check
        CHECK (trace_state IS NULL OR (octet_length(trace_state) <= 512 AND trace_state ~ '^[ -~]*$'));

ALTER TABLE tenant_configuration_readiness_outbox
    ADD COLUMN IF NOT EXISTS request_id text,
    ADD COLUMN IF NOT EXISTS correlation_id text,
    ADD COLUMN IF NOT EXISTS command_id text,
    ADD COLUMN IF NOT EXISTS trace_parent text,
    ADD COLUMN IF NOT EXISTS trace_state text;

UPDATE tenant_configuration_readiness_outbox
SET request_id = COALESCE(request_id, event_id::text),
    correlation_id = COALESCE(correlation_id, event_id::text),
    command_id = COALESCE(command_id, event_id::text)
WHERE request_id IS NULL OR correlation_id IS NULL OR command_id IS NULL;

ALTER TABLE tenant_configuration_readiness_outbox ALTER COLUMN request_id SET NOT NULL;
ALTER TABLE tenant_configuration_readiness_outbox ALTER COLUMN correlation_id SET NOT NULL;
ALTER TABLE tenant_configuration_readiness_outbox ALTER COLUMN command_id SET NOT NULL;

ALTER TABLE tenant_configuration_readiness_outbox
	DROP CONSTRAINT IF EXISTS tenant_readiness_outbox_request_id_check,
	DROP CONSTRAINT IF EXISTS tenant_readiness_outbox_correlation_id_check,
	DROP CONSTRAINT IF EXISTS tenant_readiness_outbox_command_id_check,
	DROP CONSTRAINT IF EXISTS tenant_readiness_outbox_trace_parent_check,
	DROP CONSTRAINT IF EXISTS tenant_readiness_outbox_trace_state_check;
ALTER TABLE tenant_configuration_readiness_outbox
    ADD CONSTRAINT tenant_readiness_outbox_request_id_check
        CHECK (request_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT tenant_readiness_outbox_correlation_id_check
        CHECK (correlation_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT tenant_readiness_outbox_command_id_check
        CHECK (command_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT tenant_readiness_outbox_trace_parent_check
        CHECK (trace_parent IS NULL OR trace_parent ~ '^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$'),
    ADD CONSTRAINT tenant_readiness_outbox_trace_state_check
        CHECK (trace_state IS NULL OR (octet_length(trace_state) <= 512 AND trace_state ~ '^[ -~]*$'));

COMMIT;
