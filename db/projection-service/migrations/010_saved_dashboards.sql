CREATE TABLE IF NOT EXISTS projection_saved_dashboards (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    owner_subject TEXT NOT NULL,
    name TEXT NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 256),
    version BIGINT NOT NULL CHECK (version > 0),
    config_json JSONB NOT NULL CHECK (octet_length(config_json::text) <= 65536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS projection_saved_dashboards_owner_page_idx
    ON projection_saved_dashboards (tenant_id, owner_subject, updated_at DESC, id DESC);
