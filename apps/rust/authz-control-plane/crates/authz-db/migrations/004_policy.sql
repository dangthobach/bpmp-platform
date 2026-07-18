-- Migration 004: Policy Engine — ABAC & Versioning (Layer D, G6)

-- ─── Policy ───────────────────────────────────────────────────────────────────

CREATE TABLE policy (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    name      VARCHAR(200) NOT NULL,
    effect    VARCHAR(10)  NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
    -- DENY policies with higher priority override ALLOW.
    -- Denial is explicit and intentional — never silent.
    priority  INT         NOT NULL DEFAULT 0,
    is_active BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_policy_tenant_active ON policy(tenant_id, is_active);

CREATE TRIGGER trg_policy_updated_at
    BEFORE UPDATE ON policy
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ─── Policy Rule ──────────────────────────────────────────────────────────────

CREATE TABLE policy_rule (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id      UUID        NOT NULL REFERENCES policy(id) ON DELETE CASCADE,
    subject_type   VARCHAR(50)  NOT NULL CHECK (subject_type IN ('ROLE', 'USER', 'GROUP')),
    resource_type  VARCHAR(100) NOT NULL,
    action         VARCHAR(50)  NOT NULL,
    -- The ABAC condition expression as a JSON AST.
    -- See ConditionNode in authz-core for the full schema.
    condition_expr JSONB       NOT NULL
);

CREATE INDEX idx_policy_rule_policy        ON policy_rule(policy_id);
CREATE INDEX idx_policy_rule_resource_type ON policy_rule(resource_type, action);

-- ─── Policy Version (G6: Policy Versioning & Shadow Mode) ────────────────────

CREATE TABLE policy_version (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id     UUID        NOT NULL REFERENCES policy(id) ON DELETE RESTRICT,
    version_num   INT         NOT NULL,
    -- Full snapshot of the policy + all rules at publish time
    snapshot      JSONB       NOT NULL,
    status        VARCHAR(20)  NOT NULL DEFAULT 'DRAFT'
        CHECK (status IN ('DRAFT', 'SHADOW', 'ACTIVE', 'ARCHIVED')),
    published_by  UUID,
    published_at  TIMESTAMPTZ,
    notes         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(policy_id, version_num)
);

COMMENT ON TABLE policy_version IS
    'G6: Immutable snapshot of a policy at a point in time. '
    'Lifecycle: DRAFT → SHADOW → ACTIVE → ARCHIVED. '
    'Only one ACTIVE version per policy per tenant at a time (enforced by application).';

CREATE INDEX idx_policy_version_policy ON policy_version(policy_id, status);
CREATE INDEX idx_policy_version_active ON policy_version(policy_id, status) WHERE status = 'ACTIVE';
CREATE INDEX idx_policy_version_shadow ON policy_version(policy_id, status) WHERE status = 'SHADOW';

-- ─── Policy Shadow Log (G6: Shadow Mode Divergence Tracking) ──────────────────

CREATE TABLE policy_shadow_log (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_version_id UUID        NOT NULL REFERENCES policy_version(id) ON DELETE CASCADE,
    user_id           UUID,
    resource_ref      VARCHAR(300),
    action            VARCHAR(50),
    shadow_decision   VARCHAR(10) NOT NULL CHECK (shadow_decision IN ('ALLOW', 'DENY')),
    active_decision   VARCHAR(10) NOT NULL CHECK (active_decision IN ('ALLOW', 'DENY')),
    -- Generated column: true when shadow and active disagree
    diverged          BOOLEAN GENERATED ALWAYS AS (shadow_decision != active_decision) STORED,
    -- Full context snapshot for replay and debugging
    context_snapshot  JSONB,
    logged_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN policy_shadow_log.diverged IS
    'Generated column: true when shadow_decision != active_decision. '
    'Divergence rate > 5% blocks promotion to ACTIVE.';

CREATE INDEX idx_shadow_diverged ON policy_shadow_log(policy_version_id, diverged, logged_at DESC);
CREATE INDEX idx_shadow_version  ON policy_shadow_log(policy_version_id, logged_at DESC);
