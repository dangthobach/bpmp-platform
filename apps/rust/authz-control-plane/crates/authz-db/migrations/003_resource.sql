-- Migration 003: Resource Registry (Layer C)
-- Implements G1: Type-level vs Instance-level ACL separation

-- ─── Resource Type ────────────────────────────────────────────────────────────

CREATE TABLE resource_type (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    code       VARCHAR(100) NOT NULL,
    name       VARCHAR(200) NOT NULL,
    -- JSON schema: attributes list, valid actions, field mappings for multi-backend
    -- Example:
    -- {
    --   "attributes": ["branch_code", "status", "created_by"],
    --   "actions": ["read", "write", "approve", "archive"],
    --   "field_mappings": {
    --     "branchCode": { "sql": "branch_code", "es": "branch_code", "mongo": "branchCode" }
    --   }
    -- }
    schema_def JSONB       NOT NULL DEFAULT '{"attributes":[],"actions":[],"field_mappings":{}}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, code)
);

COMMENT ON TABLE resource_type IS
    'Defines the structure and valid actions for a category of resources. '
    'Type-level policies apply to all instances of this type.';

COMMENT ON COLUMN resource_type.schema_def IS
    'Backend-agnostic schema: field names, valid actions, and per-backend field mappings. '
    'Used by FilterTranslator to map canonical field names to SQL columns, ES fields, Mongo keys.';

CREATE INDEX idx_resource_type_tenant ON resource_type(tenant_id, code);

CREATE TRIGGER trg_resource_type_updated_at
    BEFORE UPDATE ON resource_type
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ─── Resource Instance ────────────────────────────────────────────────────────
-- G1 Design: Only create rows here for the ~1% of resources that need special ACL.
-- The 99% handled by type-level policy need NO row in this table.

CREATE TABLE resource_instance (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type_id UUID        NOT NULL REFERENCES resource_type(id) ON DELETE RESTRICT,
    -- The domain service's own ID for this object (external reference)
    external_ref     VARCHAR(300),
    owner_id         UUID REFERENCES user_account(id) ON DELETE SET NULL,
    attributes       JSONB       NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE resource_instance IS
    'G1: Only exists for resources needing special instance-level ACL. '
    'For 100M document systems, expect <1M rows here — not 100M.';

CREATE INDEX idx_resource_instance_type    ON resource_instance(resource_type_id);
CREATE INDEX idx_resource_instance_ext_ref ON resource_instance(external_ref) WHERE external_ref IS NOT NULL;
CREATE INDEX idx_resource_instance_owner   ON resource_instance(owner_id)    WHERE owner_id IS NOT NULL;

-- ─── Add FK from user_role to resource_instance ───────────────────────────────
-- (Added here because resource_instance is defined in this migration)

ALTER TABLE user_role
    ADD CONSTRAINT fk_user_role_resource_scope
    FOREIGN KEY (resource_scope_id)
    REFERENCES resource_instance(id)
    ON DELETE SET NULL;

CREATE INDEX idx_user_role_scope ON user_role(resource_scope_id) WHERE resource_scope_id IS NOT NULL;

-- ─── Resource ACL ─────────────────────────────────────────────────────────────

CREATE TABLE resource_acl (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_instance_id UUID        NOT NULL REFERENCES resource_instance(id) ON DELETE CASCADE,
    subject_id           UUID        NOT NULL,
    subject_type         VARCHAR(20) NOT NULL CHECK (subject_type IN ('USER', 'ROLE', 'GROUP')),
    -- Array of actions this ACL entry grants (e.g. '{read,approve}')
    actions              VARCHAR(50)[] NOT NULL,
    -- Optional extra ABAC conditions on top of the ACL grant
    conditions           JSONB DEFAULT NULL
);

CREATE INDEX idx_acl_instance ON resource_acl(resource_instance_id, subject_type, subject_id);

-- ─── Schema Field Registry (EC-5) ────────────────────────────────────────────

CREATE TABLE schema_field_registry (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    resource_type  VARCHAR(100) NOT NULL,
    -- Canonical name used in AST policies: 'branchCode'
    canonical_name VARCHAR(100) NOT NULL,
    -- Backend-specific names
    sql_name       VARCHAR(100) NOT NULL,
    es_name        VARCHAR(100),
    mongo_name     VARCHAR(100),
    data_type      VARCHAR(50)  NOT NULL CHECK (
        data_type IN ('string', 'uuid', 'timestamp', 'integer', 'boolean', 'enum', 'json_object')
    ),
    enum_values    TEXT[],
    description    TEXT,
    UNIQUE(tenant_id, resource_type, canonical_name)
);

COMMENT ON TABLE schema_field_registry IS
    'EC-5: Single source of truth for field naming across services. '
    'CI/CD policy validator rejects policies that reference unknown fields. '
    'resource_type.schema_def.field_mappings is auto-generated from this table.';

CREATE INDEX idx_schema_field_tenant_type ON schema_field_registry(tenant_id, resource_type);
