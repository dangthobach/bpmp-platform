BEGIN;

CREATE TABLE decision_models (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    model_key TEXT NOT NULL CHECK (length(model_key) BETWEEN 1 AND 256),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 512),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 4096),
    status TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
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
    CHECK ((NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL)
        OR (is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND length(delete_reason) > 0))
);

CREATE INDEX decision_models_live_page ON decision_models (tenant_id, id) WHERE NOT is_deleted;
CREATE INDEX decision_models_live_search ON decision_models (tenant_id, lower(name), id) WHERE NOT is_deleted;

CREATE TABLE decision_model_drafts (
    tenant_id UUID NOT NULL,
    decision_model_id UUID NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    dmn_xml TEXT NOT NULL CHECK (octet_length(dmn_xml) BETWEEN 1 AND 1048576),
    validation_status TEXT NOT NULL DEFAULT 'NOT_VALIDATED'
        CHECK (validation_status IN ('NOT_VALIDATED', 'VALID', 'INVALID', 'STALE')),
    diagnostics JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(diagnostics) = 'array'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID NOT NULL,
    PRIMARY KEY (tenant_id, decision_model_id),
    FOREIGN KEY (tenant_id, decision_model_id) REFERENCES decision_models (tenant_id, id) ON DELETE RESTRICT
);

CREATE TABLE authoring_decision_audit_evidence (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    decision_model_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (length(action) BETWEEN 1 AND 128),
    command_id UUID NOT NULL,
    correlation_id TEXT NOT NULL CHECK (length(correlation_id) BETWEEN 1 AND 256),
    before_digest BYTEA CHECK (before_digest IS NULL OR octet_length(before_digest) = 32),
    after_digest BYTEA CHECK (after_digest IS NULL OR octet_length(after_digest) = 32),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, decision_model_id) REFERENCES decision_models (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX authoring_decision_audit_by_model ON authoring_decision_audit_evidence (tenant_id, decision_model_id, occurred_at DESC, id DESC);

CREATE TRIGGER authoring_decision_audit_evidence_immutable
BEFORE UPDATE OR DELETE ON authoring_decision_audit_evidence
FOR EACH ROW EXECUTE FUNCTION reject_authoring_evidence_mutation();

COMMIT;
