BEGIN;

CREATE TABLE runtime_deployments (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    process_model_id UUID NOT NULL,
    process_model_version_id UUID NOT NULL,
    target_environment TEXT NOT NULL CHECK (length(target_environment) BETWEEN 1 AND 128),
    deployment_sequence BIGINT NOT NULL CHECK (deployment_sequence > 0),
    status TEXT NOT NULL CHECK (status IN (
      'QUEUED', 'COMPILING', 'STAGING', 'ACTIVATING', 'ACTIVE',
      'ROLLING_BACK', 'ROLLED_BACK', 'FAILED'
    )),
    artifact_id TEXT CHECK (artifact_id IS NULL OR artifact_id ~ '^[0-9a-f]{64}$'),
    artifact_sha256 BYTEA CHECK (artifact_sha256 IS NULL OR octet_length(artifact_sha256) = 32),
    previous_deployment_id UUID,
    engine_receipt JSONB,
    failure_code TEXT CHECK (failure_code IS NULL OR length(failure_code) <= 128),
    failure_message TEXT CHECK (failure_message IS NULL OR length(failure_message) <= 2000),
    actor_id UUID NOT NULL,
    command_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest) = 32),
    correlation_id TEXT NOT NULL CHECK (length(correlation_id) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    rolled_back_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, target_environment, deployment_sequence),
    UNIQUE (tenant_id, actor_id, idempotency_key),
    UNIQUE (tenant_id, command_id),
    FOREIGN KEY (tenant_id, process_model_id)
      REFERENCES process_models (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, previous_deployment_id)
      REFERENCES runtime_deployments (tenant_id, id) ON DELETE RESTRICT,
    CHECK ((artifact_id IS NULL) = (artifact_sha256 IS NULL)),
    CHECK ((status <> 'ACTIVE') OR (artifact_id IS NOT NULL AND activated_at IS NOT NULL)),
    CHECK ((status <> 'ROLLED_BACK') OR rolled_back_at IS NOT NULL)
);

ALTER TABLE process_model_versions
    ADD CONSTRAINT process_model_versions_deployment_scope_uq
    UNIQUE (tenant_id, process_model_id, id);
ALTER TABLE runtime_deployments
    ADD CONSTRAINT runtime_deployments_version_scope_fk
    FOREIGN KEY (tenant_id, process_model_id, process_model_version_id)
    REFERENCES process_model_versions (tenant_id, process_model_id, id) ON DELETE RESTRICT;

CREATE INDEX runtime_deployments_environment_history
    ON runtime_deployments (tenant_id, target_environment, deployment_sequence DESC);
CREATE INDEX runtime_deployments_recovery
    ON runtime_deployments (status, updated_at)
    WHERE status IN ('QUEUED', 'COMPILING', 'STAGING', 'ACTIVATING', 'ROLLING_BACK');

CREATE TABLE runtime_deployment_outbox (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    deployment_id UUID NOT NULL,
    event_type TEXT NOT NULL CHECK (length(event_type) BETWEEN 1 AND 128),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, deployment_id)
      REFERENCES runtime_deployments (tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX runtime_deployment_outbox_pending
    ON runtime_deployment_outbox (created_at, id)
    WHERE published_at IS NULL;

COMMIT;
