BEGIN;

ALTER TABLE configuration_profiles
    ADD COLUMN IF NOT EXISTS owner text;
UPDATE configuration_profiles SET owner = 'ENGINE' WHERE owner IS NULL;
ALTER TABLE configuration_profiles ALTER COLUMN owner SET NOT NULL;
ALTER TABLE configuration_profiles
    DROP CONSTRAINT IF EXISTS configuration_profiles_owner_check;
ALTER TABLE configuration_profiles
    ADD CONSTRAINT configuration_profiles_owner_check
    CHECK (owner IN ('ENGINE','API_GATEWAY','HUMAN_RUNTIME','PROJECTION','GOVERNANCE'));
ALTER TABLE configuration_profiles
    DROP CONSTRAINT IF EXISTS configuration_profiles_tenant_id_name_key;
ALTER TABLE configuration_profiles
    ADD CONSTRAINT configuration_profiles_tenant_owner_name_key
    UNIQUE (tenant_id, owner, name);

ALTER TABLE configuration_active_scopes
    ADD COLUMN IF NOT EXISTS owner text;
UPDATE configuration_active_scopes SET owner = 'ENGINE' WHERE owner IS NULL;
ALTER TABLE configuration_active_scopes ALTER COLUMN owner SET NOT NULL;
ALTER TABLE configuration_active_scopes
    DROP CONSTRAINT IF EXISTS configuration_active_scopes_owner_check;
ALTER TABLE configuration_active_scopes
    ADD CONSTRAINT configuration_active_scopes_owner_check
    CHECK (owner IN ('ENGINE','API_GATEWAY','HUMAN_RUNTIME','PROJECTION','GOVERNANCE'));
ALTER TABLE configuration_active_scopes
    DROP CONSTRAINT IF EXISTS configuration_active_scopes_pkey;
ALTER TABLE configuration_active_scopes
    ADD PRIMARY KEY (tenant_id, owner, scope_type, scope_reference);

CREATE TABLE IF NOT EXISTS configuration_outbox_publish_state (
    singleton_id smallint PRIMARY KEY CHECK (singleton_id = 1),
    next_sequence bigint NOT NULL DEFAULT 0 CHECK (next_sequence >= 0),
    checkpoint bigint NOT NULL DEFAULT 0 CHECK (checkpoint >= 0),
    lease_owner text,
    lease_until timestamptz,
    updated_at timestamptz NOT NULL
);

ALTER TABLE configuration_outbox
    ADD COLUMN IF NOT EXISTS event_sequence bigint,
    ADD COLUMN IF NOT EXISTS lease_owner text,
    ADD COLUMN IF NOT EXISTS lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS last_error text;

WITH ordered AS (
    SELECT event_id, row_number() OVER (ORDER BY occurred_at, event_id) AS sequence
    FROM configuration_outbox
    WHERE event_sequence IS NULL
)
UPDATE configuration_outbox AS target
SET event_sequence = ordered.sequence
FROM ordered
WHERE target.event_id = ordered.event_id;

ALTER TABLE configuration_outbox ALTER COLUMN event_sequence SET NOT NULL;
ALTER TABLE configuration_outbox
    DROP CONSTRAINT IF EXISTS configuration_outbox_event_sequence_key;
ALTER TABLE configuration_outbox
    ADD CONSTRAINT configuration_outbox_event_sequence_key UNIQUE (event_sequence);
ALTER TABLE configuration_outbox
    DROP CONSTRAINT IF EXISTS configuration_outbox_event_sequence_check;
ALTER TABLE configuration_outbox
    ADD CONSTRAINT configuration_outbox_event_sequence_check CHECK (event_sequence > 0);

INSERT INTO configuration_outbox_publish_state(
    singleton_id,
    next_sequence,
    checkpoint,
    updated_at
)
SELECT
    1,
    COALESCE(max(event_sequence), 0),
    COALESCE(
        min(event_sequence) FILTER (WHERE published_at IS NULL) - 1,
        max(event_sequence),
        0
    ),
    statement_timestamp()
FROM configuration_outbox
ON CONFLICT (singleton_id) DO UPDATE
SET next_sequence = GREATEST(
        configuration_outbox_publish_state.next_sequence,
        EXCLUDED.next_sequence
    ),
    checkpoint = GREATEST(
        configuration_outbox_publish_state.checkpoint,
        EXCLUDED.checkpoint
    ),
    updated_at = EXCLUDED.updated_at;

DROP INDEX IF EXISTS configuration_outbox_pending_idx;
CREATE INDEX configuration_outbox_pending_idx
    ON configuration_outbox (event_sequence, next_attempt_at)
    WHERE published_at IS NULL;

COMMIT;
