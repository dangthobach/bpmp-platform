# Contributing

## Development Setup

Install the Rust toolchain declared in `rust-toolchain.toml` and Buf. On
Windows, Buf can be installed with:

```powershell
winget install --id bufbuild.buf --exact
```

Keep configuration externalized and versioned. Deterministic domain code must
not read wall clock, randomness, network, database, filesystem, or environment
state. Tenant scope is mandatory across durable contracts and persistence keys.

## Changes

- Keep business rules in the owning bounded context and I/O in adapters.
- Add Protobuf fields compatibly; never reuse removed field numbers.
- Add unit tests for examples and property tests for universal invariants.
- Tag requirement properties as documented in `tests/README.md`.
- Do not commit secrets, generated credentials, or regulated data.

Run the full gate before submitting a change:

```powershell
.\tools\check.ps1
```

For changes to the Wasmtime adapter, also run:

```powershell
.\tools\check-wasmtime.ps1
```

Protocol changes must pass both `buf lint` and the checked-in breaking-change
baseline. Explain durable schema, migration, replay, and rollback implications
in the pull request.

## Entity and API design rules

New or refactored persisted business entities must compose the AuditEntity,
SoftDeletable and VersionedEntity contracts: trusted created/updated actors and
timestamps, soft-delete metadata and a monotonic optimistic concurrency counter.
Repository mutations must enforce tenant scope and compare-and-set versions;
public CRUD deletion must not physically remove business records. Immutable
published content, audit evidence and technical inbox/checkpoint storage follow
the explicit safety mappings in [entity conventions](docs/refactor-prototype/entity-conventions.md),
so soft deletion cannot invalidate replay or idempotency.

API changes must trace a business intent/UI action to an owning operation and
keep the embedded Gateway OpenAPI, route registry and Protobuf compatible.
See the [API design specification](docs/refactor-prototype/api-spec.md) for the
current/proposed catalog and unresolved contract gates. Proposed operations are
not deployed endpoints and must not be published as the runtime contract.

## Backend operational requirements

All newly owned entity IDs must be UUIDs (native PostgreSQL uuid for PK/FK),
with explicit compatibility mapping for existing opaque IDs. Generate identities
outside the deterministic core and preserve them across retries.

Every long-running backend deployable must expose GET /healthz, GET /readyz
and GET /metrics on a configured management surface. Preserve existing /livez
aliases during rollout and qualify the metrics format for the actual scraper.

Every outbox/log owner must ship a configured periodic cleanup job, with
retention, bounded batches, lease/fencing, recovery and metrics. Never purge
pending outbox, required local-reader/checkpoint data or held audit evidence.
See [backend standards](docs/refactor-prototype/backend-operations.md) for
eligibility, archive and service-specific rollout gates.
