-- Application schema: Organization tree (Group → Subsidiary → Branch → Department)
-- Strategy: adjacency list (parent_id) + materialized path (ltree)
-- Rationale: O(log N + k) subtree queries via GIST(path), cheap writes.

CREATE EXTENSION IF NOT EXISTS ltree;

CREATE TABLE IF NOT EXISTS org_aggregate (
    id            UUID PRIMARY KEY,
    tenant_id     UUID         NOT NULL,
    root_node_id  UUID         NOT NULL,
    version       BIGINT       NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_org_aggregate_tenant ON org_aggregate(tenant_id);

CREATE TABLE IF NOT EXISTS org_node (
    id          UUID PRIMARY KEY,
    org_id      UUID         NOT NULL REFERENCES org_aggregate(id) ON DELETE CASCADE,
    tenant_id   UUID         NOT NULL,
    parent_id   UUID         REFERENCES org_node(id),
    code        VARCHAR(64)  NOT NULL,
    name        VARCHAR(255) NOT NULL,
    kind        VARCHAR(32)  NOT NULL
                CHECK (kind IN ('GROUP','SUBSIDIARY','BRANCH','DEPARTMENT')),
    path        LTREE        NOT NULL,
    is_active   BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, path)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_node_tenant_code
    ON org_node(tenant_id, code);

CREATE INDEX IF NOT EXISTS idx_org_node_path_gist
    ON org_node USING GIST (path);

CREATE INDEX IF NOT EXISTS idx_org_node_parent
    ON org_node(parent_id);

CREATE INDEX IF NOT EXISTS idx_org_node_tenant_kind
    ON org_node(tenant_id, kind) WHERE is_active;

-- Row-Level Security: tenant guard as the last line of defence.
-- App layer always passes tenant_id; RLS protects against accidental
-- queries that omit it.
ALTER TABLE org_aggregate ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_node      ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_aggregate ON org_aggregate;
DROP POLICY IF EXISTS tenant_isolation_node      ON org_node;

CREATE POLICY tenant_isolation_aggregate ON org_aggregate
  USING (tenant_id::text = current_setting('app.tenant_id', true));

CREATE POLICY tenant_isolation_node ON org_node
  USING (tenant_id::text = current_setting('app.tenant_id', true));
