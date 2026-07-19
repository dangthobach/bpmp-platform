const SQLX_ENTITY_METADATA: &str = include_str!("../migrations/011_entity_metadata.sql");
const FLYWAY_CONFIG: &str = include_str!("../../../db/flyway.conf.example");
const ALL_MIGRATIONS: &str = concat!(
    include_str!("../migrations/001_identity.sql"),
    include_str!("../migrations/002_rbac.sql"),
    include_str!("../migrations/003_resource.sql"),
    include_str!("../migrations/004_policy.sql"),
    include_str!("../migrations/005_rebac.sql"),
    include_str!("../migrations/006_filter.sql"),
    include_str!("../migrations/007_audit.sql"),
    include_str!("../migrations/008_triggers.sql"),
    include_str!("../migrations/009_indexes.sql"),
    include_str!("../migrations/010_user_last_active.sql"),
    include_str!("../migrations/011_entity_metadata.sql"),
    include_str!("../migrations/012_tenant_registry.sql"),
);

const TENANT_REGISTRY: &str = include_str!("../migrations/012_tenant_registry.sql");

#[test]
fn sqlx_and_flyway_use_one_canonical_migration_directory() {
    assert!(FLYWAY_CONFIG.contains("filesystem:./crates/authz-db/migrations"));
    assert!(FLYWAY_CONFIG.contains("flyway.sqlMigrationPrefix    =\n"));
    assert!(FLYWAY_CONFIG.contains("flyway.sqlMigrationSeparator = _"));
}

#[test]
fn canonical_migrations_are_transaction_compatible() {
    let normalized = ALL_MIGRATIONS.to_ascii_lowercase();
    assert!(!normalized.contains("create index concurrently"));
    assert!(!normalized.contains("where expires_at is null or expires_at > now()"));
}

#[test]
fn mutable_entities_have_required_metadata_and_immutable_logs_are_excluded() {
    for required in [
        "version BIGINT NOT NULL DEFAULT 0",
        "is_deleted BOOLEAN NOT NULL DEFAULT false",
        "deleted_at TIMESTAMPTZ",
        "deleted_by UUID",
        "created_by UUID",
        "updated_by UUID",
        "SELECT ... FOR UPDATE",
    ] {
        assert!(
            SQLX_ENTITY_METADATA.contains(required),
            "missing canonical metadata contract: {required}"
        );
    }

    let mutable_table_array = SQLX_ENTITY_METADATA
        .split("mutable_tables text[] := ARRAY[")
        .nth(1)
        .and_then(|tail| tail.split("];\nBEGIN").next())
        .expect("mutable table declaration must be present");
    for immutable in [
        "authz_decision_log",
        "user_attribute_history",
        "policy_shadow_log",
        "relation_reachability",
    ] {
        assert!(
            !mutable_table_array.contains(immutable),
            "immutable/derived table {immutable} must not receive soft-delete metadata"
        );
    }
}

#[test]
fn tenant_registry_has_immutable_audit_and_stable_code_uniqueness() {
    assert!(TENANT_REGISTRY.contains("CREATE TABLE tenant_audit_log"));
    assert!(TENANT_REGISTRY.contains("entity_version"));
    assert!(TENANT_REGISTRY.contains("actor_ref"));
    assert!(TENANT_REGISTRY.contains("request_id"));
    assert!(TENANT_REGISTRY.contains("ON tenant(lower(code))"));
}
