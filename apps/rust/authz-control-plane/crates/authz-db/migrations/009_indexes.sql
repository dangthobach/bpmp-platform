-- Migration 009: Performance Indexes
-- Comprehensive index strategy for all access patterns (P1–P5 from design doc)

-- ─── P1: N+1 Query Elimination ────────────────────────────────────────────────
-- The combined RBAC+policy+filter query joins across role_tree → role_permission →
-- permission → row_filter → field_filter → policy_rule.
-- These indexes support each join condition efficiently.

-- Role hierarchy traversal (WITH RECURSIVE)
CREATE INDEX IF NOT EXISTS idx_role_hierarchy
    ON role(tenant_id, parent_role_id, id)
    WHERE parent_role_id IS NOT NULL;

-- Role permission lookup for a given resource type and action
CREATE INDEX IF NOT EXISTS idx_role_permission_resource
    ON role_permission(role_id)
    INCLUDE (permission_id, conditions);

CREATE INDEX IF NOT EXISTS idx_permission_type_action
    ON permission(tenant_id, resource_type, action, scope);

-- Policy rule lookup (resource_type + action → condition_expr)
CREATE INDEX IF NOT EXISTS idx_policy_rule_lookup
    ON policy_rule(resource_type, action)
    INCLUDE (policy_id, condition_expr, subject_type);

-- ─── P4: Row Filter Lookup ────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_row_filter_active_lookup
    ON row_filter(permission_id, resource_type, priority DESC)
    WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_field_filter_lookup
    ON field_filter(permission_id, resource_type);

-- ─── Temporal Policy Lookup ───────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_temporal_policy_active
    ON temporal_policy(permission_id)
    WHERE is_active = true;

-- ─── User Role Active Lookup ──────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_user_role_active_full
    ON user_role(user_id, role_id, resource_scope_id, expires_at);

-- ─── ReBAC Reachability ───────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_reachability_forward
    ON relation_reachability(tenant_id, subject, relation)
    INCLUDE (object, depth);

CREATE INDEX IF NOT EXISTS idx_reachability_reverse
    ON relation_reachability(tenant_id, object, relation)
    INCLUDE (subject, depth);

-- ─── Resource ACL Fast Lookup ─────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_resource_acl_lookup
    ON resource_acl(resource_instance_id, subject_type, subject_id)
    INCLUDE (actions, conditions);

-- ─── Resource Instance External Ref ──────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_resource_instance_by_ref
    ON resource_instance(resource_type_id, external_ref)
    WHERE external_ref IS NOT NULL;

-- ─── Authz Decision Log - Fast Explain/Replay ─────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_decision_log_latest
    ON authz_decision_log(user_id, resource_type, action, decided_at DESC)
    INCLUDE (decision, eval_trace, context);

-- ─── Schema Field Registry ────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_schema_field_canonical
    ON schema_field_registry(tenant_id, resource_type, canonical_name);

-- ─── Policy Version Latest Active ─────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_policy_version_latest
    ON policy_version(policy_id, status, version_num DESC);

-- ─── External Attribute Source Lookup ────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_ext_attr_source_code
    ON external_attribute_source(tenant_id, code);
