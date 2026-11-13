BEGIN;

CREATE TABLE process_models (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    model_key TEXT NOT NULL CHECK (length(model_key) BETWEEN 1 AND 256),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 512),
    category TEXT NOT NULL DEFAULT '' CHECK (length(category) <= 256),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 4096),
    status TEXT NOT NULL DEFAULT 'DRAFT'
        CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    latest_version_id UUID,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID,
    delete_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, model_key),
    CHECK (
        (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL)
        OR (is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND length(delete_reason) > 0)
    )
);

CREATE INDEX process_models_live_page
    ON process_models (tenant_id, id)
    WHERE NOT is_deleted;
CREATE INDEX process_models_live_search
    ON process_models (tenant_id, lower(name), id)
    WHERE NOT is_deleted;

CREATE TABLE process_model_drafts (
    tenant_id UUID NOT NULL,
    process_model_id UUID NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    base_version_id UUID,
    bpmn_xml TEXT NOT NULL CHECK (octet_length(bpmn_xml) BETWEEN 1 AND 1048576),
    validation_status TEXT NOT NULL DEFAULT 'NOT_VALIDATED'
        CHECK (validation_status IN ('NOT_VALIDATED', 'VALID', 'INVALID', 'STALE')),
    diagnostics JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(diagnostics) = 'array'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID NOT NULL,
    PRIMARY KEY (tenant_id, process_model_id),
    FOREIGN KEY (tenant_id, process_model_id)
        REFERENCES process_models (tenant_id, id) ON DELETE RESTRICT
);

CREATE TABLE authoring_command_dedup (
    tenant_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
    command_id UUID NOT NULL,
    operation TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 128),
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest) = 32),
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, idempotency_key),
    UNIQUE (tenant_id, command_id)
);

CREATE TABLE authoring_audit_evidence (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    process_model_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (length(action) BETWEEN 1 AND 128),
    command_id UUID NOT NULL,
    correlation_id TEXT NOT NULL CHECK (length(correlation_id) BETWEEN 1 AND 256),
    before_digest BYTEA CHECK (before_digest IS NULL OR octet_length(before_digest) = 32),
    after_digest BYTEA CHECK (after_digest IS NULL OR octet_length(after_digest) = 32),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, process_model_id)
        REFERENCES process_models (tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX authoring_audit_by_model
    ON authoring_audit_evidence (tenant_id, process_model_id, occurred_at DESC, id DESC);

CREATE TABLE authoring_authorization_bundle_cache (
    tenant_id UUID PRIMARY KEY,
    bundle_sequence BIGINT NOT NULL CHECK (bundle_sequence > 0),
    catalog_version BIGINT NOT NULL CHECK (catalog_version > 0),
    revoke_epoch BIGINT NOT NULL CHECK (revoke_epoch >= 0),
    content_hash BYTEA NOT NULL CHECK (octet_length(content_hash) = 32),
    bundle_bytes BYTEA NOT NULL CHECK (octet_length(bundle_bytes) BETWEEN 1 AND 16777216),
    installed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION reject_authoring_evidence_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'authoring command and audit evidence is immutable';
END;
$$;

CREATE TRIGGER authoring_command_dedup_immutable
BEFORE UPDATE OR DELETE ON authoring_command_dedup
FOR EACH ROW EXECUTE FUNCTION reject_authoring_evidence_mutation();

CREATE TRIGGER authoring_audit_evidence_immutable
BEFORE UPDATE OR DELETE ON authoring_audit_evidence
FOR EACH ROW EXECUTE FUNCTION reject_authoring_evidence_mutation();

COMMIT;
