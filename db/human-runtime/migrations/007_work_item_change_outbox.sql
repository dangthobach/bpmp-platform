CREATE TABLE IF NOT EXISTS human_work_item_change_outbox (
    tenant_id text NOT NULL,
    event_id text NOT NULL,
    work_item_id text NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL,
    occurred_at timestamptz NOT NULL,
    correlation_id text NOT NULL DEFAULT '',
    payload bytea NOT NULL CHECK (octet_length(payload) > 0 AND octet_length(payload) <= 65536),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL,
    lease_owner text,
    lease_expires_at timestamptz,
    published_at timestamptz,
    is_deleted boolean NOT NULL DEFAULT false CHECK (NOT is_deleted),
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    PRIMARY KEY (tenant_id, event_id),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX IF NOT EXISTS human_work_item_change_outbox_due_idx
    ON human_work_item_change_outbox (available_at, tenant_id, event_id)
    WHERE published_at IS NULL AND NOT is_deleted;
