-- Canonical mutable-entity metadata and concurrency contract.
--
-- "version" is an optimistic-lock token. Pessimistic critical sections must
-- additionally lock rows with SELECT ... FOR UPDATE inside a short transaction.
-- Append-only audit/history and derived projection tables are intentionally excluded.

DO $$
DECLARE
    table_name text;
    mutable_tables text[] := ARRAY[
        'tenant',
        'user_account',
        'role',
        'permission',
        'role_permission',
        'user_role',
        'resource_type',
        'resource_instance',
        'resource_acl',
        'schema_field_registry',
        'policy',
        'policy_rule',
        'policy_version',
        'relation_type',
        'relation_tuple',
        'group_partition',
        'field_filter',
        'row_filter',
        'temporal_policy',
        'external_attribute_source'
    ];
BEGIN
    FOREACH table_name IN ARRAY mutable_tables LOOP
        EXECUTE format(
            'ALTER TABLE %I
                ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0,
                ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT false,
                ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
                ADD COLUMN IF NOT EXISTS deleted_by UUID,
                ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                ADD COLUMN IF NOT EXISTS created_by UUID,
                ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                ADD COLUMN IF NOT EXISTS updated_by UUID',
            table_name
        );
        EXECUTE format(
            'ALTER TABLE %I ADD CONSTRAINT %I CHECK (version >= 0)',
            table_name,
            'ck_' || table_name || '_version_non_negative'
        );
        EXECUTE format(
            'ALTER TABLE %I ADD CONSTRAINT %I CHECK (
                (is_deleted = false AND deleted_at IS NULL AND deleted_by IS NULL)
                OR (is_deleted = true AND deleted_at IS NOT NULL)
            )',
            table_name,
            'ck_' || table_name || '_soft_delete_consistent'
        );
    END LOOP;
END
$$;

CREATE OR REPLACE FUNCTION set_mutable_entity_metadata()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = clock_timestamp();
    NEW.version = OLD.version + 1;

    IF NEW.is_deleted AND NOT OLD.is_deleted THEN
        NEW.deleted_at = COALESCE(NEW.deleted_at, clock_timestamp());
    ELSIF OLD.is_deleted AND NOT NEW.is_deleted THEN
        RAISE EXCEPTION 'soft-deleted rows cannot be restored in place';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    table_name text;
    mutable_tables text[] := ARRAY[
        'tenant',
        'user_account',
        'role',
        'permission',
        'role_permission',
        'user_role',
        'resource_type',
        'resource_instance',
        'resource_acl',
        'schema_field_registry',
        'policy',
        'policy_rule',
        'policy_version',
        'relation_type',
        'relation_tuple',
        'group_partition',
        'field_filter',
        'row_filter',
        'temporal_policy',
        'external_attribute_source'
    ];
BEGIN
    FOREACH table_name IN ARRAY mutable_tables LOOP
        EXECUTE format(
            'DROP TRIGGER IF EXISTS %I ON %I',
            'trg_' || table_name || '_mutable_metadata',
            table_name
        );
        EXECUTE format(
            'CREATE TRIGGER %I
             BEFORE UPDATE ON %I
             FOR EACH ROW EXECUTE FUNCTION set_mutable_entity_metadata()',
            'trg_' || table_name || '_mutable_metadata',
            table_name
        );
    END LOOP;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_policy_version_one_active
    ON policy_version(policy_id)
    WHERE status = 'ACTIVE' AND is_deleted = false;

COMMENT ON COLUMN policy.version IS
    'Optimistic lock token. UPDATE must include the expected version when not protected by FOR UPDATE.';
COMMENT ON COLUMN policy.is_deleted IS
    'Soft-delete marker for mutable control-plane entities; immutable audit/event logs do not use this column.';
