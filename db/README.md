# BPMP database migrations

PostgreSQL state is owned by bounded context:

| Directory | Owner |
|---|---|
| `human-runtime/migrations` | Human Runtime |
| `configuration-service/migrations` | Configuration Service |
| `projection-service/migrations` | Projection Service |
| `governance-service/migrations` | Governance Service |

No service may read or write another service's database. Engine workflow state
is authoritative in RocksDB replicated through Raft and has no PostgreSQL DDL.

## Migration contract

- Name files `NNN_lowercase_description.sql` and increase the number.
- Make a migration forward-only. Never rewrite a file after it has been
  applied to any retained environment.
- Prefer additive, backward-compatible changes. Separate expand and contract
  across releases when old and new binaries overlap.
- Keep a bounded-context migration transactional. The runner removes
  top-level `BEGIN;` and `COMMIT;` markers and supplies one transaction that
  also records the checksum ledger entry.
- New mutable domain entities require tenant-scoped identity where applicable,
  an optimistic `version`, explicit `is_deleted` semantics and audit metadata.
  Append-only inbox, outbox, checkpoint and audit records may use immutable
  identity/sequence semantics instead of pretending to be mutable entities.
- Audit tables must reject `UPDATE` and `DELETE`.
- Build indexes from actual predicates and ordering; verify large-table changes
  with a representative PostgreSQL query plan.

The runner and schema contract checks are under `db/scripts/`. They are
executed by the one-shot jobs in
`platform/local/compose.infrastructure.yaml`.

## Create a migration

Example:

```sql
ALTER TABLE work_items
    ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0;

ALTER TABLE work_items
    DROP CONSTRAINT IF EXISTS work_items_priority_check;

ALTER TABLE work_items
    ADD CONSTRAINT work_items_priority_check CHECK (priority >= 0);
```

After adding the ordered file:

```powershell
.\platform\local\manage.ps1 migrate
.\platform\local\manage.ps1 migrate
```

The second execution must report every migration as skipped and all schema
contracts as passed.
