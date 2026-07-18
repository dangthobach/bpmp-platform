# AuthZ database migrations

`crates/authz-db/migrations/` is the only DDL source for both SQLx and Flyway.
Do not add a second migration mirror under `db/`; independent copies previously
produced incompatible schemas.

## Naming

Canonical files use SQLx naming: `{version}_{description}.sql`, for example
`011_entity_metadata.sql`. `db/flyway.conf.example` configures Flyway with an
empty migration prefix and `_` separator so it consumes the same files.

## Entity contract

Every mutable control-plane entity has:

- `version BIGINT NOT NULL DEFAULT 0`: optimistic-lock token.
- `is_deleted BOOLEAN NOT NULL DEFAULT false`: canonical soft-delete flag.
- `deleted_at`, `deleted_by`: deletion audit tuple.
- `created_at`, `created_by`, `updated_at`, `updated_by`: audit metadata.

The database trigger increments `version` and sets `updated_at` for every
update. Repository updates must either:

1. use `WHERE version = $expected_version` and report a version conflict; or
2. lock the row with `SELECT ... FOR UPDATE` in a short transaction when a
   multi-row invariant requires pessimistic serialization.

Policy promotion uses both: row locks serialize the policy lifecycle, while
the entity version remains visible to API clients and audit tooling.

Append-only audit/history records and derived projections intentionally do not
have `is_deleted` or mutable `version` fields:

- `authz_decision_log`
- `user_attribute_history`
- `policy_shadow_log`
- `relation_reachability`

## SQLx

The Rust service calls `sqlx::migrate!` against the canonical directory.

```powershell
$env:DATABASE_URL = "postgres://authz:authz_secret@localhost:5432/authz_db"
sqlx migrate run --source crates/authz-db/migrations
```

## Flyway

Create a local configuration from the example and provide credentials through
environment variables or a secret manager.

```powershell
Copy-Item db/flyway.conf.example db/flyway.conf
flyway -configFiles=db/flyway.conf validate
flyway -configFiles=db/flyway.conf migrate
flyway -configFiles=db/flyway.conf info
```

Docker uses the same canonical mount:

```powershell
docker run --rm `
  -v "${PWD}/crates/authz-db/migrations:/flyway/sql:ro" `
  redgate/flyway `
  -url="jdbc:postgresql://host.docker.internal:5432/authz_db" `
  -user=authz `
  -password=$env:AUTHZ_DB_PASSWORD `
  -locations=filesystem:/flyway/sql `
  -sqlMigrationPrefix= `
  -sqlMigrationSeparator=_ `
  migrate
```

`flyway.cleanDisabled=true` is mandatory. Production must have one migration
owner: disable runtime SQLx migration when the deployment pipeline uses Flyway.
