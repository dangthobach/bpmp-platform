-- Versioned authorization catalog snapshots. The catalog is owned by Authz;
-- runtime configuration remains operational configuration only.
CREATE TABLE authorization_owner_manifest (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id VARCHAR(200) NOT NULL,
    manifest_version VARCHAR(100) NOT NULL,
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest) = 32),
    entries JSONB NOT NULL,
    state VARCHAR(16) NOT NULL CHECK (state IN ('ACTIVE', 'RETIRED')),
    registered_by VARCHAR(300) NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, manifest_version)
);
CREATE UNIQUE INDEX idx_authz_owner_one_active_manifest
    ON authorization_owner_manifest(owner_id) WHERE state = 'ACTIVE';
CREATE TABLE authorization_owner_manifest_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    manifest_id UUID NOT NULL REFERENCES authorization_owner_manifest(id) ON DELETE RESTRICT,
    owner_id VARCHAR(200) NOT NULL,
    actor_ref VARCHAR(300) NOT NULL,
    request_id VARCHAR(200) NOT NULL,
    action VARCHAR(16) NOT NULL CHECK (action IN ('REGISTER', 'RETIRE')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE authorization_catalog_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    version_num BIGINT NOT NULL CHECK (version_num > 0),
    state VARCHAR(16) NOT NULL CHECK (state IN ('DRAFT', 'VALIDATED', 'PUBLISHED', 'RETIRED')),
    draft_revision BIGINT NOT NULL DEFAULT 1 CHECK (draft_revision > 0),
    -- Snapshot includes owner, resource/action codes, schemas, and compatibility metadata.
    -- Published snapshots are immutable; edits create a new version.
    snapshot JSONB NOT NULL,
    content_hash BYTEA,
    signing_key_id VARCHAR(200),
    signature BYTEA,
    bundle_bytes BYTEA,
    created_by VARCHAR(300) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    validated_by VARCHAR(300),
    validated_at TIMESTAMPTZ,
    published_by VARCHAR(300),
    published_at TIMESTAMPTZ,
    UNIQUE (tenant_id, version_num),
    CHECK (
        (state <> 'PUBLISHED') OR
        (content_hash IS NOT NULL AND signing_key_id IS NOT NULL AND signature IS NOT NULL AND bundle_bytes IS NOT NULL AND published_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX idx_authz_catalog_one_published_per_tenant
    ON authorization_catalog_version(tenant_id)
    WHERE state = 'PUBLISHED';
CREATE INDEX idx_authz_catalog_tenant_versions
    ON authorization_catalog_version(tenant_id, version_num DESC);

CREATE TABLE authorization_catalog_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    catalog_version_id UUID NOT NULL REFERENCES authorization_catalog_version(id) ON DELETE RESTRICT,
    actor_ref VARCHAR(300) NOT NULL,
    request_id VARCHAR(200) NOT NULL,
    action VARCHAR(32) NOT NULL CHECK (action IN ('CREATE_DRAFT', 'UPDATE_DRAFT', 'VALIDATE', 'PUBLISH', 'RETIRE')),
    reason TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_authz_catalog_audit_tenant_time
    ON authorization_catalog_audit(tenant_id, occurred_at DESC);

CREATE TABLE authorization_policy_bundle_outbox (
    event_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    catalog_version_id UUID NOT NULL REFERENCES authorization_catalog_version(id) ON DELETE RESTRICT,
    bundle_sequence BIGINT NOT NULL CHECK (bundle_sequence > 0),
    bundle_bytes BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    delivery_attempts INT NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    last_delivery_error TEXT,
    UNIQUE (tenant_id, bundle_sequence)
);
CREATE INDEX idx_authz_policy_bundle_outbox_pending
    ON authorization_policy_bundle_outbox(created_at, event_id)
    WHERE delivered_at IS NULL;

CREATE TABLE authorization_policy_bundle_acknowledgement (
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    bundle_sequence BIGINT NOT NULL CHECK (bundle_sequence > 0),
    catalog_version_id UUID NOT NULL REFERENCES authorization_catalog_version(id) ON DELETE RESTRICT,
    consumer_id VARCHAR(300) NOT NULL,
    consumer_type VARCHAR(100) NOT NULL,
    bundle_content_hash BYTEA NOT NULL CHECK (octet_length(bundle_content_hash) = 32),
    acknowledged_at_epoch_ms BIGINT NOT NULL CHECK (acknowledged_at_epoch_ms > 0),
    PRIMARY KEY (tenant_id, bundle_sequence, consumer_id),
    FOREIGN KEY (tenant_id, bundle_sequence)
        REFERENCES authorization_policy_bundle_outbox(tenant_id, bundle_sequence) ON DELETE RESTRICT
);
CREATE INDEX idx_authz_policy_bundle_ack_version
    ON authorization_policy_bundle_acknowledgement(tenant_id, catalog_version_id, acknowledged_at_epoch_ms DESC);
