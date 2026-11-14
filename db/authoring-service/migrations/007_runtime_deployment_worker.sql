BEGIN;

ALTER TABLE runtime_deployments
    DROP CONSTRAINT runtime_deployments_status_check;
ALTER TABLE runtime_deployments
    ADD CONSTRAINT runtime_deployments_status_check CHECK (status IN (
      'QUEUED', 'COMPILING', 'STAGING', 'ACTIVATING', 'ACTIVE',
      'ROLLING_BACK', 'ROLLED_BACK', 'SUPERSEDED', 'FAILED'
    ));
ALTER TABLE runtime_deployments
    ADD COLUMN action_type TEXT NOT NULL DEFAULT 'DEPLOY'
      CHECK (action_type IN ('DEPLOY', 'ROLLBACK')),
    ADD COLUMN workflow_type TEXT,
    ADD COLUMN workflow_version TEXT,
    ADD COLUMN engine_config_version TEXT,
    ADD COLUMN engine_policy_version TEXT,
    ADD COLUMN engine_config_sha256 TEXT,
    ADD COLUMN governance_config_version TEXT,
    ADD COLUMN governance_policy_version TEXT,
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    ADD COLUMN lease_owner TEXT,
    ADD COLUMN lease_until TIMESTAMPTZ;

CREATE TABLE runtime_deployment_sequences (
    tenant_id UUID NOT NULL,
    target_environment TEXT NOT NULL CHECK (length(target_environment) BETWEEN 1 AND 128),
    last_sequence BIGINT NOT NULL CHECK (last_sequence > 0),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, target_environment)
);

CREATE INDEX runtime_deployments_claim
    ON runtime_deployments (next_attempt_at, created_at, id)
    WHERE status IN ('QUEUED', 'COMPILING', 'STAGING', 'ACTIVATING', 'ROLLING_BACK');
CREATE INDEX runtime_deployment_outbox_claim
    ON runtime_deployment_outbox (created_at, id)
    WHERE published_at IS NULL;

COMMIT;
