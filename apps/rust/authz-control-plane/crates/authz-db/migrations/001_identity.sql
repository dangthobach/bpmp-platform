-- Migration 001: Identity & Multi-tenancy (Layer A)
-- Includes: tenant, user_account, user_attribute_history

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─── Tenant ───────────────────────────────────────────────────────────────────

CREATE TABLE tenant (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    code      VARCHAR(50) UNIQUE NOT NULL,
    name      VARCHAR(200) NOT NULL,
    is_active BOOLEAN     NOT NULL DEFAULT true,
    -- Tenant-level configuration: fail_mode, rebac_max_depth, feature flags
    config    JSONB       NOT NULL DEFAULT '{
        "fail_mode": "DENY",
        "rebac_max_depth": 10,
        "shadow_mode_enabled": false
    }',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE  tenant              IS 'Top-level multi-tenancy isolation unit.';
COMMENT ON COLUMN tenant.code        IS 'Short unique code used as a stable reference, e.g. "vpbank", "pdms".';
COMMENT ON COLUMN tenant.config      IS 'Per-tenant AuthZ engine config: fail_mode (DENY|OPEN), rebac_max_depth, shadow_mode_enabled.';

-- ─── User Account ────────────────────────────────────────────────────────────

CREATE TABLE user_account (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    username            VARCHAR(100) NOT NULL,
    -- External IdP subject ID (e.g. Keycloak "sub" claim)
    external_id         VARCHAR(200),
    -- Dynamic user attributes used in ABAC: {"branch_code":"HN01","level":3}
    attributes          JSONB       NOT NULL DEFAULT '{}',
    -- Monotonic counter incremented on every attribute sync from Keycloak.
    -- Cache keys embed this version to detect staleness without long TTL.
    attributes_version  BIGINT      NOT NULL DEFAULT 0,
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, username)
);

COMMENT ON COLUMN user_account.attributes_version IS
    'Monotonic version. Incremented on each Keycloak attribute sync. '
    'Cache key format: "authz:ctx:{userId}:{version}". Stale cache auto-rejected on version mismatch.';

CREATE INDEX idx_user_tenant         ON user_account(tenant_id);
CREATE INDEX idx_user_external_id    ON user_account(external_id) WHERE external_id IS NOT NULL;
CREATE INDEX idx_user_attributes     ON user_account USING gin(attributes);

-- ─── User Attribute History ──────────────────────────────────────────────────

CREATE TABLE user_attribute_history (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    attribute   VARCHAR(100) NOT NULL,
    old_value   TEXT,
    new_value   TEXT,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The admin UUID or NULL if changed by automated sync
    changed_by  UUID
);

COMMENT ON TABLE user_attribute_history IS
    'Audit trail for every attribute change on a user. '
    'Required for compliance and incident investigation.';

CREATE INDEX idx_attr_history_user ON user_attribute_history(user_id, changed_at DESC);

-- ─── Automatic updated_at trigger ────────────────────────────────────────────

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_tenant_updated_at
    BEFORE UPDATE ON tenant
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_user_account_updated_at
    BEFORE UPDATE ON user_account
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
