-- Transactional Outbox table.
-- Inserts run in the SAME transaction as the aggregate change.
-- A background worker polls UNPROCESSED rows with `FOR UPDATE SKIP LOCKED`
-- to allow multiple replicas without losing or duplicating events.

CREATE TABLE IF NOT EXISTS app_outbox (
    id              UUID PRIMARY KEY,
    tenant_id       UUID         NOT NULL,
    aggregate_type  VARCHAR(64)  NOT NULL,
    aggregate_id    UUID         NOT NULL,
    event_type      VARCHAR(128) NOT NULL,
    payload         JSONB        NOT NULL,
    diff            JSONB,
    occurred_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ
);

-- Partial index keeps the worker probe O(log N) even when the table grows.
CREATE INDEX IF NOT EXISTS idx_app_outbox_unprocessed
    ON app_outbox(occurred_at)
    WHERE processed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_app_outbox_tenant
    ON app_outbox(tenant_id, occurred_at);
