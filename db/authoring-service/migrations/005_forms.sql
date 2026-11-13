BEGIN;

CREATE TABLE form_definitions (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    form_key TEXT NOT NULL CHECK (length(form_key) BETWEEN 1 AND 256),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 512),
    status TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
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
    UNIQUE (tenant_id, form_key),
    CHECK ((NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL)
        OR (is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND length(delete_reason) > 0))
);

CREATE INDEX form_definitions_live_page ON form_definitions (tenant_id, id) WHERE NOT is_deleted;
CREATE INDEX form_definitions_live_search ON form_definitions (tenant_id, lower(name), id) WHERE NOT is_deleted;

CREATE TABLE form_drafts (
    tenant_id UUID NOT NULL,
    form_id UUID NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    schema_json JSONB NOT NULL CHECK (jsonb_typeof(schema_json) = 'object' AND octet_length(schema_json::text) <= 262144),
    ui_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(ui_schema_json) = 'object' AND octet_length(ui_schema_json::text) <= 262144),
    validation_status TEXT NOT NULL DEFAULT 'NOT_VALIDATED' CHECK (validation_status IN ('NOT_VALIDATED', 'VALID', 'INVALID', 'STALE')),
    diagnostics JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(diagnostics) = 'array'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID NOT NULL,
    PRIMARY KEY (tenant_id, form_id),
    FOREIGN KEY (tenant_id, form_id) REFERENCES form_definitions (tenant_id, id) ON DELETE RESTRICT
);

CREATE TABLE form_versions (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    form_id UUID NOT NULL,
    version_num BIGINT NOT NULL CHECK (version_num > 0),
    source_revision BIGINT NOT NULL CHECK (source_revision > 0),
    schema_json JSONB NOT NULL CHECK (jsonb_typeof(schema_json) = 'object' AND octet_length(schema_json::text) <= 262144),
    ui_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(ui_schema_json) = 'object' AND octet_length(ui_schema_json::text) <= 262144),
    content_hash BYTEA NOT NULL CHECK (octet_length(content_hash) = 32),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version_tag TEXT NOT NULL CHECK (length(version_tag) BETWEEN 1 AND 128),
    summary TEXT NOT NULL CHECK (length(summary) BETWEEN 1 AND 1000),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, form_id, version_num),
    UNIQUE (tenant_id, form_id, source_revision),
    UNIQUE (tenant_id, form_id, version_tag),
    FOREIGN KEY (tenant_id, form_id) REFERENCES form_definitions (tenant_id, id) ON DELETE RESTRICT
);

ALTER TABLE form_definitions ADD CONSTRAINT form_definitions_latest_version_fk
    FOREIGN KEY (tenant_id, latest_version_id) REFERENCES form_versions (tenant_id, id) ON DELETE RESTRICT;
CREATE INDEX form_versions_history ON form_versions (tenant_id, form_id, version_num DESC);

CREATE TABLE authoring_form_audit_evidence (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    form_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (length(action) BETWEEN 1 AND 128),
    command_id UUID NOT NULL,
    correlation_id TEXT NOT NULL CHECK (length(correlation_id) BETWEEN 1 AND 256),
    before_digest BYTEA CHECK (before_digest IS NULL OR octet_length(before_digest) = 32),
    after_digest BYTEA CHECK (after_digest IS NULL OR octet_length(after_digest) = 32),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, form_id) REFERENCES form_definitions (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX authoring_form_audit_by_form ON authoring_form_audit_evidence (tenant_id, form_id, occurred_at DESC, id DESC);

CREATE FUNCTION reject_form_version_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'form versions are immutable';
END;
$$;
CREATE TRIGGER form_versions_immutable BEFORE UPDATE OR DELETE ON form_versions
FOR EACH ROW EXECUTE FUNCTION reject_form_version_mutation();
CREATE TRIGGER authoring_form_audit_evidence_immutable BEFORE UPDATE OR DELETE ON authoring_form_audit_evidence
FOR EACH ROW EXECUTE FUNCTION reject_authoring_evidence_mutation();

COMMIT;
