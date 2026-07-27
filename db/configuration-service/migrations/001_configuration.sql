CREATE TABLE IF NOT EXISTS configuration_profiles (
    id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    name text NOT NULL,
    scope_type text NOT NULL,
    scope_reference text NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    current_published_version_id uuid,
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    created_by text NOT NULL,
    updated_at timestamptz NOT NULL,
    updated_by text NOT NULL,
    UNIQUE (tenant_id, name),
    CHECK (length(trim(tenant_id)) > 0),
    CHECK (length(trim(name)) > 0),
    CHECK (scope_type IN ('PLATFORM','ENVIRONMENT','TENANT','WORKFLOW_TYPE','WORKFLOW_VERSION','APPROVED_INSTANCE_OVERRIDE')),
    CHECK (length(trim(scope_reference)) > 0)
);

CREATE TABLE IF NOT EXISTS configuration_versions (
    id uuid PRIMARY KEY,
    profile_id uuid NOT NULL REFERENCES configuration_profiles(id),
    tenant_id text NOT NULL,
    ordinal bigint NOT NULL CHECK (ordinal > 0),
    config_version text NOT NULL,
    policy_version text NOT NULL,
    schema_version integer NOT NULL CHECK (schema_version > 0),
    status text NOT NULL CHECK (status IN ('DRAFT','PUBLISHED','RETIRED')),
    values_json jsonb NOT NULL,
    content_hash bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    reason text NOT NULL,
    created_at timestamptz NOT NULL,
    created_by text NOT NULL,
    published_at timestamptz,
    published_by text,
    UNIQUE (profile_id, ordinal),
    UNIQUE (tenant_id, config_version),
    CHECK (length(trim(config_version)) > 0),
    CHECK (length(trim(policy_version)) > 0),
    CHECK (length(trim(reason)) > 0)
);

CREATE TABLE IF NOT EXISTS configuration_active_scopes (
    tenant_id text NOT NULL,
    scope_type text NOT NULL,
    scope_reference text NOT NULL,
    profile_id uuid NOT NULL REFERENCES configuration_profiles(id),
    version_id uuid NOT NULL REFERENCES configuration_versions(id),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, scope_type, scope_reference)
);

ALTER TABLE configuration_profiles
    DROP CONSTRAINT IF EXISTS configuration_profiles_current_published_version_fk;
ALTER TABLE configuration_profiles
    ADD CONSTRAINT configuration_profiles_current_published_version_fk
    FOREIGN KEY (current_published_version_id) REFERENCES configuration_versions(id);

CREATE TABLE IF NOT EXISTS configuration_audit (
    audit_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    profile_id uuid NOT NULL,
    version_id uuid,
    actor_id text NOT NULL,
    action text NOT NULL,
    aggregate_version bigint NOT NULL,
    reason text NOT NULL,
    content_hash bytea,
    correlation_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    CHECK (length(trim(actor_id)) > 0),
    CHECK (length(trim(action)) > 0),
    CHECK (length(trim(correlation_id)) > 0)
);

CREATE TABLE IF NOT EXISTS configuration_outbox (
    event_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    profile_id uuid NOT NULL,
    version_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    published_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    CHECK (attempt_count >= 0)
);

CREATE TABLE IF NOT EXISTS configuration_idempotency (
    tenant_id text NOT NULL,
    actor_id text NOT NULL,
    idempotency_key text NOT NULL,
    operation text NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    command_id text NOT NULL,
    result_profile_id uuid,
    result_version_id uuid,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, actor_id, idempotency_key),
    CHECK (length(trim(idempotency_key)) > 0),
    CHECK (length(trim(operation)) > 0),
    CHECK (length(trim(command_id)) > 0)
);

CREATE INDEX IF NOT EXISTS configuration_profiles_tenant_page_idx
    ON configuration_profiles (tenant_id, name, id) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS configuration_versions_profile_idx
    ON configuration_versions (tenant_id, profile_id, ordinal DESC);
CREATE UNIQUE INDEX IF NOT EXISTS configuration_versions_one_draft_idx
    ON configuration_versions (profile_id) WHERE status = 'DRAFT';
CREATE UNIQUE INDEX IF NOT EXISTS configuration_versions_one_published_idx
    ON configuration_versions (profile_id) WHERE status = 'PUBLISHED';
CREATE INDEX IF NOT EXISTS configuration_outbox_pending_idx
    ON configuration_outbox (next_attempt_at, occurred_at, event_id) WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS configuration_idempotency_created_idx
    ON configuration_idempotency (created_at);

CREATE OR REPLACE FUNCTION reject_configuration_audit_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'configuration audit is append-only';
END;
$$;

DROP TRIGGER IF EXISTS configuration_audit_no_update ON configuration_audit;
CREATE TRIGGER configuration_audit_no_update
BEFORE UPDATE OR DELETE ON configuration_audit
FOR EACH ROW EXECUTE FUNCTION reject_configuration_audit_mutation();
