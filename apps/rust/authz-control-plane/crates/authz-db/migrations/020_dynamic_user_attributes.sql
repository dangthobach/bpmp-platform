-- Migration 020: Dynamic User Attributes & Indexed Lookup Table (WP2.7)
--
-- Performance design for large datasets:
-- 1. `tenant_attribute_definition` acts as a per-tenant schema allowlist supporting
--    multiple workflows (e.g. `region`, `credit.product_lines`, `loan.approval_limit`).
-- 2. `user_attribute` stores normalized (tenant_id, user_id, attribute_key, value_text)
--    rows with composite B-tree indexes so high-cardinality queries like
--    `WHERE tenant_id = $1 AND attribute_key = 'branch' AND value_text = 'HN01'`
--    or range queries `WHERE tenant_id = $1 AND attribute_key = 'approval_limit' AND value_int >= $2`
--    execute as Index-Only / Index Range Scans instead of scanning JSONB blobs.
-- 3. The PK `(tenant_id, user_id, attribute_key, value_text)` also returns an actor's
--    attributes in canonical sorted order for `SignedActorContext` issuance.
-- 4. `user_account.attributes` (JSONB) and `user_account.attributes_version` remain
--    synchronized in the same transaction for backward compatibility.

CREATE TABLE tenant_attribute_definition (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID         NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    attribute_key       VARCHAR(64)  NOT NULL,
    value_type          VARCHAR(16)  NOT NULL
                        CHECK (value_type IN ('string', 'string_list', 'int64', 'bool')),
    max_values          INTEGER      NOT NULL DEFAULT 1
                        CHECK (max_values >= 1 AND max_values <= 32),
    workflow_namespaces TEXT[]       NOT NULL DEFAULT ARRAY[]::text[],
    description         TEXT         NOT NULL DEFAULT '',
    is_active           BOOLEAN      NOT NULL DEFAULT true,
    version             BIGINT       NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_by          UUID,
    updated_by          UUID,
    UNIQUE (tenant_id, attribute_key),
    CHECK (attribute_key ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CHECK (value_type = 'string_list' OR max_values = 1)
);

CREATE INDEX idx_tenant_attr_def_active
    ON tenant_attribute_definition (tenant_id, attribute_key)
    WHERE is_active;

CREATE TRIGGER trg_tenant_attribute_definition_updated_at
    BEFORE UPDATE ON tenant_attribute_definition
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_attribute (
    tenant_id     UUID         NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    user_id       UUID         NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    attribute_key VARCHAR(64)  NOT NULL,
    value_text    VARCHAR(256) NOT NULL,
    value_int     BIGINT,
    value_bool    BOOLEAN,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, attribute_key, value_text),
    FOREIGN KEY (tenant_id, attribute_key)
        REFERENCES tenant_attribute_definition (tenant_id, attribute_key)
        ON DELETE RESTRICT
);

-- Index-Only Scan for equality / IN lookups across large user populations:
-- e.g. WHERE tenant_id = $1 AND attribute_key = 'branch' AND value_text = 'HN01'
CREATE INDEX idx_user_attribute_lookup_text
    ON user_attribute (tenant_id, attribute_key, value_text, user_id);

-- B-tree Range Scan for numeric attributes (e.g. approval_limit >= 50000000):
CREATE INDEX idx_user_attribute_lookup_int
    ON user_attribute (tenant_id, attribute_key, value_int, user_id)
    WHERE value_int IS NOT NULL;

-- B-tree Lookup for boolean attributes:
CREATE INDEX idx_user_attribute_lookup_bool
    ON user_attribute (tenant_id, attribute_key, value_bool, user_id)
    WHERE value_bool IS NOT NULL;

-- Compact GIN index for legacy JSONB containment queries (`attributes @> '{"branch":"HN01"}'`).
CREATE INDEX IF NOT EXISTS idx_user_attributes_path_ops
    ON user_account USING gin (attributes jsonb_path_ops);
