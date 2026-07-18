-- Migration 006: Data Filters — Field, Row, Temporal, External Attribute (Layer E, EC-1, EC-4)

-- ─── Field Filter (Layer E-3: field masking) ──────────────────────────────────

CREATE TABLE field_filter (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    permission_id  UUID        NOT NULL REFERENCES permission(id) ON DELETE CASCADE,
    resource_type  VARCHAR(100) NOT NULL,
    -- Whitelist of fields to return (empty = all fields allowed)
    allowed_fields VARCHAR(100)[],
    -- Fields to mask instead of block
    masked_fields  VARCHAR(100)[],
    -- Mask pattern: '****', '***-***-####'
    mask_pattern   VARCHAR(50)
);

CREATE INDEX idx_field_filter_permission ON field_filter(permission_id, resource_type);

-- ─── Row Filter (Layer E-1: backend-agnostic AST) ─────────────────────────────

CREATE TABLE row_filter (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    permission_id   UUID        NOT NULL REFERENCES permission(id) ON DELETE CASCADE,
    resource_type   VARCHAR(100) NOT NULL,
    -- Backend-agnostic AST — same JSON format as policy_rule.condition_expr
    filter_expr     JSONB       NOT NULL,
    -- Escape hatches (require governance approval — see trigger in 008_triggers)
    sql_fragment    TEXT        DEFAULT NULL,
    es_fragment     JSONB       DEFAULT NULL,
    mongo_fragment  JSONB       DEFAULT NULL,
    priority        INT         NOT NULL DEFAULT 0,
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    -- Governance columns — all must be set when using escape hatch
    escape_hatch_reason      TEXT,
    escape_hatch_approved_by UUID,
    escape_hatch_approved_at TIMESTAMPTZ,
    escape_hatch_ticket_ref  VARCHAR(100)
);

COMMENT ON COLUMN row_filter.filter_expr IS
    'Backend-agnostic JSON AST. Same ConditionNode schema as policy_rule.condition_expr. '
    'Translated to SQL/ES/Mongo by FilterTranslator implementations in authz-engine.';

COMMENT ON COLUMN row_filter.sql_fragment IS
    'EC-5 escape hatch: raw SQL WHERE fragment for edge cases. '
    'Requires escape_hatch_approved_by to be set (enforced by trigger).';

CREATE INDEX idx_row_filter_permission ON row_filter(permission_id, resource_type)
    WHERE is_active = true;

-- ─── Temporal Policy (EC-1) ───────────────────────────────────────────────────

CREATE TABLE temporal_policy (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    permission_id   UUID        NOT NULL REFERENCES permission(id) ON DELETE CASCADE,
    name            VARCHAR(200) NOT NULL,
    -- ISO weekday numbers: 1=Mon, 2=Tue, ..., 7=Sun
    allowed_days    SMALLINT[]  NOT NULL DEFAULT '{1,2,3,4,5}',
    allowed_from    TIME        NOT NULL DEFAULT '08:00',
    allowed_until   TIME        NOT NULL DEFAULT '17:30',
    timezone        VARCHAR(50)  NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
    -- CIDR allowlist for client IP. NULL = no IP restriction.
    -- Example: '{10.0.0.0/8, 192.168.1.0/24}'
    allowed_cidr    INET[]      DEFAULT NULL,
    -- If true, user must have an active shift record
    require_shift   BOOLEAN     NOT NULL DEFAULT false,
    -- Reference format: 'shift_schedule:user_id'
    shift_table_ref VARCHAR(300) DEFAULT NULL,
    is_active       BOOLEAN     NOT NULL DEFAULT true
);

COMMENT ON TABLE temporal_policy IS
    'EC-1: Evaluated BEFORE the ABAC/compiled-cache path. '
    'Temporal conditions (env.now) must NOT be embedded in filter_expr — '
    'that would break the compiled predicate cache (P2). '
    'This separate table is loaded once and evaluated in pure in-memory arithmetic.';

CREATE INDEX idx_temporal_permission ON temporal_policy(permission_id) WHERE is_active = true;

-- ─── External Attribute Source (EC-4) ────────────────────────────────────────

CREATE TABLE external_attribute_source (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    -- Short code referenced in AST: {"type": "external_attr", "source": "shift_service"}
    code            VARCHAR(100) NOT NULL,
    base_url        VARCHAR(500) NOT NULL,
    -- URL template with placeholders: '/internal/users/{userId}/attributes'
    attribute_path  VARCHAR(200) NOT NULL,
    cacheable       BOOLEAN     NOT NULL DEFAULT true,
    -- Short TTL: data is dynamic (shift status, session state)
    cache_ttl_secs  INT         NOT NULL DEFAULT 30,
    -- MUST be short — AuthZ is on the hot path. Circuit breaker trips at 3 failures.
    timeout_ms      INT         NOT NULL DEFAULT 200,
    -- Value to return when source is unavailable. NULL = fail-closed (deny).
    fallback_value  JSONB       DEFAULT NULL,
    UNIQUE(tenant_id, code)
);

COMMENT ON COLUMN external_attribute_source.timeout_ms IS
    'EC-4: Hard timeout in milliseconds. Must be < 200ms. '
    'JIT fetch failure with no fallback_value = fail-closed (DENY).';

CREATE INDEX idx_ext_attr_source_tenant ON external_attribute_source(tenant_id, code);
