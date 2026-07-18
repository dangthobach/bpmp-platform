-- Migration 005: ReBAC — Relation Tuples & Materialized Graph (Layer D, G3, EC-2)

-- ─── Relation Type (fanout limits) ───────────────────────────────────────────

CREATE TABLE relation_type (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    relation    VARCHAR(100) NOT NULL,
    -- Maximum number of objects a single subject can have with this relation.
    -- NULL = unlimited (use with caution for large graphs).
    max_fanout  INT         DEFAULT NULL,
    description TEXT,
    UNIQUE(tenant_id, relation)
);

COMMENT ON COLUMN relation_type.max_fanout IS
    'EC-2/Gap4: Fan-out limit prevents "Big Node" problem. '
    'For groups, recommend <= 10000. NULL = unlimited.';

-- ─── Relation Tuple (Zanzibar-style) ─────────────────────────────────────────

CREATE TABLE relation_tuple (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    -- Subject: "user:uuid-A", "group:ALL_EMPLOYEES_HN"
    subject    VARCHAR(300) NOT NULL,
    -- Relation: "delegate_of", "member_of", "reviewer_of", "owner_of"
    relation   VARCHAR(100) NOT NULL,
    -- Object: "user:uuid-B", "contract:uuid-C", "branch:HN01"
    object     VARCHAR(300) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Expiring relations supported natively
    expires_at TIMESTAMPTZ DEFAULT NULL,
    -- Idempotency: prevent duplicate tuples
    UNIQUE(tenant_id, subject, relation, object)
);

COMMENT ON TABLE relation_tuple IS
    'G3/EC-2: Zanzibar-style relation store. '
    'Subject and object use "type:id" encoding: "user:uuid", "group:code", "contract:uuid". '
    'Indexes support both forward (subject→object) and reverse (object→subject) traversal.';

-- Forward traversal: who has relation R with object O?
CREATE INDEX idx_tuple_subject ON relation_tuple(tenant_id, subject, relation);
-- Reverse traversal: what objects does subject S have relation R with?
CREATE INDEX idx_tuple_object  ON relation_tuple(tenant_id, object,  relation);
-- Active-only index (most queries only care about non-expired tuples)
CREATE INDEX idx_tuple_active
    ON relation_tuple(tenant_id, subject, relation, object, expires_at);

-- ─── Materialized Reachability (EC-2: O(1) graph lookup) ─────────────────────

CREATE TABLE relation_reachability (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    subject     VARCHAR(300) NOT NULL,
    relation    VARCHAR(100) NOT NULL,
    -- Every object transitively reachable from subject via relation
    object      VARCHAR(300) NOT NULL,
    -- Number of hops (depth=1 means direct relation)
    depth       INT         NOT NULL,
    -- Ordered node path for debugging: ['user:A', 'user:B', 'user:C']
    path        TEXT[]      NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, subject, relation, object)
);

COMMENT ON TABLE relation_reachability IS
    'EC-2: Pre-computed transitive closure of relation_tuple. '
    'Maintained incrementally by the CDC consumer (or bg worker). '
    'Allows O(1) lookup instead of O(depth) recursive SQL traversal. '
    'Live traversal (WITH RECURSIVE) used as fallback when this table is stale.';

-- Primary lookup: does subject S reach object O via relation R?
CREATE UNIQUE INDEX idx_reachability_unique  ON relation_reachability(tenant_id, subject, relation, object);
-- Reverse lookup: who can reach object O via relation R?
CREATE INDEX        idx_reachability_lookup  ON relation_reachability(tenant_id, object, relation);
CREATE INDEX        idx_reachability_subject ON relation_reachability(tenant_id, subject, relation);

-- ─── Group Partition (Gap4: Big Node decomposition) ──────────────────────────

CREATE TABLE group_partition (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    parent_group   VARCHAR(300) NOT NULL,
    child_group    VARCHAR(300) NOT NULL,
    -- The rule that determined this partition, e.g. "branch_code=HN"
    partition_key  VARCHAR(100),
    max_size       INT         NOT NULL DEFAULT 5000
);

COMMENT ON TABLE group_partition IS
    'Gap4: Decomposes large "Big Node" groups into sub-partitions. '
    'Example: ALL_EMPLOYEES → ALL_EMPLOYEES_HN (5000) + ALL_EMPLOYEES_HCM (5000). '
    'Policy engine traverses the partition tree instead of a single flat node.';

CREATE INDEX idx_group_partition_parent ON group_partition(tenant_id, parent_group);
