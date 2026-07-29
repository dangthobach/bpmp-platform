#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: verify-schema.sh <namespace>" >&2
  exit 2
fi

namespace=$1
psql_args=(-X --no-psqlrc --set=ON_ERROR_STOP=1 --quiet --tuples-only --no-align)

for variable in PGHOST PGPORT PGDATABASE PGUSER PGPASSWORD; do
  if [[ -z "${!variable:-}" ]]; then
    echo "$variable is required" >&2
    exit 2
  fi
done

assert_zero() {
  local description=$1
  local query=$2
  local actual
  actual=$(psql "${psql_args[@]}" --command="$query")
  if [[ "$actual" != "0" ]]; then
    echo "schema contract failed: $description (violations=$actual)" >&2
    exit 1
  fi
}

assert_one() {
  local description=$1
  local query=$2
  local actual
  actual=$(psql "${psql_args[@]}" --command="$query")
  if [[ "$actual" != "1" ]]; then
    echo "schema contract failed: $description (actual=$actual)" >&2
    exit 1
  fi
}

assert_one "migration ledger exists" \
  "SELECT count(*) FROM pg_class WHERE oid = to_regclass('public.bpmp_schema_migrations');"
assert_one "migration ledger is append-only" \
  "SELECT count(*) FROM pg_trigger WHERE tgrelid = 'bpmp_schema_migrations'::regclass AND tgname = 'bpmp_schema_migrations_no_mutation' AND tgenabled <> 'D';"

case "$namespace" in
  human-runtime)
    assert_zero "Human Runtime required tables" "
      SELECT count(*) FROM unnest(ARRAY[
        'assignment_policies','work_items','human_audit_log','human_event_inbox',
        'human_cases','human_case_plan_items','escalation_outbox',
        'human_tenant_security_profiles','human_actor_revoke_epochs'
      ]) AS required(name)
      WHERE to_regclass('public.' || required.name) IS NULL;"
    assert_zero "Human Runtime mutable entities expose version and is_deleted" "
      SELECT count(*) FROM (VALUES
        ('assignment_policies'),('work_items'),('human_cases'),
        ('human_case_plan_items'),('escalation_outbox'),
        ('human_tenant_security_profiles'),('human_actor_revoke_epochs')
      ) AS entity(table_name)
      CROSS JOIN (VALUES ('version'),('is_deleted')) AS required(column_name)
      WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns c
        WHERE c.table_schema = 'public'
          AND c.table_name = entity.table_name
          AND c.column_name = required.column_name
      );"
    assert_one "Human Runtime audit trigger" \
      "SELECT count(*) FROM pg_trigger WHERE tgrelid = 'human_audit_log'::regclass AND tgname = 'human_audit_immutable' AND tgenabled <> 'D';"
    assert_one "Human Runtime delegation depth column" \
      "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'work_items' AND column_name = 'delegation_depth';"
    ;;
  configuration-service)
    assert_zero "Configuration Service required tables" "
      SELECT count(*) FROM unnest(ARRAY[
        'configuration_profiles','configuration_versions','configuration_active_scopes',
        'configuration_audit','configuration_outbox',
        'configuration_outbox_publish_state','configuration_idempotency'
      ]) AS required(name)
      WHERE to_regclass('public.' || required.name) IS NULL;"
    assert_one "Configuration profile optimistic version" \
      "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'configuration_profiles' AND column_name = 'aggregate_version';"
    assert_one "Configuration profile soft delete" \
      "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'configuration_profiles' AND column_name = 'is_deleted';"
    assert_one "Configuration audit trigger" \
      "SELECT count(*) FROM pg_trigger WHERE tgrelid = 'configuration_audit'::regclass AND tgname = 'configuration_audit_no_update' AND tgenabled <> 'D';"
    assert_one "Configuration outbox sequence uniqueness" \
      "SELECT count(*) FROM pg_constraint WHERE conrelid = 'configuration_outbox'::regclass AND conname = 'configuration_outbox_event_sequence_key' AND contype = 'u';"
    ;;
  projection-service)
    assert_zero "Projection Service required tables" "
      SELECT count(*) FROM unnest(ARRAY[
        'projection_event_inbox','projection_checkpoints','workflow_instance_read_models'
      ]) AS required(name)
      WHERE to_regclass('public.' || required.name) IS NULL;"
    assert_zero "Projection read model exposes version and is_deleted" "
      SELECT count(*) FROM (VALUES ('version'),('is_deleted')) AS required(column_name)
      WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns c
        WHERE c.table_schema = 'public'
          AND c.table_name = 'workflow_instance_read_models'
          AND c.column_name = required.column_name
      );"
    assert_one "Projection immutable identity trigger" \
      "SELECT count(*) FROM pg_trigger WHERE tgrelid = 'workflow_instance_read_models'::regclass AND tgname = 'projection_read_model_identity_guard' AND tgenabled <> 'D';"
    ;;
  governance-service)
    assert_zero "Governance Service required tables" "
      SELECT count(*) FROM unnest(ARRAY[
        'governance_approval_requests','governance_signed_approvals',
        'governance_service_audit'
      ]) AS required(name)
      WHERE to_regclass('public.' || required.name) IS NULL;"
    assert_one "Governance aggregate optimistic version" \
      "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'governance_approval_requests' AND column_name = 'aggregate_version';"
    assert_one "Governance audit trigger" \
      "SELECT count(*) FROM pg_trigger WHERE tgrelid = 'governance_service_audit'::regclass AND tgname = 'governance_audit_no_update' AND tgenabled <> 'D';"
    assert_one "Governance approval ownership foreign key" \
      "SELECT count(*) FROM pg_constraint WHERE conrelid = 'governance_signed_approvals'::regclass AND contype = 'f';"
    ;;
  *)
    echo "unsupported schema namespace: $namespace" >&2
    exit 2
    ;;
esac

echo "schema contract passed: namespace=$namespace database=$PGDATABASE"
