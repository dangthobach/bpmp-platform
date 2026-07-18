-- Migration 008: Database-level Triggers (Safety Guards)
-- Implements: cycle detection, fanout limit, escape hatch approval

-- ─── EC-2: Cycle Detection Trigger ───────────────────────────────────────────
-- Prevents inserting relation_tuple entries that would create a cycle.
-- Example: A→B, B→C already exist; inserting C→A would create a cycle.

CREATE OR REPLACE FUNCTION check_relation_cycle()
RETURNS TRIGGER AS $$
DECLARE
    cycle_exists BOOLEAN;
BEGIN
    -- If inserting (subject → object), check whether object can already reach subject.
    -- If yes, inserting would create a cycle.
    WITH RECURSIVE reachable AS (
        -- Start from the object side — find all nodes reachable FROM object
        SELECT object AS node
        FROM   relation_tuple
        WHERE  tenant_id = NEW.tenant_id
          AND  subject   = NEW.object
          AND  relation  = NEW.relation
          AND  (expires_at IS NULL OR expires_at > NOW())

        UNION

        -- Recurse: follow the graph forward
        SELECT rt.object
        FROM   relation_tuple rt
        JOIN   reachable r ON rt.subject = r.node
        WHERE  rt.tenant_id = NEW.tenant_id
          AND  rt.relation  = NEW.relation
          AND  (rt.expires_at IS NULL OR rt.expires_at > NOW())
    )
    SELECT EXISTS (
        SELECT 1 FROM reachable WHERE node = NEW.subject
    ) INTO cycle_exists;

    IF cycle_exists THEN
        RAISE EXCEPTION
            'Cycle detected: (%) -[%]-> (%) would create a cycle in the relation graph',
            NEW.subject, NEW.relation, NEW.object
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION check_relation_cycle() IS
    'EC-2 Layer 1: Prevents cycles in relation_tuple at write time. '
    'Runs a bounded recursive CTE before each INSERT. '
    'For very large graphs, consider supplementing with application-level DFS.';

CREATE TRIGGER trg_check_relation_cycle
    BEFORE INSERT ON relation_tuple
    FOR EACH ROW EXECUTE FUNCTION check_relation_cycle();

-- ─── Gap4: Fan-out Limit Trigger ──────────────────────────────────────────────
-- Prevents a single subject from having more object relations than allowed.

CREATE OR REPLACE FUNCTION enforce_fanout_limit()
RETURNS TRIGGER AS $$
DECLARE
    current_count INT;
    max_allowed   INT;
BEGIN
    -- Look up the limit for this relation type in this tenant
    SELECT max_fanout INTO max_allowed
    FROM   relation_type
    WHERE  tenant_id = NEW.tenant_id
      AND  relation  = NEW.relation;

    -- If no limit configured, allow unconditionally
    IF max_allowed IS NULL THEN
        RETURN NEW;
    END IF;

    -- Count existing active tuples with this subject+relation
    SELECT COUNT(*) INTO current_count
    FROM   relation_tuple
    WHERE  tenant_id = NEW.tenant_id
      AND  subject   = NEW.subject
      AND  relation  = NEW.relation
      AND  (expires_at IS NULL OR expires_at > NOW());

    IF current_count >= max_allowed THEN
        RAISE EXCEPTION
            'Fan-out limit exceeded: subject=% relation=% current=% limit=%',
            NEW.subject, NEW.relation, current_count, max_allowed
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION enforce_fanout_limit() IS
    'Gap4: Prevents "Big Node" problem by capping fan-out per relation type. '
    'Limit is configured per tenant in relation_type.max_fanout.';

CREATE TRIGGER trg_enforce_fanout
    BEFORE INSERT ON relation_tuple
    FOR EACH ROW EXECUTE FUNCTION enforce_fanout_limit();

-- ─── EC-5: Escape Hatch Approval Trigger ─────────────────────────────────────
-- Blocks insertion of escape hatch fragments without governance approval.

CREATE OR REPLACE FUNCTION enforce_escape_hatch_approval()
RETURNS TRIGGER AS $$
BEGIN
    -- Check if any escape hatch field is being used
    IF (NEW.sql_fragment IS NOT NULL
        OR NEW.es_fragment IS NOT NULL
        OR NEW.mongo_fragment IS NOT NULL)
    THEN
        -- All governance fields must be present
        IF NEW.escape_hatch_approved_by IS NULL
           OR NEW.escape_hatch_reason IS NULL
           OR NEW.escape_hatch_ticket_ref IS NULL
        THEN
            RAISE EXCEPTION
                'Escape hatch requires approval: '
                'set escape_hatch_approved_by, escape_hatch_reason, and escape_hatch_ticket_ref. '
                'Reference: EC-5 policy governance.'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION enforce_escape_hatch_approval() IS
    'EC-5: Prevents raw SQL/ES/Mongo escape hatches without documented approval. '
    'All three governance columns must be set when using any escape hatch field.';

CREATE TRIGGER trg_escape_hatch_approval
    BEFORE INSERT OR UPDATE ON row_filter
    FOR EACH ROW EXECUTE FUNCTION enforce_escape_hatch_approval();

-- ─── Optimistic Lock: User Attribute Update ───────────────────────────────────
-- Application-side: UPDATE ... WHERE attributes_version < :newVersion
-- This function is a helper for the repository to confirm the update succeeded.
-- (Not a trigger — called from application code.)

-- CDC REPLICA IDENTITY setup for future Debezium/change-data-capture integration
ALTER TABLE relation_tuple      REPLICA IDENTITY FULL;
ALTER TABLE policy              REPLICA IDENTITY FULL;
ALTER TABLE role_permission     REPLICA IDENTITY FULL;
ALTER TABLE user_account        REPLICA IDENTITY FULL;
ALTER TABLE row_filter          REPLICA IDENTITY FULL;
ALTER TABLE policy_version      REPLICA IDENTITY FULL;
