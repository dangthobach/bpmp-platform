#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
native_env="${repo_root}/platform/local/.env.native"

if [[ ! -f "$native_env" ]]; then
  echo "Missing $native_env; copy .env.native.example to .env.native first." >&2
  exit 2
fi

# shellcheck disable=SC1090
source "$native_env"
: "${POSTGRES_HOST:=127.0.0.1}"
: "${POSTGRES_PORT:=5432}"
: "${POSTGRES_DB:=bpmp_platform}"
: "${POSTGRES_USER:=bachdt}"
: "${MIGRATION_WAIT_TIMEOUT_SECONDS:=60}"

export PGHOST="$POSTGRES_HOST" PGPORT="$POSTGRES_PORT" PGDATABASE=postgres PGUSER="$POSTGRES_USER" PGPASSWORD="${POSTGRES_PASSWORD:-}"

psql -X --set=ON_ERROR_STOP=1 <<SQL
SELECT format('CREATE DATABASE %I', '$POSTGRES_DB')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$POSTGRES_DB')\gexec
SQL

export PGDATABASE="$POSTGRES_DB"
for schema in human_runtime configuration projection governance; do
  psql -X --set=ON_ERROR_STOP=1 -c "CREATE SCHEMA IF NOT EXISTS \"$schema\" AUTHORIZATION \"$POSTGRES_USER\";"
done

run_migration() {
  local schema="$1" namespace="$2" directory="$3"
  PGSCHEMA="$schema" "$repo_root/db/scripts/migrate.sh" "$repo_root/$directory" "$namespace"
  PGSCHEMA="$schema" "$repo_root/db/scripts/verify-schema.sh" "$namespace"
}

run_migration human_runtime human-runtime db/human-runtime/migrations
run_migration configuration configuration-service db/configuration-service/migrations
run_migration projection projection-service db/projection-service/migrations
run_migration governance governance-service db/governance-service/migrations

echo "Native PostgreSQL ready: $POSTGRES_DB on $POSTGRES_HOST:$POSTGRES_PORT"
