-- Phase 5: Master Data (MDM) owned schema (isolated database, immutable versioned publications).

CREATE TABLE IF NOT EXISTS mdm_datasets (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    current_publication_version BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, code)
);

CREATE TABLE IF NOT EXISTS mdm_publications (
    dataset_id TEXT NOT NULL REFERENCES mdm_datasets(id),
    publication_version BIGINT NOT NULL,
    tenant_id TEXT NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL,
    effective_to TIMESTAMPTZ,
    content_sha256 TEXT NOT NULL,
    records_json JSONB NOT NULL,
    rollback_of_version BIGINT,
    published_by TEXT NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dataset_id, publication_version)
);

CREATE TABLE IF NOT EXISTS mdm_consumer_acks (
    tenant_id TEXT NOT NULL,
    dataset_id TEXT NOT NULL REFERENCES mdm_datasets(id),
    consumer_id TEXT NOT NULL,
    acknowledged_version BIGINT NOT NULL,
    acked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, dataset_id, consumer_id)
);

CREATE TABLE IF NOT EXISTS mdm_outbox (
    event_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    dataset_id TEXT NOT NULL,
    publication_version BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
