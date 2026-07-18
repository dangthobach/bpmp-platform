-- Migration 002: RBAC — Role & Permission Hierarchy (Layer B)

-- ─── Role ────────────────────────────────────────────────────────────────────

CREATE TABLE role (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    code           VARCHAR(100) NOT NULL,
    name           VARCHAR(200) NOT NULL,
    -- Self-reference: parent role for inheritance traversal via WITH RECURSIVE
    parent_role_id UUID REFERENCES role(id) ON DELETE SET NULL,
    -- Higher priority roles take precedence in DENY-override evaluation
    priority       INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, code)
);

COMMENT ON COLUMN role.parent_role_id IS
    'Self-reference enabling role hierarchy. Engine traverses upward via WITH RECURSIVE '
    'to collect all inherited permissions.';

CREATE INDEX idx_role_tenant        ON role(tenant_id);
CREATE INDEX idx_role_parent        ON role(parent_role_id) WHERE parent_role_id IS NOT NULL;

-- ─── Permission ───────────────────────────────────────────────────────────────

CREATE TABLE permission (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    code          VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    action        VARCHAR(50)  NOT NULL,
    -- 'own' = only resources the user created
    -- 'branch' = all resources in the user's branch
    -- 'all' = all resources in the tenant
    scope         VARCHAR(20)  NOT NULL CHECK (scope IN ('own', 'branch', 'all')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, code)
);

COMMENT ON COLUMN permission.scope IS
    'Coarse-grained scope: own | branch | all. Fine-grained filter defined in row_filter.';

CREATE INDEX idx_permission_tenant        ON permission(tenant_id);
CREATE INDEX idx_permission_resource_type ON permission(tenant_id, resource_type, action);

-- ─── Role → Permission ────────────────────────────────────────────────────────

CREATE TABLE role_permission (
    role_id       UUID NOT NULL REFERENCES role(id)       ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permission(id) ON DELETE CASCADE,
    -- Optional extra ABAC conditions layered on top of the permission
    conditions    JSONB DEFAULT NULL,
    PRIMARY KEY(role_id, permission_id)
);

CREATE INDEX idx_role_permission_role ON role_permission(role_id);

-- ─── User → Role (with scoping and expiry) ────────────────────────────────────

CREATE TABLE user_role (
    user_id           UUID        NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    role_id           UUID        NOT NULL REFERENCES role(id)         ON DELETE CASCADE,
    -- Multi-tenancy: the tenant this role assignment belongs to (denormalized for query efficiency)
    tenant_id         UUID        NOT NULL REFERENCES tenant(id)       ON DELETE RESTRICT,
    -- Scoped role: role applies only to a specific resource instance
    -- Example: user A is REVIEWER only for contract batch #456
    resource_scope_id UUID,       -- FK to resource_instance added in migration 003
    -- Temporary permission: expires automatically, no cleanup job needed
    expires_at        TIMESTAMPTZ DEFAULT NULL,
    granted_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by        UUID,       -- UserId of the admin who granted this
    PRIMARY KEY(user_id, role_id)
);

COMMENT ON COLUMN user_role.resource_scope_id IS
    'When set, this role assignment is scoped to a single resource instance. '
    'The engine only considers this role when evaluating access to that specific resource.';

COMMENT ON COLUMN user_role.expires_at IS
    'Temporary permission. NULL = permanent. Expired entries are ignored by '
    'the engine without needing a cleanup job.';

CREATE INDEX idx_user_role_user    ON user_role(user_id);
CREATE INDEX idx_user_role_role    ON user_role(role_id);
-- Current time is evaluated by the query because PostgreSQL partial-index
-- predicates may only call IMMUTABLE functions.
CREATE INDEX idx_user_role_active ON user_role(user_id, role_id, expires_at);
