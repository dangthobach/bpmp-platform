CREATE TABLE IF NOT EXISTS governance_approval_requests (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    idempotency_key text NOT NULL,
    instance_id text NOT NULL,
    workflow_type text NOT NULL,
    workflow_version text NOT NULL,
    policy_id text NOT NULL,
    legal_deadline_epoch_ms bigint NOT NULL CHECK (legal_deadline_epoch_ms > 0),
    key_scope text NOT NULL,
    key_epoch bigint NOT NULL CHECK (key_epoch > 0),
    reason_code text NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    pending_ledger_digest bytea NOT NULL CHECK (octet_length(pending_ledger_digest) = 32),
    expected_version bigint NOT NULL CHECK (expected_version >= 0),
    config_version text NOT NULL,
    policy_version text NOT NULL,
    status text NOT NULL CHECK (status IN (
        'PENDING', 'APPROVED', 'COMMITTED', 'KEY_SHRED_PENDING', 'COMPLETED', 'REJECTED'
    )),
    aggregate_version bigint NOT NULL DEFAULT 1 CHECK (aggregate_version > 0),
    committed_command_id text NOT NULL DEFAULT '',
    committed_sequence bigint NOT NULL DEFAULT 0 CHECK (committed_sequence >= 0),
    last_error text NOT NULL DEFAULT '',
    shred_attempts integer NOT NULL DEFAULT 0 CHECK (shred_attempts >= 0),
    shred_available_at timestamptz,
    shred_lease_until timestamptz,
    shred_worker_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_at_epoch_ms bigint NOT NULL CHECK (created_at_epoch_ms > 0),
    updated_at_epoch_ms bigint NOT NULL CHECK (updated_at_epoch_ms > 0),
    PRIMARY KEY (tenant_id, request_id),
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS governance_approval_requests_shred_idx
    ON governance_approval_requests (shred_available_at, tenant_id, request_id)
    WHERE status = 'KEY_SHRED_PENDING';

CREATE TABLE IF NOT EXISTS governance_signed_approvals (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('REQUESTER', 'APPROVER')),
    actor_id text NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    capability text NOT NULL,
    auth_assurance text NOT NULL,
    approved_at_epoch_ms bigint NOT NULL CHECK (approved_at_epoch_ms > 0),
    expires_at_epoch_ms bigint NOT NULL CHECK (expires_at_epoch_ms > approved_at_epoch_ms),
    key_id text NOT NULL,
    signature bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, request_id, role, actor_id),
    FOREIGN KEY (tenant_id, request_id)
        REFERENCES governance_approval_requests (tenant_id, request_id)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS governance_service_audit (
    audit_id bigserial PRIMARY KEY,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    action text NOT NULL,
    actor_id text NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    occurred_at_epoch_ms bigint NOT NULL CHECK (occurred_at_epoch_ms > 0),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION governance_reject_audit_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'governance audit is append-only';
END;
$$;

DROP TRIGGER IF EXISTS governance_audit_no_update ON governance_service_audit;
CREATE TRIGGER governance_audit_no_update
BEFORE UPDATE OR DELETE ON governance_service_audit
FOR EACH ROW EXECUTE FUNCTION governance_reject_audit_mutation();
