ALTER TABLE query_revoke_feed ADD COLUMN event_id UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX query_revoke_feed_event_id ON query_revoke_feed(event_id);

-- A technical delivery cursor, separate from immutable security/audit events.
CREATE TABLE query_revoke_publish_state (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    checkpoint BIGINT NOT NULL DEFAULT 0 CHECK (checkpoint >= 0),
    lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_owner TEXT,
    lease_until TIMESTAMPTZ,
    failure_count BIGINT NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO query_revoke_publish_state(singleton) VALUES(true);
