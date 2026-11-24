-- Case-owned projection of committed Engine events. The raw Engine envelope and
-- business payload are deliberately not retained in this schema.
ALTER TABLE customer_cases
  ADD COLUMN engine_sequence bigint NOT NULL DEFAULT 0 CHECK (engine_sequence >= 0);

CREATE TABLE case_engine_events (
  tenant_id uuid NOT NULL,
  case_id uuid NOT NULL,
  event_id text NOT NULL CHECK (length(event_id) BETWEEN 1 AND 256),
  engine_sequence bigint NOT NULL CHECK (engine_sequence > 0),
  event_type text NOT NULL CHECK (length(event_type) BETWEEN 1 AND 128),
  node_id text NOT NULL DEFAULT '' CHECK (length(node_id) <= 256),
  occurred_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, event_id),
  UNIQUE (tenant_id, case_id, engine_sequence),
  FOREIGN KEY (tenant_id, case_id) REFERENCES customer_cases (tenant_id, id)
);
CREATE INDEX case_engine_events_timeline_idx
  ON case_engine_events (tenant_id, case_id, engine_sequence DESC);
