-- Phase 4: Document Adapter owned schema (isolated database, no cross-service SQL).
-- Case Service and BPMP Engine store only document references, never object bytes.

CREATE TABLE IF NOT EXISTS document_upload_sessions (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    owner_ref_type TEXT NOT NULL,
    owner_ref_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    declared_mime TEXT NOT NULL,
    declared_size_bytes BIGINT NOT NULL CHECK (declared_size_bytes > 0 AND declared_size_bytes <= 52428800),
    quarantine_object_key TEXT NOT NULL,
    checksum_sha256 TEXT,
    status TEXT NOT NULL CHECK (status IN ('UPLOAD_PENDING', 'QUARANTINED', 'SCANNING', 'CLEAN', 'REJECTED', 'EXPIRED')),
    version BIGINT NOT NULL DEFAULT 1,
    expires_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_doc_uploads_tenant_owner
    ON document_upload_sessions (tenant_id, owner_ref_type, owner_ref_id, created_at DESC);

CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    upload_session_id TEXT NOT NULL REFERENCES document_upload_sessions(id),
    owner_ref_type TEXT NOT NULL,
    owner_ref_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 52428800),
    content_sha256 TEXT NOT NULL,
    storage_object_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('QUARANTINED', 'SCANNING', 'CLEAN', 'REJECTED', 'DELETED')),
    legal_hold BOOLEAN NOT NULL DEFAULT FALSE,
    retention_until TIMESTAMPTZ,
    ocr_status TEXT NOT NULL DEFAULT 'NOT_REQUESTED',
    version BIGINT NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_documents_tenant_created
    ON documents (tenant_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_documents_tenant_owner
    ON documents (tenant_id, owner_ref_type, owner_ref_id);

CREATE TABLE IF NOT EXISTS document_scan_callbacks_inbox (
    provider_key TEXT NOT NULL,
    external_event_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    document_version BIGINT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('CLEAN', 'REJECTED')),
    payload_digest TEXT NOT NULL,
    receipt_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('APPLIED', 'DUPLICATE', 'IGNORED_STALE', 'REJECTED_CONFLICT')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider_key, external_event_id)
);

CREATE TABLE IF NOT EXISTS document_idempotency_receipts (
    tenant_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    response_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, actor_id, operation, idempotency_key)
);

CREATE TABLE IF NOT EXISTS document_outbox (
    event_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL DEFAULT 'Document',
    aggregate_id TEXT NOT NULL,
    aggregate_version BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    UNIQUE (tenant_id, aggregate_id, aggregate_version, event_type)
);

CREATE INDEX IF NOT EXISTS idx_document_outbox_unpublished
    ON document_outbox (created_at ASC)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS document_audit_log (
    audit_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    action TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    details_json JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_document_audit_tenant_doc
    ON document_audit_log (tenant_id, document_id, occurred_at DESC);
