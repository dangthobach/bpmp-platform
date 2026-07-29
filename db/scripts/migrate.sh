#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: migrate.sh <migration-directory> <namespace>" >&2
  exit 2
fi

migration_dir=$1
namespace=$2
wait_timeout_seconds=${MIGRATION_WAIT_TIMEOUT_SECONDS:-60}

if [[ ! -d "$migration_dir" ]]; then
  echo "migration directory does not exist: $migration_dir" >&2
  exit 2
fi
if [[ ! "$namespace" =~ ^[a-z0-9][a-z0-9._-]{0,62}$ ]]; then
  echo "migration namespace is invalid: $namespace" >&2
  exit 2
fi
if [[ ! "$wait_timeout_seconds" =~ ^[1-9][0-9]*$ ]]; then
  echo "MIGRATION_WAIT_TIMEOUT_SECONDS must be a positive integer" >&2
  exit 2
fi

for variable in PGHOST PGPORT PGDATABASE PGUSER PGPASSWORD; do
  if [[ -z "${!variable:-}" ]]; then
    echo "$variable is required" >&2
    exit 2
  fi
done

deadline=$((SECONDS + wait_timeout_seconds))
until pg_isready -q; do
  if (( SECONDS >= deadline )); then
    echo "PostgreSQL did not become ready within ${wait_timeout_seconds}s" >&2
    exit 1
  fi
  sleep 1
done

psql_args=(-X --no-psqlrc --set=ON_ERROR_STOP=1)

psql "${psql_args[@]}" <<'SQL'
CREATE TABLE IF NOT EXISTS bpmp_schema_migrations (
    namespace text NOT NULL,
    migration_id text NOT NULL,
    checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    applied_by text NOT NULL DEFAULT current_user,
    version bigint NOT NULL DEFAULT 1 CHECK (version = 1),
    is_deleted boolean NOT NULL DEFAULT false CHECK (NOT is_deleted),
    PRIMARY KEY (namespace, migration_id)
);

CREATE OR REPLACE FUNCTION bpmp_reject_schema_migration_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'bpmp_schema_migrations is append-only';
END;
$$;

DROP TRIGGER IF EXISTS bpmp_schema_migrations_no_mutation ON bpmp_schema_migrations;
CREATE TRIGGER bpmp_schema_migrations_no_mutation
BEFORE UPDATE OR DELETE ON bpmp_schema_migrations
FOR EACH ROW EXECUTE FUNCTION bpmp_reject_schema_migration_mutation();
SQL

coproc LOCK_SESSION {
  psql "${psql_args[@]}" --quiet --tuples-only --no-align
}
lock_input=${LOCK_SESSION[1]}
lock_output=${LOCK_SESSION[0]}
lock_pid=$LOCK_SESSION_PID

cleanup() {
  exec {lock_input}>&- 2>/dev/null || true
  exec {lock_output}<&- 2>/dev/null || true
  wait "$lock_pid" 2>/dev/null || true
}
trap cleanup EXIT

printf "SELECT 'LOCKED' FROM (SELECT pg_advisory_lock(hashtextextended('%s', 0))) AS lock;\n" \
  "$namespace" >&"$lock_input"

lock_state=
while IFS= read -r lock_state <&"$lock_output"; do
  [[ "$lock_state" == "LOCKED" ]] && break
done
if [[ "$lock_state" != "LOCKED" ]]; then
  echo "failed to acquire migration lock for $namespace" >&2
  exit 1
fi

mapfile -d '' migrations < <(
  find "$migration_dir" -maxdepth 1 -type f -name '*.sql' -print0 | sort -z
)
if (( ${#migrations[@]} == 0 )); then
  echo "no SQL migrations found in $migration_dir" >&2
  exit 1
fi

declare -A seen_ids=()
applied=0
skipped=0

for migration in "${migrations[@]}"; do
  migration_id=$(basename "$migration")
  if [[ ! "$migration_id" =~ ^[0-9]{3,}_[a-z0-9][a-z0-9_]*\.sql$ ]]; then
    echo "migration filename must match NNN_name.sql: $migration_id" >&2
    exit 1
  fi
  if [[ -n "${seen_ids[$migration_id]:-}" ]]; then
    echo "duplicate migration id: $migration_id" >&2
    exit 1
  fi
  seen_ids[$migration_id]=1

  checksum=$(sha256sum "$migration" | cut -d ' ' -f 1)
  recorded_checksum=$(
    psql "${psql_args[@]}" --quiet --tuples-only --no-align \
      --command="SELECT checksum_sha256 FROM bpmp_schema_migrations WHERE namespace = '$namespace' AND migration_id = '$migration_id';"
  )

  if [[ -n "$recorded_checksum" ]]; then
    if [[ "$recorded_checksum" != "$checksum" ]]; then
      echo "checksum mismatch for applied migration $namespace/$migration_id" >&2
      exit 1
    fi
    echo "skip $namespace/$migration_id"
    skipped=$((skipped + 1))
    continue
  fi

  normalized=$(mktemp)
  driver=$(mktemp)
  sed -E '/^[[:space:]]*(BEGIN|COMMIT)[[:space:]]*;[[:space:]]*$/Id' \
    "$migration" >"$normalized"
  cat >"$driver" <<SQL
\set ON_ERROR_STOP on
BEGIN;
\i $normalized
INSERT INTO bpmp_schema_migrations(namespace, migration_id, checksum_sha256)
VALUES (:'namespace', :'migration_id', :'checksum');
COMMIT;
SQL

  echo "apply $namespace/$migration_id"
  if ! psql "${psql_args[@]}" \
      --set=namespace="$namespace" \
      --set=migration_id="$migration_id" \
      --set=checksum="$checksum" \
      --file="$driver"; then
    rm -f "$normalized" "$driver"
    exit 1
  fi
  rm -f "$normalized" "$driver"
  applied=$((applied + 1))
done

recorded_count=$(
  psql "${psql_args[@]}" --quiet --tuples-only --no-align \
    --command="SELECT count(*) FROM bpmp_schema_migrations WHERE namespace = '$namespace';"
)
if [[ "$recorded_count" != "${#migrations[@]}" ]]; then
  echo "migration ledger count mismatch for $namespace: files=${#migrations[@]} ledger=$recorded_count" >&2
  exit 1
fi

echo "migration complete: namespace=$namespace applied=$applied skipped=$skipped"
