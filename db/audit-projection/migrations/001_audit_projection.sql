-- Phase 5: Rebuildable Audit Projection schema (NOT the immutable business audit source of truth).
-- Authoritative business audit remains owned by each bounded context; this index can be wiped and rebuilt.

CREATE TABLE IF NOT EXISTS audit_projection_entries (
    tenant_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    source_service TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    action TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    redacted_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    legal_hold BOOLEAN NOT NULL DEFAULT FALSE,
    projection_generation BIGINT NOT NULL DEFAULT 1,
    projected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_audit_proj_tenant_time
    ON audit_projection_entries (tenant_id, occurred_at DESC, event_id DESC);

CREATE TABLE IF NOT EXISTS audit_projection_checkpoints (
    source_topic TEXT NOT NULL,
    partition_id INT NOT NULL,
    last_offset BIGINT NOT NULL,
    projection_generation BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_topic, partition_id)
);

CREATE TABLE IF NOT EXISTS audit_retention_policies (
    tenant_id TEXT PRIMARY KEY,
    retention_days INT NOT NULL CHECK (retention_days >= 30),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
