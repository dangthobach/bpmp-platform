-- Link tenant-scoped BPMP access profiles to an external identity without
-- storing credentials or treating IdP role claims as BPMP authorization.
ALTER TABLE user_account
    ADD COLUMN idp_issuer VARCHAR(512),
    ADD COLUMN idp_subject VARCHAR(255);

-- Existing external_id values are intentionally not backfilled: they do not
-- prove which issuer issued the subject. Resolve links only after verified
-- administrative provisioning or an authenticated linking flow.
ALTER TABLE user_account
    ADD CONSTRAINT user_account_idp_link_pair CHECK (
        (idp_issuer IS NULL AND idp_subject IS NULL)
        OR (idp_issuer IS NOT NULL AND idp_subject IS NOT NULL)
    );

CREATE UNIQUE INDEX uq_user_account_tenant_idp_subject
    ON user_account (tenant_id, idp_issuer, idp_subject)
    WHERE idp_issuer IS NOT NULL AND idp_subject IS NOT NULL AND NOT is_deleted;

CREATE INDEX idx_user_account_idp_subject
    ON user_account (idp_issuer, idp_subject)
    WHERE idp_issuer IS NOT NULL AND idp_subject IS NOT NULL AND NOT is_deleted;

CREATE TABLE identity_profile_mutation_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    operation VARCHAR(32) NOT NULL CHECK (operation IN ('PROVISION_LINK')),
    entity_version BIGINT NOT NULL CHECK (entity_version >= 0),
    actor_ref TEXT NOT NULL CHECK (length(trim(actor_ref)) > 0),
    executor_ref TEXT NOT NULL CHECK (length(trim(executor_ref)) > 0),
    request_id TEXT NOT NULL CHECK (length(trim(request_id)) > 0),
    idempotency_key_hash BYTEA NOT NULL CHECK (octet_length(idempotency_key_hash) = 32),
    identity_fingerprint BYTEA NOT NULL CHECK (octet_length(identity_fingerprint) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX idx_identity_profile_audit_user
    ON identity_profile_mutation_audit (tenant_id, user_id, created_at DESC);

CREATE TABLE identity_profile_idempotency (
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    actor_ref TEXT NOT NULL,
    operation VARCHAR(32) NOT NULL,
    idempotency_key_hash BYTEA NOT NULL CHECK (octet_length(idempotency_key_hash) = 32),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (clock_timestamp() + interval '7 days'),
    PRIMARY KEY (tenant_id, actor_ref, operation, idempotency_key_hash),
    CHECK (expires_at > created_at)
);

CREATE INDEX idx_identity_profile_idempotency_expiry
    ON identity_profile_idempotency (expires_at);
