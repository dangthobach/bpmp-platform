BEGIN;

CREATE TABLE human_admin_command_dedup (
    tenant_id text NOT NULL,
    idempotency_key text NOT NULL,
    command_id text NOT NULL,
    payload_digest bytea NOT NULL CHECK (octet_length(payload_digest) = 32),
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_key),
    UNIQUE (tenant_id, command_id)
);

CREATE OR REPLACE FUNCTION reject_human_admin_dedup_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'human admin command deduplication records are immutable';
END;
$$;

CREATE TRIGGER human_admin_command_dedup_immutable
BEFORE UPDATE OR DELETE ON human_admin_command_dedup
FOR EACH ROW EXECUTE FUNCTION reject_human_admin_dedup_mutation();

COMMIT;
