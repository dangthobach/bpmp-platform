CREATE TABLE human_bulk_operations (
    tenant_id text NOT NULL,
    actor_id text NOT NULL,
    operation_id text NOT NULL,
    idempotency_key text NOT NULL,
    action text NOT NULL CHECK (action IN ('CLAIM','UNCLAIM','COMPLETE','DELEGATE')),
    mode text NOT NULL CHECK (mode = 'PARTIAL'),
    correlation_id text NOT NULL DEFAULT '',
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, operation_id),
    UNIQUE (tenant_id, actor_id, idempotency_key)
);

CREATE TABLE human_bulk_item_receipts (
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    work_item_id text NOT NULL,
    command_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('PENDING','SUCCEEDED','FAILED')),
    error_code text NOT NULL DEFAULT '',
    retryable boolean NOT NULL DEFAULT false,
    resulting_version bigint NOT NULL DEFAULT 0 CHECK (resulting_version >= 0),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, operation_id, work_item_id),
    UNIQUE (tenant_id, command_id),
    FOREIGN KEY (tenant_id, operation_id)
        REFERENCES human_bulk_operations (tenant_id, operation_id)
        ON DELETE RESTRICT
);

CREATE INDEX human_bulk_item_receipts_pending_idx
    ON human_bulk_item_receipts (tenant_id, operation_id, work_item_id)
    WHERE status = 'PENDING' OR (status = 'FAILED' AND retryable);
