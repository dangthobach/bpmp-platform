-- Migration 007: Audit & Decision Log (G7: Policy Debugger)

CREATE TABLE authz_decision_log (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    user_id           UUID        NOT NULL,
    resource_type     VARCHAR(100) NOT NULL,
    resource_ref      VARCHAR(300),
    action            VARCHAR(50)  NOT NULL,
    decision          VARCHAR(10)  NOT NULL CHECK (decision IN ('ALLOW', 'DENY')),
    matched_policy_id UUID REFERENCES policy(id) ON DELETE SET NULL,
    policy_version_id UUID REFERENCES policy_version(id) ON DELETE SET NULL,
    -- Full AST node-by-node evaluation trace (G7)
    -- Format: { decision, matched_policy, shadow_diverged, layers: { temporal_gate, rbac, abac, rebac } }
    eval_trace        JSONB       NOT NULL,
    -- Snapshot of user attributes + resource attributes at decision time (for Replay API)
    context           JSONB       NOT NULL,
    decided_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Which sidecar pod made this decision (for distributed deployments)
    sidecar_id        VARCHAR(200)
);

COMMENT ON TABLE authz_decision_log IS
    'G7: Every AuthZ decision is logged here with a full AST eval trace. '
    'Enables Explain API (why was I denied?) and Replay API '
    '(what would current policy decide on this old context?). '
    'Never delete rows; archive to cold storage after retention period.';

COMMENT ON COLUMN authz_decision_log.eval_trace IS
    'G7: Node-by-node AST evaluation trace. '
    'Contains layers: temporal_gate, rbac, resource_acl, abac (tree), rebac. '
    'shadow_diverged flag indicates shadow policy produced a different outcome.';

COMMENT ON COLUMN authz_decision_log.context IS
    'Snapshot of the full evaluation context at decision time: '
    '{ user: { attributes, version }, resource: { attributes }, env: { request_time, client_ip } }. '
    'Used by Replay API to reproduce the exact evaluation.';

-- User access history
CREATE INDEX idx_authz_log_user     ON authz_decision_log(user_id, decided_at DESC);
-- Resource access history
CREATE INDEX idx_authz_log_resource ON authz_decision_log(resource_type, resource_ref, decided_at DESC);
-- Diverged shadow cases (partial index for fast shadow analysis queries)
CREATE INDEX idx_authz_log_diverged ON authz_decision_log(policy_version_id, decided_at DESC)
    WHERE eval_trace->>'shadow_diverged' = 'true';
-- Tenant-scoped audit queries
CREATE INDEX idx_authz_log_tenant   ON authz_decision_log(tenant_id, decided_at DESC);

-- Idempotency: prevent duplicate log entries from WAL relay retries
-- (EC-3: ON CONFLICT DO NOTHING in the application layer uses this unique constraint)
ALTER TABLE authz_decision_log ADD CONSTRAINT uq_decision_log_id UNIQUE (id);
