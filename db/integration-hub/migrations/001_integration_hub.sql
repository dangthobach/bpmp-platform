-- Phase 4: Integration Hub owned schema (isolated database, no cross-service SQL).
-- Integration Hub owns external endpoint registry, callback inbox, delivery ledger, and circuit state.

CREATE TABLE IF NOT EXISTS integration_endpoints (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    system_type TEXT NOT NULL CHECK (system_type IN ('T24_ESB', 'AML', 'RPA', 'IDP_OCR', 'WEBHOOK')),
    base_url TEXT NOT NULL,
    secret_ref TEXT NOT NULL,
    auth_mode TEXT NOT NULL CHECK (auth_mode IN ('MTLS', 'HMAC_SHA256', 'OAUTH2_CLIENT_CREDENTIALS')),
    timeout_ms INT NOT NULL CHECK (timeout_ms > 0 AND timeout_ms <= 30000),
    max_attempts INT NOT NULL CHECK (max_attempts >= 1 AND max_attempts <= 10),
    circuit_failure_threshold INT NOT NULL DEFAULT 5,
    circuit_open_seconds INT NOT NULL DEFAULT 30,
    bulkhead_max_concurrency INT NOT NULL DEFAULT 16,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED', 'DELETED')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_integration_endpoints_tenant
    ON integration_endpoints (tenant_id, status, id);

CREATE TABLE IF NOT EXISTS integration_deliveries (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    endpoint_id TEXT NOT NULL REFERENCES integration_endpoints(id),
    correlation_id TEXT NOT NULL,
    downstream_idempotency_key TEXT NOT NULL,
    payload_digest TEXT NOT NULL,
    redacted_summary TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('QUEUED', 'DISPATCHING', 'RETRY_WAIT', 'SUCCEEDED', 'FAILED_TERMINAL', 'RECONCILING', 'COMPENSATED')),
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ,
    last_response_class TEXT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_integration_deliveries_tenant
    ON integration_deliveries (tenant_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS integration_callback_inbox (
    binding_id TEXT NOT NULL,
    provider_key TEXT NOT NULL,
    external_event_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    payload_digest TEXT NOT NULL,
    receipt_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('VERIFIED', 'DUPLICATE', 'REJECTED')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (binding_id, provider_key, external_event_id)
);

CREATE TABLE IF NOT EXISTS integration_idempotency_receipts (
    tenant_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    response_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, actor_id, operation, idempotency_key)
);

CREATE TABLE IF NOT EXISTS integration_outbox (
    event_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    aggregate_version BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS integration_audit_log (
    audit_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    action TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    details TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
