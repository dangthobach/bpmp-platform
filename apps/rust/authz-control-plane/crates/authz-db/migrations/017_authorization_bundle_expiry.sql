-- Track bundle validity in the outbox so Authz can renew active policy bundles
-- before local evaluators fail closed on expiry.
ALTER TABLE authorization_policy_bundle_outbox
    ADD COLUMN bundle_expires_at_epoch_ms BIGINT NOT NULL DEFAULT 0
        CHECK (bundle_expires_at_epoch_ms >= 0);

CREATE INDEX idx_authz_policy_bundle_expiry
    ON authorization_policy_bundle_outbox(bundle_expires_at_epoch_ms, tenant_id, bundle_sequence);
