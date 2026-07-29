ALTER TABLE configuration_profiles
    DROP CONSTRAINT IF EXISTS configuration_profiles_owner_check;
ALTER TABLE configuration_profiles
    ADD CONSTRAINT configuration_profiles_owner_check CHECK (owner IN (
        'ENGINE',
        'API_GATEWAY',
        'HUMAN_RUNTIME',
        'PROJECTION',
        'GOVERNANCE',
        'CONFIGURATION_SERVICE',
        'COCKPIT_GATEWAY',
        'AUTHZ_CONTROL_PLANE'
    ));

ALTER TABLE configuration_active_scopes
    DROP CONSTRAINT IF EXISTS configuration_active_scopes_owner_check;
ALTER TABLE configuration_active_scopes
    ADD CONSTRAINT configuration_active_scopes_owner_check CHECK (owner IN (
        'ENGINE',
        'API_GATEWAY',
        'HUMAN_RUNTIME',
        'PROJECTION',
        'GOVERNANCE',
        'CONFIGURATION_SERVICE',
        'COCKPIT_GATEWAY',
        'AUTHZ_CONTROL_PLANE'
    ));

CREATE TABLE IF NOT EXISTS configuration_activation_requirements (
    owner text PRIMARY KEY,
    is_required boolean NOT NULL,
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at timestamptz NOT NULL,
    updated_by text NOT NULL,
    CHECK (owner IN (
        'ENGINE',
        'API_GATEWAY',
        'HUMAN_RUNTIME',
        'PROJECTION',
        'GOVERNANCE',
        'CONFIGURATION_SERVICE',
        'COCKPIT_GATEWAY',
        'AUTHZ_CONTROL_PLANE'
    )),
    CHECK (length(trim(updated_by)) > 0)
);

INSERT INTO configuration_activation_requirements(owner, is_required, updated_at, updated_by)
VALUES
    ('ENGINE', true, statement_timestamp(), 'migration'),
    ('API_GATEWAY', true, statement_timestamp(), 'migration'),
    ('HUMAN_RUNTIME', true, statement_timestamp(), 'migration'),
    ('PROJECTION', true, statement_timestamp(), 'migration'),
    ('GOVERNANCE', true, statement_timestamp(), 'migration'),
    ('CONFIGURATION_SERVICE', true, statement_timestamp(), 'migration'),
    ('COCKPIT_GATEWAY', true, statement_timestamp(), 'migration'),
    ('AUTHZ_CONTROL_PLANE', true, statement_timestamp(), 'migration')
ON CONFLICT (owner) DO NOTHING;

CREATE TABLE IF NOT EXISTS tenant_configuration_readiness (
    tenant_id text PRIMARY KEY,
    tenant_version bigint NOT NULL CHECK (tenant_version >= 0),
    lifecycle_event_sequence bigint NOT NULL CHECK (lifecycle_event_sequence > 0),
    ready boolean NOT NULL,
    missing_owners text[] NOT NULL,
    profile_set_hash bytea NOT NULL CHECK (octet_length(profile_set_hash) = 32),
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at timestamptz NOT NULL,
    CHECK (length(trim(tenant_id)) > 0)
);

CREATE TABLE IF NOT EXISTS tenant_configuration_readiness_outbox (
    event_id uuid PRIMARY KEY,
    event_sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    tenant_id text NOT NULL,
    tenant_version bigint NOT NULL CHECK (tenant_version >= 0),
    ready boolean NOT NULL,
    missing_owners text[] NOT NULL,
    profile_set_hash bytea NOT NULL CHECK (octet_length(profile_set_hash) = 32),
    occurred_at timestamptz NOT NULL,
    published_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL,
    lease_owner text,
    lease_until timestamptz,
    last_error text,
    CHECK (length(trim(tenant_id)) > 0)
);

CREATE INDEX IF NOT EXISTS tenant_configuration_readiness_outbox_pending_idx
    ON tenant_configuration_readiness_outbox(event_sequence, next_attempt_at)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS tenant_configuration_readiness_publish_state (
    singleton_id smallint PRIMARY KEY CHECK (singleton_id = 1),
    checkpoint bigint NOT NULL DEFAULT 0 CHECK (checkpoint >= 0),
    lease_owner text,
    lease_until timestamptz,
    updated_at timestamptz NOT NULL
);

INSERT INTO tenant_configuration_readiness_publish_state(singleton_id, updated_at)
VALUES(1, statement_timestamp())
ON CONFLICT (singleton_id) DO NOTHING;
