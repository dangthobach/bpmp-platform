# BPMP local infrastructure

This Compose project provides persistent local infrastructure for service
development:

- one PostgreSQL database and credential boundary for each of Human Runtime,
  Configuration Service, Projection Service and Governance Service;
- Redpanda as the Kafka-compatible broker;
- Redis for API Gateway rate limiting;
- an OTLP collector with a debug trace exporter;
- one-shot, checksum-tracked DDL jobs and idempotent Kafka topic provisioning.

It does not run application containers. Use the broker-backed E2E topology for
the complete three-node Engine and UI flow:

```powershell
.\platform\e2e\run.ps1
```

## Start

Docker Engine and Compose v2 are required. From the repository root:

```powershell
.\platform\local\manage.ps1 init
```

The command creates the ignored `platform/local/.env` file from
`.env.example`, then validates interpolation. Review credentials and host ports
before using a shared workstation.

Start infrastructure, apply all pending DDL and create Kafka topics:

```powershell
.\platform\local\manage.ps1 up
```

`up` is repeatable. Existing migrations and Kafka topics are verified and
skipped. Application services should set `apply_migrations` to `false` when
using these dedicated migration jobs.

## Operations

```powershell
# Re-run migration and schema contract checks.
.\platform\local\manage.ps1 migrate

# Verify databases, topics and container health.
.\platform\local\manage.ps1 verify

# Show containers, including stopped one-shot jobs.
.\platform\local\manage.ps1 status

# Read all logs or one service.
.\platform\local\manage.ps1 logs
.\platform\local\manage.ps1 logs -Service redpanda

# Stop containers while retaining data.
.\platform\local\manage.ps1 down

# Explicitly destroy only this Compose project's local volumes.
.\platform\local\manage.ps1 reset -ConfirmReset
```

The reset command is intentionally guarded. It deletes all local PostgreSQL,
Kafka and Redis data owned by the configured Compose project.

## Default host endpoints

Every value is configurable in the ignored `.env` file.

| Component | Host endpoint |
|---|---|
| Human Runtime PostgreSQL | `localhost:15432` |
| Configuration PostgreSQL | `localhost:15433` |
| Projection PostgreSQL | `localhost:15434` |
| Governance PostgreSQL | `localhost:15435` |
| Kafka | `localhost:19092` |
| Redpanda Admin API | `localhost:19644` |
| Redis | `localhost:16379` |
| OTLP gRPC | `localhost:14317` |

Containers on the Compose network use `redpanda:9092`, `redis:6379` and the
service-specific PostgreSQL DNS names from `compose.infrastructure.yaml`.

## DDL behavior

Each bounded context owns its migration directory under `db/`. A migration
filename must match `NNN_name.sql`. The migration runner:

1. waits for the owning PostgreSQL database;
2. takes a session advisory lock scoped by bounded context;
3. verifies the SHA-256 checksum of every previously applied file;
4. applies each pending file and its ledger row atomically;
5. rejects mutation or deletion of the append-only migration ledger;
6. checks required tables, optimistic versions, soft-delete metadata and
   immutable audit triggers relevant to that context.

Never edit an applied migration. Add the next ordered file. A checksum mismatch
is a deployment failure and requires restoring the original migration bytes,
not changing the ledger.

The database containers use local owner credentials for convenience. A
production environment must use a privileged migration identity separately
from a less-privileged runtime identity and obtain both from the secret
manager.

## Backup and restore

Create logical backups before applying DDL to retained local data:

```powershell
docker compose --env-file platform/local/.env `
  -f platform/local/compose.infrastructure.yaml `
  exec -T human-postgres `
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' `
  > human-runtime.dump
```

Restore only into an empty compatible database, then run `manage.ps1 migrate`
to apply newer migrations.
The Engine authoritative RocksDB/Raft volumes are not part of this local
infrastructure Compose project and require the separate snapshot/restore
procedure in `docs/production-deployment-process.md`.
