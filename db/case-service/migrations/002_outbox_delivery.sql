-- Leased outbox delivery; consumers must deduplicate by (tenant_id, event_id).
ALTER TABLE case_outbox ADD COLUMN lease_token uuid;
ALTER TABLE case_outbox ADD COLUMN lease_until timestamptz;
ALTER TABLE case_outbox ADD COLUMN next_attempt_at timestamptz;
ALTER TABLE case_outbox ADD CONSTRAINT case_outbox_lease_pair
  CHECK ((lease_token IS NULL) = (lease_until IS NULL));
UPDATE case_outbox SET next_attempt_at = occurred_at WHERE next_attempt_at IS NULL;
ALTER TABLE case_outbox ALTER COLUMN next_attempt_at SET NOT NULL;
ALTER TABLE case_outbox ALTER COLUMN next_attempt_at SET DEFAULT now();
CREATE INDEX case_outbox_delivery_idx ON case_outbox (next_attempt_at, occurred_at, event_id)
  WHERE published_at IS NULL;
CREATE UNIQUE INDEX case_outbox_aggregate_version_idx
  ON case_outbox (tenant_id, aggregate_id, aggregate_version);
