-- Case Service owns this schema. Apply only to a dedicated Case database/role.
CREATE TABLE IF NOT EXISTS customer_cases (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  customer_ref text NOT NULL,
  workflow_type text NOT NULL,
  workflow_version text NOT NULL,
  business_status text NOT NULL DEFAULT 'SUBMITTED',
  orchestration_status text NOT NULL DEFAULT 'START_PENDING',
  workflow_instance_id text,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_at timestamptz NOT NULL,
  created_by text NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (length(trim(customer_ref)) > 0 AND length(trim(workflow_type)) > 0 AND length(trim(workflow_version)) > 0),
  CHECK (business_status IN ('SUBMITTED', 'IN_REVIEW', 'ADDITIONAL_DOCS_REQUIRED', 'APPROVED', 'DISBURSED', 'REJECTED')),
  CHECK (orchestration_status IN ('START_PENDING', 'RECONCILING', 'ACTIVE', 'START_FAILED', 'CLOSED')),
  CHECK (workflow_instance_id IS NULL OR length(trim(workflow_instance_id)) > 0),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS customer_cases_tenant_created_idx
  ON customer_cases (tenant_id, created_at DESC, id DESC) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS case_create_receipts (
  tenant_id uuid NOT NULL,
  actor_id text NOT NULL,
  idempotency_key text NOT NULL,
  request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
  case_id uuid NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, actor_id, idempotency_key),
  FOREIGN KEY (tenant_id, case_id) REFERENCES customer_cases (tenant_id, id)
);

CREATE TABLE IF NOT EXISTS case_outbox (
  tenant_id uuid NOT NULL,
  event_id uuid NOT NULL,
  aggregate_id uuid NOT NULL,
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  event_type text NOT NULL,
  schema_version integer NOT NULL CHECK (schema_version > 0),
  correlation_id text NOT NULL,
  causation_id text NOT NULL,
  occurred_at timestamptz NOT NULL,
  producer text NOT NULL,
  payload jsonb NOT NULL,
  published_at timestamptz,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  PRIMARY KEY (tenant_id, event_id),
  FOREIGN KEY (tenant_id, aggregate_id) REFERENCES customer_cases (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS case_outbox_pending_idx
  ON case_outbox (occurred_at, event_id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS workflow_start_jobs (
  tenant_id uuid NOT NULL,
  case_id uuid NOT NULL,
  command_id uuid NOT NULL,
  actor_id text NOT NULL,
  correlation_id text NOT NULL,
  workflow_type text NOT NULL,
  workflow_version text NOT NULL,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'RECONCILING', 'STARTED', 'FAILED_MANUAL')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  lease_until timestamptz,
  lease_token uuid,
  last_error text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, case_id),
  UNIQUE (tenant_id, command_id),
  FOREIGN KEY (tenant_id, case_id) REFERENCES customer_cases (tenant_id, id),
  CHECK ((lease_until IS NULL) = (lease_token IS NULL))
);
CREATE INDEX IF NOT EXISTS workflow_start_jobs_ready_idx
  ON workflow_start_jobs (next_attempt_at, case_id)
  WHERE status IN ('PENDING', 'RECONCILING');

CREATE TABLE IF NOT EXISTS case_inbox (
  tenant_id uuid NOT NULL,
  source text NOT NULL,
  event_id text NOT NULL,
  payload_digest bytea NOT NULL CHECK (octet_length(payload_digest) = 32),
  applied_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, source, event_id)
);
