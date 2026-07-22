BEGIN;

CREATE TABLE assignment_policies (
    tenant_id text NOT NULL,
    policy_ref text NOT NULL,
    workflow_type text NOT NULL,
    workflow_version text NOT NULL,
    node_id text NOT NULL,
    assignee_id text,
    candidate_group text,
    sla_duration_ms bigint NOT NULL DEFAULT 0 CHECK (sla_duration_ms >= 0),
    escalation_policy_ref text,
    config_version text NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    created_by text NOT NULL,
    updated_at timestamptz NOT NULL,
    updated_by text NOT NULL,
    PRIMARY KEY (tenant_id, policy_ref, workflow_type, workflow_version, node_id),
    CHECK ((assignee_id IS NULL) <> (candidate_group IS NULL))
);

CREATE TABLE work_items (
    tenant_id text NOT NULL,
    work_item_id text NOT NULL,
    activation_event_id text NOT NULL,
    instance_id text NOT NULL,
    workflow_type text NOT NULL,
    workflow_version text NOT NULL,
    node_id text NOT NULL,
    task_type text NOT NULL,
    assignment_policy_ref text NOT NULL,
    assignee_id text,
    candidate_group text,
    form_key text,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'COMPLETION_REQUESTED', 'COMPLETED', 'CANCELLED')),
    decision text,
    sla_deadline timestamptz,
    escalation_policy_ref text,
    completion_command_id text,
    version bigint NOT NULL CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, work_item_id),
    UNIQUE (tenant_id, activation_event_id),
    UNIQUE NULLS NOT DISTINCT (tenant_id, completion_command_id),
    CHECK ((assignee_id IS NULL) <> (candidate_group IS NULL))
);

CREATE INDEX work_items_actor_active_idx
    ON work_items (tenant_id, assignee_id, status, updated_at DESC)
    WHERE NOT is_deleted;
CREATE INDEX work_items_group_active_idx
    ON work_items (tenant_id, candidate_group, status, updated_at DESC)
    WHERE NOT is_deleted;
CREATE INDEX work_items_sla_due_idx
    ON work_items (sla_deadline, tenant_id, work_item_id)
    WHERE status = 'ACTIVE' AND NOT is_deleted AND sla_deadline IS NOT NULL;

CREATE TABLE human_audit_log (
    tenant_id text NOT NULL,
    audit_id text NOT NULL,
    work_item_id text,
    case_id text,
    actor_id text NOT NULL,
    action text NOT NULL,
    occurred_at timestamptz NOT NULL,
    command_id text,
    correlation_id text,
    from_version bigint,
    to_version bigint,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_deleted boolean NOT NULL DEFAULT false CHECK (NOT is_deleted),
    version bigint NOT NULL DEFAULT 1 CHECK (version = 1),
    PRIMARY KEY (tenant_id, audit_id)
);

CREATE OR REPLACE FUNCTION reject_human_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'human_audit_log is append-only';
END;
$$;

CREATE TRIGGER human_audit_immutable
BEFORE UPDATE OR DELETE ON human_audit_log
FOR EACH ROW EXECUTE FUNCTION reject_human_audit_mutation();

CREATE TABLE human_event_inbox (
    tenant_id text NOT NULL,
    consumer_name text NOT NULL,
    event_id text NOT NULL,
    stream_id text NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    processed_at timestamptz NOT NULL,
    is_deleted boolean NOT NULL DEFAULT false CHECK (NOT is_deleted),
    version bigint NOT NULL DEFAULT 1 CHECK (version = 1),
    PRIMARY KEY (tenant_id, consumer_name, event_id),
    UNIQUE (tenant_id, consumer_name, stream_id, sequence)
);

CREATE TABLE human_cases (
    tenant_id text NOT NULL,
    case_id text NOT NULL,
    case_type text NOT NULL,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'COMPLETED')),
    version bigint NOT NULL CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, case_id)
);

CREATE TABLE human_case_plan_items (
    tenant_id text NOT NULL,
    case_id text NOT NULL,
    plan_item_id text NOT NULL,
    plan_item_kind text NOT NULL CHECK (plan_item_kind IN ('STAGE', 'MILESTONE')),
    status text NOT NULL CHECK (status IN ('AVAILABLE', 'ACTIVE', 'COMPLETED')),
    version bigint NOT NULL CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, case_id, plan_item_id),
    FOREIGN KEY (tenant_id, case_id) REFERENCES human_cases (tenant_id, case_id)
);

CREATE TABLE escalation_outbox (
    tenant_id text NOT NULL,
    escalation_id text NOT NULL,
    work_item_id text NOT NULL,
    escalation_policy_ref text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL,
    published_at timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_owner text,
    lease_expires_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, escalation_id),
    UNIQUE (tenant_id, work_item_id, escalation_policy_ref)
);

CREATE TABLE human_tenant_security_profiles (
    tenant_id text PRIMARY KEY,
    encryption_key_scope text NOT NULL,
    config_version text NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(trim(encryption_key_scope)) > 0),
    CHECK (length(trim(config_version)) > 0)
);

CREATE TABLE human_actor_revoke_epochs (
    tenant_id text NOT NULL,
    actor_id text NOT NULL,
    revoke_epoch bigint NOT NULL CHECK (revoke_epoch >= 0),
    config_version text NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    is_deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, actor_id)
);

COMMIT;
