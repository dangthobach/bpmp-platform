-- Dynamic tenant registry audit trail.
-- Tenant rows remain soft-deleted so historical tenant-scoped records retain
-- referential integrity. This log is immutable and therefore intentionally
-- does not receive mutable entity metadata.

CREATE TABLE tenant_audit_log (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID        NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    operation        VARCHAR(20) NOT NULL
                     CHECK (operation IN ('CREATE', 'UPDATE', 'STATUS', 'DELETE')),
    entity_version   BIGINT      NOT NULL CHECK (entity_version >= 0),
    actor_ref        TEXT        NOT NULL CHECK (length(trim(actor_ref)) > 0),
    request_id       TEXT        NOT NULL CHECK (length(trim(request_id)) > 0),
    previous_value   JSONB,
    current_value    JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX idx_tenant_audit_tenant_version
    ON tenant_audit_log(tenant_id, entity_version DESC, created_at DESC);

-- Tenant codes are security-scoped stable identifiers and are never reused,
-- including after soft deletion.
CREATE UNIQUE INDEX uq_tenant_code_normalized
    ON tenant(lower(code));

COMMENT ON TABLE tenant_audit_log IS
    'Immutable audit history for dynamic tenant lifecycle mutations.';
