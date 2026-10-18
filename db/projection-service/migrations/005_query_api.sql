CREATE TABLE IF NOT EXISTS projection_saved_queries (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    owner_subject TEXT NOT NULL,
    name TEXT NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 256),
    version BIGINT NOT NULL CHECK (version > 0),
    query_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS projection_saved_queries_owner_page_idx ON projection_saved_queries(tenant_id, owner_subject, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS projection_query_exports (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    owner_subject TEXT NOT NULL,
    status SMALLINT NOT NULL,
    query_json JSONB NOT NULL,
    max_rows INTEGER NOT NULL CHECK (max_rows BETWEEN 1 AND 1000000),
    row_count BIGINT NOT NULL DEFAULT 0,
    csv BYTEA,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS projection_query_exports_owner_idx ON projection_query_exports(tenant_id, owner_subject, created_at DESC);
