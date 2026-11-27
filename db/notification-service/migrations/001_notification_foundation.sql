-- Notification Service owns this schema (ADR-013). Apply only to a dedicated Notification database/role.

-- 1. Classification & Producers
CREATE TABLE IF NOT EXISTS categories (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  category_key text NOT NULL,
  name text NOT NULL,
  default_lane text NOT NULL,
  user_unsubscribable boolean NOT NULL,
  ambiguous_outcome_policy text NOT NULL,
  retention_class text NOT NULL,
  retain_rendered_copy boolean NOT NULL,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (length(trim(category_key)) > 0 AND length(trim(name)) > 0),
  CHECK (default_lane IN ('TRANSACTIONAL', 'NORMAL', 'BULK')),
  CHECK (ambiguous_outcome_policy IN ('RESEND_ON_FAILOVER', 'RECONCILE_BEFORE_RETRY', 'NO_RETRY')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS categories_tenant_key_uidx
  ON categories (tenant_id, category_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS applications (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  app_key text NOT NULL,
  name text NOT NULL,
  auth_mode text NOT NULL,
  principal_ref text NOT NULL,
  allowed_namespaces jsonb NOT NULL,
  allowed_channels jsonb NOT NULL,
  default_lane text NOT NULL,
  quota_config jsonb NOT NULL,
  callback_url text,
  callback_secret_ref text,
  owner_team text,
  default_sender_identity jsonb,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (length(trim(app_key)) > 0 AND length(trim(name)) > 0 AND length(trim(principal_ref)) > 0),
  CHECK (auth_mode IN ('WORKLOAD_MTLS', 'OIDC_CLIENT', 'API_KEY')),
  CHECK (default_lane IN ('TRANSACTIONAL', 'NORMAL', 'BULK')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS applications_tenant_key_uidx
  ON applications (tenant_id, app_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS application_api_keys (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  application_id uuid NOT NULL,
  key_prefix text NOT NULL,
  secret_hash text NOT NULL,
  status text NOT NULL,
  expires_at timestamptz,
  last_used_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, application_id) REFERENCES applications (tenant_id, id),
  CHECK (length(trim(key_prefix)) > 0 AND length(trim(secret_hash)) > 0),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS application_api_keys_prefix_uidx
  ON application_api_keys (tenant_id, key_prefix) WHERE NOT is_deleted;

-- 2. Templates, Layouts & Releases
CREATE TABLE IF NOT EXISTS layouts (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  layout_key text NOT NULL,
  name text NOT NULL,
  channel text NOT NULL,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (length(trim(layout_key)) > 0 AND length(trim(name)) > 0),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS layouts_tenant_key_uidx
  ON layouts (tenant_id, layout_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS layout_versions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  layout_id uuid NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  syntax text NOT NULL,
  variable_schema jsonb NOT NULL,
  content jsonb NOT NULL,
  content_hash text NOT NULL,
  status text NOT NULL,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, layout_id) REFERENCES layouts (tenant_id, id),
  CHECK (status IN ('DRAFT', 'IN_REVIEW', 'PUBLISHED', 'DEPRECATED', 'RETIRED')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS layout_versions_layout_ver_uidx
  ON layout_versions (tenant_id, layout_id, version) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS templates (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  template_key text NOT NULL,
  application_id uuid NOT NULL,
  category_id uuid NOT NULL,
  name text NOT NULL,
  default_locale text NOT NULL,
  tags jsonb NOT NULL DEFAULT '[]'::jsonb,
  provenance jsonb,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, application_id) REFERENCES applications (tenant_id, id),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  CHECK (length(trim(template_key)) > 0 AND length(trim(name)) > 0 AND length(trim(default_locale)) > 0),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS templates_tenant_key_uidx
  ON templates (tenant_id, template_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS template_versions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  template_id uuid NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  schema_major int NOT NULL CHECK (schema_major > 0),
  syntax text NOT NULL,
  layout_version_id uuid,
  variable_schema jsonb NOT NULL,
  content_hash text NOT NULL,
  source_commit text,
  change_note text,
  approval_ref text,
  status text NOT NULL,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES templates (tenant_id, id),
  FOREIGN KEY (tenant_id, layout_version_id) REFERENCES layout_versions (tenant_id, id),
  CHECK (status IN ('DRAFT', 'IN_REVIEW', 'PUBLISHED', 'DEPRECATED', 'RETIRED')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS template_versions_tmpl_ver_uidx
  ON template_versions (tenant_id, template_id, version) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS template_variants (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  template_version_id uuid NOT NULL,
  channel text NOT NULL,
  locale text NOT NULL,
  variant_kind text NOT NULL,
  subject_tmpl text,
  html_tmpl text,
  text_tmpl text,
  mjml_source text,
  provider_template_id text,
  param_mapping jsonb,
  variant_hash text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, template_version_id) REFERENCES template_versions (tenant_id, id),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK (variant_kind IN ('INLINE', 'PROVIDER_TEMPLATE')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS template_variants_ver_chan_loc_uidx
  ON template_variants (tenant_id, template_version_id, channel, locale) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS template_releases (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  template_id uuid NOT NULL,
  schema_major int NOT NULL CHECK (schema_major > 0),
  template_version_id uuid NOT NULL,
  ordinal bigint NOT NULL CHECK (ordinal > 0),
  previous_ordinal bigint,
  activate_at timestamptz NOT NULL,
  activated_at timestamptz,
  approval_ref text,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES templates (tenant_id, id),
  FOREIGN KEY (tenant_id, template_version_id) REFERENCES template_versions (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS template_releases_ordinal_uidx
  ON template_releases (tenant_id, template_id, schema_major, ordinal) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS template_releases_active_idx
  ON template_releases (tenant_id, template_id, schema_major, ordinal DESC)
  WHERE NOT is_deleted AND status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS publication_releases (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  artifact_kind text NOT NULL,
  artifact_id uuid NOT NULL,
  target_version_id uuid NOT NULL,
  ordinal bigint NOT NULL CHECK (ordinal > 0),
  previous_ordinal bigint,
  activate_at timestamptz NOT NULL,
  activated_at timestamptz,
  approval_ref text,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (artifact_kind IN ('LAYOUT', 'PROVIDER_PROFILE', 'ROUTING_POLICY', 'TRIGGER_RULE')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS publication_releases_ordinal_uidx
  ON publication_releases (tenant_id, artifact_kind, artifact_id, ordinal) WHERE NOT is_deleted;

-- 3. Providers, Routing & Circuit Breakers
CREATE TABLE IF NOT EXISTS provider_profiles (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  provider_key text NOT NULL,
  name text NOT NULL,
  channel text NOT NULL,
  driver_kind text NOT NULL,
  preset_key text,
  active_ordinal bigint NOT NULL DEFAULT 0 CHECK (active_ordinal >= 0),
  operational_state text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (length(trim(provider_key)) > 0 AND length(trim(name)) > 0),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK (driver_kind IN ('smtp', 'http', 'mock', 'inapp')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_profiles_tenant_key_uidx
  ON provider_profiles (tenant_id, provider_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS provider_revisions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  provider_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  config jsonb NOT NULL,
  credentials_ref text,
  sender_identity jsonb NOT NULL,
  capabilities jsonb NOT NULL,
  quota_config jsonb NOT NULL,
  config_hash text NOT NULL,
  status text NOT NULL,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  CHECK (status IN ('DRAFT', 'IN_REVIEW', 'PUBLISHED', 'DEPRECATED', 'RETIRED')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_revisions_provider_rev_uidx
  ON provider_revisions (tenant_id, provider_id, revision) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS provider_health (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  provider_id uuid NOT NULL,
  breaker_state text NOT NULL,
  error_rate_bps int NOT NULL CHECK (error_rate_bps >= 0 AND error_rate_bps <= 10000),
  window_requests int NOT NULL CHECK (window_requests >= 0),
  window_failures int NOT NULL CHECK (window_failures >= 0),
  last_transition_at timestamptz NOT NULL,
  last_error_code text,
  updated_by_replica text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  CHECK (breaker_state IN ('CLOSED', 'OPEN', 'HALF_OPEN')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_health_provider_uidx
  ON provider_health (tenant_id, provider_id) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS breaker_overrides (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  provider_id uuid NOT NULL,
  override_mode text NOT NULL,
  reason text NOT NULL,
  ordinal bigint NOT NULL CHECK (ordinal > 0),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  CHECK (override_mode IN ('FORCE_OPEN', 'FORCE_CLOSE', 'DRAINING')),
  CHECK (length(trim(reason)) > 0),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS breaker_overrides_active_idx
  ON breaker_overrides (tenant_id, provider_id, ordinal DESC)
  WHERE NOT is_deleted AND revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS breaker_transitions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  provider_id uuid NOT NULL,
  from_state text NOT NULL,
  to_state text NOT NULL,
  trigger_kind text NOT NULL,
  reason text,
  actor_ref uuid,
  error_rate_bps int NOT NULL DEFAULT 0 CHECK (error_rate_bps >= 0 AND error_rate_bps <= 10000),
  slow_call_rate_bps int NOT NULL DEFAULT 0 CHECK (slow_call_rate_bps >= 0 AND slow_call_rate_bps <= 10000),
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  CHECK (from_state IN ('CLOSED', 'OPEN', 'HALF_OPEN')),
  CHECK (to_state IN ('CLOSED', 'OPEN', 'HALF_OPEN')),
  CHECK (length(trim(trigger_kind)) > 0),
  CHECK (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL)
);
CREATE INDEX IF NOT EXISTS breaker_transitions_provider_idx
  ON breaker_transitions (tenant_id, provider_id, occurred_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS routing_policies (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  policy_key text NOT NULL,
  application_id uuid,
  category_id uuid,
  channel text NOT NULL,
  active_ordinal bigint NOT NULL DEFAULT 0 CHECK (active_ordinal >= 0),
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, application_id) REFERENCES applications (tenant_id, id),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS routing_policies_tenant_key_uidx
  ON routing_policies (tenant_id, policy_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS routing_policy_versions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  routing_policy_id uuid NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  routes jsonb NOT NULL,
  failover_policy jsonb NOT NULL,
  digest text NOT NULL,
  status text NOT NULL,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, routing_policy_id) REFERENCES routing_policies (tenant_id, id),
  CHECK (status IN ('DRAFT', 'IN_REVIEW', 'PUBLISHED', 'DEPRECATED', 'RETIRED')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS routing_policy_versions_pol_ver_uidx
  ON routing_policy_versions (tenant_id, routing_policy_id, version) WHERE NOT is_deleted;

-- 4. Workflow Trigger Rules
CREATE TABLE IF NOT EXISTS trigger_rules (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  rule_key text NOT NULL,
  name text NOT NULL,
  workflow_type text NOT NULL,
  node_id text,
  trigger_kind text NOT NULL,
  template_id uuid NOT NULL,
  schema_major int NOT NULL CHECK (schema_major > 0),
  active_ordinal bigint NOT NULL DEFAULT 0 CHECK (active_ordinal >= 0),
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES templates (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS trigger_rules_tenant_key_uidx
  ON trigger_rules (tenant_id, rule_key) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS trigger_rule_revisions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  rule_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  version_selector text NOT NULL,
  condition_cel text,
  recipient_spec jsonb NOT NULL,
  data_mapping_cel jsonb NOT NULL,
  referenced_vars jsonb NOT NULL,
  delay_seconds int NOT NULL DEFAULT 0 CHECK (delay_seconds >= 0),
  dedup_window_seconds int NOT NULL DEFAULT 0 CHECK (dedup_window_seconds >= 0),
  digest text NOT NULL,
  status text NOT NULL,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, rule_id) REFERENCES trigger_rules (tenant_id, id),
  CHECK (status IN ('DRAFT', 'IN_REVIEW', 'PUBLISHED', 'DEPRECATED', 'RETIRED')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS trigger_rule_revisions_rule_rev_uidx
  ON trigger_rule_revisions (tenant_id, rule_id, revision) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS workflow_instance_contexts (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  instance_ref text NOT NULL,
  workflow_type text NOT NULL,
  initiator_subject_ref uuid,
  encrypted_vars bytea NOT NULL,
  key_ref text NOT NULL,
  workflow_completed_at timestamptz,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS workflow_instance_contexts_instance_uidx
  ON workflow_instance_contexts (tenant_id, instance_ref) WHERE NOT is_deleted;

-- 5. Recipients, Preferences & Suppressions
CREATE TABLE IF NOT EXISTS subject_contacts (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  subject_ref uuid NOT NULL,
  channel text NOT NULL,
  encrypted_address bytea NOT NULL,
  address_hash text NOT NULL,
  masked_address text NOT NULL,
  locale text,
  timezone text,
  verified boolean NOT NULL DEFAULT false,
  source_version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS subject_contacts_subject_channel_idx
  ON subject_contacts (tenant_id, subject_ref, channel) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS recipient_preferences (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  subject_ref uuid NOT NULL,
  category_id uuid NOT NULL,
  channel text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  quiet_hours jsonb,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS recipient_preferences_subj_cat_chan_uidx
  ON recipient_preferences (tenant_id, subject_ref, category_id, channel) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS suppressions (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  channel text NOT NULL,
  address_hash text NOT NULL,
  masked_address text NOT NULL,
  reason text NOT NULL,
  source_provider_id uuid,
  expires_at timestamptz,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, source_provider_id) REFERENCES provider_profiles (tenant_id, id),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS suppressions_active_address_uidx
  ON suppressions (tenant_id, channel, address_hash) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS suppressions_keyset_idx
  ON suppressions (tenant_id, created_at DESC, id DESC) WHERE NOT is_deleted;

-- 6. Requests, Deliveries, In-App Inbox, Metrics & Owner Infrastructure
CREATE TABLE IF NOT EXISTS notification_requests (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  application_id uuid NOT NULL,
  idempotency_key text NOT NULL,
  request_digest text NOT NULL,
  template_id uuid NOT NULL,
  schema_major int NOT NULL CHECK (schema_major > 0),
  pinned_version bigint,
  category_id uuid NOT NULL,
  lane text NOT NULL,
  priority smallint NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 3),
  correlation_id text,
  trigger_rule_revision_id uuid,
  is_test boolean NOT NULL DEFAULT false,
  send_at timestamptz,
  expires_at timestamptz,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, application_id) REFERENCES applications (tenant_id, id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES templates (tenant_id, id),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  FOREIGN KEY (tenant_id, trigger_rule_revision_id) REFERENCES trigger_rule_revisions (tenant_id, id),
  CHECK (length(trim(idempotency_key)) > 0 AND length(trim(request_digest)) > 0),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS notification_requests_idempotency_uidx
  ON notification_requests (tenant_id, application_id, idempotency_key) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS notification_requests_keyset_idx
  ON notification_requests (tenant_id, created_at DESC, id DESC) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS notification_requests_correlation_idx
  ON notification_requests (tenant_id, correlation_id) WHERE correlation_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS deliveries (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  request_id uuid NOT NULL,
  template_version_id uuid NOT NULL,
  variant_id uuid NOT NULL,
  release_ordinal bigint NOT NULL CHECK (release_ordinal >= 0),
  recipient_subject_ref uuid,
  address_hash text NOT NULL,
  masked_address text NOT NULL,
  channel text NOT NULL,
  lane text NOT NULL,
  status text NOT NULL,
  selected_provider_id uuid,
  provider_message_id text,
  content_hash text NOT NULL,
  resend_of_delivery_id uuid,
  attempt_count int NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  next_attempt_at timestamptz,
  expires_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, request_id) REFERENCES notification_requests (tenant_id, id),
  FOREIGN KEY (tenant_id, template_version_id) REFERENCES template_versions (tenant_id, id),
  FOREIGN KEY (tenant_id, variant_id) REFERENCES template_variants (tenant_id, id),
  FOREIGN KEY (tenant_id, selected_provider_id) REFERENCES provider_profiles (tenant_id, id),
  FOREIGN KEY (tenant_id, resend_of_delivery_id) REFERENCES deliveries (tenant_id, id),
  CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'IN_APP', 'ZNS', 'WEBHOOK')),
  CHECK (status IN ('ACCEPTED', 'SCHEDULED', 'QUEUED', 'SENDING', 'SENT', 'DELIVERED', 'BOUNCED', 'FAILED', 'DEFERRED', 'EXPIRED', 'SUPPRESSED', 'REJECTED')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS deliveries_keyset_idx
  ON deliveries (tenant_id, created_at DESC, id DESC) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS deliveries_status_created_idx
  ON deliveries (tenant_id, status, created_at DESC, id DESC) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS deliveries_subject_created_idx
  ON deliveries (tenant_id, recipient_subject_ref, created_at DESC, id DESC)
  WHERE recipient_subject_ref IS NOT NULL AND NOT is_deleted;
CREATE INDEX IF NOT EXISTS deliveries_address_hash_created_idx
  ON deliveries (tenant_id, address_hash, created_at DESC, id DESC) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS deliveries_active_idx
  ON deliveries (tenant_id, next_attempt_at, id)
  WHERE status IN ('ACCEPTED', 'SCHEDULED', 'QUEUED', 'SENDING', 'DEFERRED') AND NOT is_deleted;

CREATE TABLE IF NOT EXISTS delivery_payloads (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  encrypted_address bytea,
  encrypted_data bytea,
  encrypted_secrets bytea,
  rendered_copy_encrypted bytea,
  key_ref text NOT NULL,
  secrets_erased_at timestamptz,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES deliveries (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS delivery_payloads_delivery_uidx
  ON delivery_payloads (tenant_id, delivery_id) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS delivery_attempts (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  provider_id uuid NOT NULL,
  provider_revision_id uuid NOT NULL,
  attempt_no int NOT NULL CHECK (attempt_no > 0),
  outcome_class text NOT NULL,
  provider_status_code text,
  provider_message_id text,
  error_code text,
  latency_ms int NOT NULL CHECK (latency_ms >= 0),
  started_at timestamptz NOT NULL,
  finished_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES deliveries (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_revision_id) REFERENCES provider_revisions (tenant_id, id),
  CHECK (outcome_class IN ('OK', 'RETRYABLE', 'THROTTLED', 'PERMANENT', 'AUTH_FAILED', 'AMBIGUOUS')),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS delivery_attempts_delivery_idx
  ON delivery_attempts (tenant_id, delivery_id, attempt_no);

CREATE TABLE IF NOT EXISTS delivery_events (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  event_type text NOT NULL,
  from_status text,
  to_status text NOT NULL,
  detail jsonb,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES deliveries (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS delivery_events_delivery_idx
  ON delivery_events (tenant_id, delivery_id, occurred_at, id);

CREATE TABLE IF NOT EXISTS provider_callbacks (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  provider_id uuid NOT NULL,
  provider_event_id text NOT NULL,
  delivery_id uuid,
  event_kind text NOT NULL,
  provider_status text NOT NULL,
  signature_valid boolean NOT NULL,
  payload_digest text NOT NULL,
  received_at timestamptz NOT NULL,
  processed_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES deliveries (tenant_id, id),
  CHECK (length(trim(provider_event_id)) > 0),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_callbacks_dedup_uidx
  ON provider_callbacks (tenant_id, provider_id, provider_event_id) WHERE NOT is_deleted;
CREATE INDEX IF NOT EXISTS provider_callbacks_keyset_idx
  ON provider_callbacks (tenant_id, received_at DESC, id DESC) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS inapp_messages (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  subject_ref uuid NOT NULL,
  category_key text NOT NULL,
  title text NOT NULL,
  body_html text NOT NULL,
  action_url text,
  metadata jsonb,
  read_at timestamptz,
  archived_at timestamptz,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES deliveries (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS inapp_messages_inbox_keyset_idx
  ON inapp_messages (tenant_id, subject_ref, created_at DESC, id DESC)
  WHERE NOT is_deleted AND archived_at IS NULL;

CREATE TABLE IF NOT EXISTS delivery_metrics_hourly (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  bucket_start timestamptz NOT NULL,
  application_id uuid NOT NULL,
  template_id uuid NOT NULL,
  category_id uuid NOT NULL,
  provider_id uuid,
  channel text NOT NULL,
  lane text NOT NULL,
  accepted_count bigint NOT NULL DEFAULT 0 CHECK (accepted_count >= 0),
  sent_count bigint NOT NULL DEFAULT 0 CHECK (sent_count >= 0),
  delivered_count bigint NOT NULL DEFAULT 0 CHECK (delivered_count >= 0),
  bounced_count bigint NOT NULL DEFAULT 0 CHECK (bounced_count >= 0),
  failed_count bigint NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
  deferred_count bigint NOT NULL DEFAULT 0 CHECK (deferred_count >= 0),
  expired_count bigint NOT NULL DEFAULT 0 CHECK (expired_count >= 0),
  suppressed_count bigint NOT NULL DEFAULT 0 CHECK (suppressed_count >= 0),
  latency_histogram jsonb,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, application_id) REFERENCES applications (tenant_id, id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES templates (tenant_id, id),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  FOREIGN KEY (tenant_id, provider_id) REFERENCES provider_profiles (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS delivery_metrics_hourly_bucket_idx
  ON delivery_metrics_hourly (tenant_id, bucket_start DESC) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS outbox (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  event_id uuid NOT NULL,
  aggregate_id uuid NOT NULL,
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  published_at timestamptz,
  attempts int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS outbox_pending_tenant_idx
  ON outbox (tenant_id, created_at, id) WHERE published_at IS NULL AND NOT is_deleted;
CREATE INDEX IF NOT EXISTS outbox_pending_global_idx
  ON outbox (created_at, id) WHERE published_at IS NULL AND NOT is_deleted;

CREATE TABLE IF NOT EXISTS inbox (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  source text NOT NULL,
  event_id text NOT NULL,
  payload_digest text NOT NULL,
  applied_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS inbox_source_event_uidx
  ON inbox (tenant_id, source, event_id) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS command_receipts (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  command_id uuid NOT NULL,
  intent_digest text NOT NULL,
  actor_ref uuid NOT NULL,
  aggregate_id uuid NOT NULL,
  status text NOT NULL,
  result jsonb NOT NULL,
  expires_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK ((is_deleted AND deleted_at IS NOT NULL AND deleted_by IS NOT NULL AND delete_reason IS NOT NULL)
      OR (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS command_receipts_command_uidx
  ON command_receipts (tenant_id, command_id) WHERE NOT is_deleted;

CREATE TABLE IF NOT EXISTS audit_evidence (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  actor_ref uuid NOT NULL,
  actor_display text,
  actor_role text,
  client_ip text,
  action text NOT NULL,
  resource_type text NOT NULL DEFAULT 'TEMPLATE' CHECK (length(trim(resource_type)) > 0),
  resource_ref text NOT NULL,
  severity text NOT NULL DEFAULT 'INFO' CHECK (severity IN ('INFO', 'WARN', 'CRITICAL')),
  command_id uuid,
  before_digest text,
  after_digest text,
  details jsonb,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
  created_by text NOT NULL,
  updated_by text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by text,
  delete_reason text,
  PRIMARY KEY (tenant_id, id),
  CHECK (length(trim(action)) > 0 AND length(trim(resource_ref)) > 0),
  CHECK (NOT is_deleted AND deleted_at IS NULL AND deleted_by IS NULL AND delete_reason IS NULL)
);
CREATE INDEX IF NOT EXISTS audit_evidence_keyset_idx
  ON audit_evidence (tenant_id, occurred_at DESC, id DESC);

-- 7. Technical Dispatch Queue (backing DispatchQueue port)
CREATE TABLE IF NOT EXISTS notification_dispatch_jobs (
  id bigserial PRIMARY KEY,
  tenant_id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  lane text NOT NULL CHECK (lane IN ('transactional', 'normal', 'bulk')),
  priority smallint NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 3),
  status text NOT NULL CHECK (status IN ('QUEUED', 'SENDING', 'DEFERRED', 'COMPLETED', 'FAILED')),
  scheduled_at timestamptz NOT NULL,
  lease_until timestamptz,
  lease_token uuid,
  attempts int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, delivery_id),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES deliveries (tenant_id, id),
  CHECK ((lease_until IS NULL) = (lease_token IS NULL))
);
CREATE INDEX IF NOT EXISTS notification_dispatch_jobs_ready_idx
  ON notification_dispatch_jobs (lane, priority ASC, scheduled_at, id)
  WHERE status IN ('QUEUED', 'DEFERRED');

-- 8. Immutability & Append-Only Triggers
CREATE OR REPLACE FUNCTION reject_notification_audit_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'notification audit_evidence is append-only';
END;
$$;

DROP TRIGGER IF EXISTS notification_audit_no_mutation ON audit_evidence;
CREATE TRIGGER notification_audit_no_mutation
BEFORE UPDATE OR DELETE ON audit_evidence
FOR EACH ROW EXECUTE FUNCTION reject_notification_audit_mutation();

CREATE OR REPLACE FUNCTION reject_notification_breaker_transition_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'notification breaker_transitions is append-only';
END;
$$;

DROP TRIGGER IF EXISTS notification_breaker_transitions_no_mutation ON breaker_transitions;
CREATE TRIGGER notification_breaker_transitions_no_mutation
BEFORE UPDATE OR DELETE ON breaker_transitions
FOR EACH ROW EXECUTE FUNCTION reject_notification_breaker_transition_mutation();

CREATE OR REPLACE FUNCTION guard_notification_published_artifact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  mutable_cols constant text[] := ARRAY['status', 'updated_at', 'updated_by', 'row_version', 'approval_ref'];
BEGIN
  IF OLD.status IN ('PUBLISHED', 'DEPRECATED', 'RETIRED') THEN
    IF TG_OP = 'DELETE' THEN
      RAISE EXCEPTION '% row % cannot be deleted once %', TG_TABLE_NAME, OLD.id, OLD.status;
    END IF;
    IF NEW.is_deleted OR NEW.status NOT IN ('PUBLISHED', 'DEPRECATED', 'RETIRED') THEN
      RAISE EXCEPTION '% row % cannot revert or soft-delete once %', TG_TABLE_NAME, OLD.id, OLD.status;
    END IF;
    IF (to_jsonb(NEW) - mutable_cols) IS DISTINCT FROM (to_jsonb(OLD) - mutable_cols) THEN
      RAISE EXCEPTION '% row % content and schema are immutable once %', TG_TABLE_NAME, OLD.id, OLD.status;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS notification_immutable_published_guard ON template_versions;
CREATE TRIGGER notification_immutable_published_guard
BEFORE UPDATE OR DELETE ON template_versions
FOR EACH ROW EXECUTE FUNCTION guard_notification_published_artifact();

DROP TRIGGER IF EXISTS notification_immutable_published_guard ON layout_versions;
CREATE TRIGGER notification_immutable_published_guard
BEFORE UPDATE OR DELETE ON layout_versions
FOR EACH ROW EXECUTE FUNCTION guard_notification_published_artifact();

DROP TRIGGER IF EXISTS notification_immutable_published_guard ON provider_revisions;
CREATE TRIGGER notification_immutable_published_guard
BEFORE UPDATE OR DELETE ON provider_revisions
FOR EACH ROW EXECUTE FUNCTION guard_notification_published_artifact();

DROP TRIGGER IF EXISTS notification_immutable_published_guard ON routing_policy_versions;
CREATE TRIGGER notification_immutable_published_guard
BEFORE UPDATE OR DELETE ON routing_policy_versions
FOR EACH ROW EXECUTE FUNCTION guard_notification_published_artifact();

DROP TRIGGER IF EXISTS notification_immutable_published_guard ON trigger_rule_revisions;
CREATE TRIGGER notification_immutable_published_guard
BEFORE UPDATE OR DELETE ON trigger_rule_revisions
FOR EACH ROW EXECUTE FUNCTION guard_notification_published_artifact();

CREATE OR REPLACE FUNCTION guard_notification_published_variant()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  target_tenant uuid;
  target_version uuid;
  parent_status text;
BEGIN
  IF TG_OP = 'DELETE' THEN
    target_tenant := OLD.tenant_id;
    target_version := OLD.template_version_id;
  ELSE
    target_tenant := NEW.tenant_id;
    target_version := NEW.template_version_id;
  END IF;

  SELECT status INTO parent_status
  FROM template_versions
  WHERE tenant_id = target_tenant AND id = target_version;

  IF parent_status IN ('PUBLISHED', 'DEPRECATED', 'RETIRED') THEN
    RAISE EXCEPTION 'template_variants under % template_version % are immutable', parent_status, target_version;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS notification_immutable_published_guard ON template_variants;
CREATE TRIGGER notification_immutable_published_guard
BEFORE INSERT OR UPDATE OR DELETE ON template_variants
FOR EACH ROW EXECUTE FUNCTION guard_notification_published_variant();
