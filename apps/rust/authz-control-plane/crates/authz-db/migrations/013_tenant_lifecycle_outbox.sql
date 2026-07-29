ALTER TABLE tenant
    ALTER COLUMN is_active SET DEFAULT false;

CREATE TABLE tenant_configuration_readiness (
    tenant_id          UUID        PRIMARY KEY REFERENCES tenant(id) ON DELETE RESTRICT,
    tenant_version     BIGINT      NOT NULL CHECK (tenant_version >= 0),
    ready              BOOLEAN     NOT NULL,
    profile_set_hash   BYTEA       NOT NULL CHECK (octet_length(profile_set_hash) = 32),
    event_id           UUID        NOT NULL UNIQUE,
    event_sequence     BIGINT      NOT NULL UNIQUE CHECK (event_sequence > 0),
    updated_at         TIMESTAMPTZ NOT NULL
);

CREATE SEQUENCE tenant_lifecycle_event_sequence;

CREATE TABLE tenant_lifecycle_outbox (
    event_id           UUID        PRIMARY KEY,
    event_sequence     BIGINT      NOT NULL UNIQUE CHECK (event_sequence > 0),
    tenant_id          UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    tenant_code        VARCHAR(50) NOT NULL,
    tenant_version     BIGINT      NOT NULL CHECK (tenant_version >= 0),
    lifecycle_kind     VARCHAR(32) NOT NULL CHECK (lifecycle_kind IN (
        'CREATED',
        'UPDATED',
        'ACTIVATION_REQUESTED',
        'ACTIVATED',
        'SUSPENDED',
        'DELETED'
    )),
    actor_ref          TEXT        NOT NULL CHECK (length(trim(actor_ref)) > 0),
    correlation_id     TEXT        NOT NULL CHECK (length(trim(correlation_id)) > 0),
    payload            JSONB       NOT NULL,
    occurred_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at       TIMESTAMPTZ,
    attempt_count      INTEGER     NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lease_owner        TEXT,
    lease_until        TIMESTAMPTZ,
    last_error         TEXT
);

CREATE INDEX idx_tenant_lifecycle_outbox_pending
    ON tenant_lifecycle_outbox(event_sequence, next_attempt_at)
    WHERE published_at IS NULL;

CREATE TABLE tenant_lifecycle_publish_state (
    singleton_id    SMALLINT    PRIMARY KEY CHECK (singleton_id = 1),
    checkpoint      BIGINT      NOT NULL DEFAULT 0 CHECK (checkpoint >= 0),
    lease_owner     TEXT,
    lease_until     TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO tenant_lifecycle_publish_state(singleton_id)
VALUES(1);
