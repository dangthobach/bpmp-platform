-- Keep the latest verified policy bundle per tenant so evaluator state survives
-- process restarts and is durable before a Kafka publication is acknowledged.
CREATE TABLE projection_authorization_policy_bundle_state (
    tenant_id TEXT PRIMARY KEY CHECK (length(tenant_id) BETWEEN 1 AND 200),
    bundle_sequence BIGINT NOT NULL CHECK (bundle_sequence > 0),
    catalog_version BIGINT NOT NULL CHECK (catalog_version > 0),
    revoke_epoch BIGINT NOT NULL CHECK (revoke_epoch >= 0),
    content_hash BYTEA NOT NULL CHECK (octet_length(content_hash) = 32),
    bundle_bytes BYTEA NOT NULL CHECK (octet_length(bundle_bytes) > 0),
    installed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
